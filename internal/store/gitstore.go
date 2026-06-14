package store

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// GitStore implements Store by cloning a git repo and polling for changes.
// When a VocabularyPath is configured, the vocabulary file is re-read on every
// poll cycle and validated atomically with the agent entries (snapshot pattern).
type GitStore struct {
	url      string
	ref      string
	cacheDir string
	subpath  string
	keyFile  string
	poll     time.Duration
	// vocabPath is the path within the cloned repo to the vocabulary file.
	// Empty means ValidateOpen.
	vocabPath string
	mode      ValidationMode

	reloadMu sync.Mutex   // serializes reload candidate building
	mu       sync.RWMutex // protects snap; held only for swap and reads
	snap     snapshot

	done chan struct{}
}

// GitStoreConfig holds configuration for a GitStore.
type GitStoreConfig struct {
	URL      string
	Ref      string        // default: main
	CacheDir string        // where to clone
	Subpath  string        // subdirectory within repo for registry files
	KeyFile  string        // SSH deploy key or HTTPS token file (optional)
	Poll     time.Duration // default: 60s
	// VocabularyPath is the path within the cloned repo to a vocabulary.v1.yaml
	// file. When set, schema_version: 2 entries are validated against it.
	// When empty, vocabulary checking is skipped (ValidateOpen).
	VocabularyPath string
	// Ext holds optional vocabulary extensions.
	//
	// Deprecated: use VocabularyPath and a vocabulary.v1.yaml file instead.
	Ext VocabularyExtensions
}

// NewGitStore clones the repo and starts the poll loop.
func NewGitStore(cfg GitStoreConfig) (*GitStore, error) {
	if cfg.Ref == "" {
		cfg.Ref = "main"
	}
	if cfg.Poll == 0 {
		cfg.Poll = 60 * time.Second
	}
	gs := &GitStore{
		url:       cfg.URL,
		ref:       cfg.Ref,
		cacheDir:  cfg.CacheDir,
		subpath:   cfg.Subpath,
		keyFile:   cfg.KeyFile,
		poll:      cfg.Poll,
		vocabPath: cfg.VocabularyPath,
		done:      make(chan struct{}),
	}

	repoDir := gs.repoDir()
	if _, err := os.Stat(filepath.Join(repoDir, ".git")); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(repoDir), 0700); err != nil {
			return nil, fmt.Errorf("creating cache dir: %w", err)
		}
		if err := gs.runGit("", "clone", "--depth=1", "--branch", cfg.Ref, cfg.URL, repoDir); err != nil {
			return nil, fmt.Errorf("git clone: %w", err)
		}
	}

	vocab, mode, err := gs.loadVocab(cfg.Ext)
	if err != nil {
		return nil, err
	}
	gs.mode = mode

	if err := gs.reload(vocab); err != nil {
		return nil, err
	}

	go gs.pollLoop()
	return gs, nil
}

// loadVocab resolves the vocabulary for the current checkout.
// It reads the vocabulary file from the repo working tree (if configured)
// and applies any deprecated VocabularyExtensions shim.
func (g *GitStore) loadVocab(ext VocabularyExtensions) (*vocabulary, ValidationMode, error) {
	if g.vocabPath == "" {
		return resolveVocabulary("", ext)
	}
	absPath, err := resolveGitVocabPath(g.repoDir(), g.vocabPath)
	if err != nil {
		return nil, ValidateOpen, err
	}
	return resolveVocabulary(absPath, ext)
}

// resolveGitVocabPath joins repoDir and relPath and checks for path traversal.
// relPath must not contain ".." components.
//
// The check runs on the *cleaned* path (after filepath.Clean), not the raw
// input. This is intentional: a crafted path like "foo/../../../etc/passwd"
// becomes "../../etc/passwd" after cleaning, which the ".." check then
// catches. Checking the raw input would miss multi-segment traversals that
// cancel each other out before the final join.
func resolveGitVocabPath(repoDir, relPath string) (string, error) {
	cleaned := filepath.Clean(relPath)
	if strings.Contains(cleaned, "..") {
		return "", fmt.Errorf("git vocab path %q contains '..': path traversal not permitted", relPath)
	}
	return filepath.Join(repoDir, cleaned), nil
}

func (g *GitStore) repoDir() string {
	// derive stable dir name from URL
	name := filepath.Base(strings.TrimSuffix(g.url, ".git"))
	return filepath.Join(g.cacheDir, name)
}

func (g *GitStore) registryDir() string {
	if g.subpath != "" {
		return filepath.Join(g.repoDir(), g.subpath)
	}
	return g.repoDir()
}

func (g *GitStore) runGit(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if g.keyFile != "" && strings.HasPrefix(g.url, "git@") {
		// Use a whitelisted env rather than inheriting os.Environ() to prevent
		// GIT_SSH_COMMAND or GIT_ASKPASS overrides from the parent process environment.
		// StrictHostKeyChecking=accept-new accepts new hosts on first connect but
		// rejects unexpected key changes, preventing silent MITM.
		cmd.Env = []string{
			"PATH=" + os.Getenv("PATH"),
			"HOME=" + os.Getenv("HOME"),
			"GIT_SSH_COMMAND=ssh -i " + g.keyFile + " -o StrictHostKeyChecking=accept-new -o BatchMode=yes",
		}
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s: %w\n%s", args[0], err, out)
	}
	return nil
}

func (g *GitStore) pollLoop() {
	ticker := time.NewTicker(g.poll)
	defer ticker.Stop()
	for {
		select {
		case <-g.done:
			return
		case <-ticker.C:
			if err := g.fetch(); err != nil {
				slog.Error("git fetch failed", "err", err)
				continue
			}
			if err := g.Reload(); err != nil {
				slog.Error("git store reload failed", "err", err)
			}
		}
	}
}

func (g *GitStore) fetch() error {
	repoDir := g.repoDir()
	// --no-hooks prevents execution of post-fetch/post-checkout hooks, which
	// could be planted by an attacker with write access to the cache directory.
	// --depth=1 is a security invariant: shallow clones prevent an attacker from
	// crafting malformed git objects in history that could exploit git parsing bugs.
	// Do not remove --depth=1 without a security review.
	if err := g.runGit(repoDir, "fetch", "--no-hooks", "--depth=1", "origin", g.ref); err != nil {
		return err
	}
	if err := g.runGit(repoDir, "reset", "--no-hooks", "--hard", "origin/"+g.ref); err != nil {
		return err
	}
	// Validate that the remote URL in .git/config still matches the configured
	// URL. An attacker with cache-dir write access could redirect the remote to
	// an attacker-controlled repo; catching that here prevents registry poisoning.
	return g.validateRemoteURL(repoDir)
}

// validateRemoteURL reads remote.origin.url from .git/config and compares it
// against the configured URL. Returns an error if they differ.
func (g *GitStore) validateRemoteURL(repoDir string) error {
	cmd := exec.Command("git", "config", "--get", "remote.origin.url")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("reading remote.origin.url: %w", err)
	}
	got := strings.TrimSpace(string(out))
	if got != g.url {
		return fmt.Errorf("remote.origin.url mismatch: got %q, want %q — possible cache tampering", got, g.url)
	}
	return nil
}

// Close stops the poll loop.
func (g *GitStore) Close() error {
	close(g.done)
	return nil
}

// Reload re-reads the vocabulary file and registry files from the cloned repo.
// If vocabulary conflicts are found, the old snapshot is retained and all
// conflicts are logged.
func (g *GitStore) Reload() error {
	g.reloadMu.Lock()
	defer g.reloadMu.Unlock()

	var (
		vocab *vocabulary
		err   error
	)
	if g.vocabPath != "" {
		absPath, pathErr := resolveGitVocabPath(g.repoDir(), g.vocabPath)
		if pathErr != nil {
			return pathErr
		}
		vocab, err = loadVocabulary(absPath)
		if err != nil {
			slog.Error("git reload: failed to load vocabulary; keeping old snapshot",
				"vocab_path", g.vocabPath, "err", err)
			return err
		}
	}
	return g.reload(vocab)
}

// reload builds a new snapshot and swaps it in atomically.
// On vocabulary conflicts the old snapshot is retained.
func (g *GitStore) reload(vocab *vocabulary) error {
	entries, err := loadDir(g.registryDir(), vocab)
	if err != nil {
		var ve RegistryValidationErrors
		if asVE(err, &ve) {
			slog.Error("git reload: vocabulary conflicts; keeping old snapshot",
				"vocab_path", g.vocabPath,
				"conflict_count", len(ve),
				"conflicts", ve.Error())
			return err
		}
		return err
	}
	g.mu.Lock()
	g.snap = snapshot{agents: entries, vocab: vocab}
	g.mu.Unlock()
	return nil
}

func (g *GitStore) ListAgents() []Agent {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]Agent, 0, len(g.snap.agents))
	for _, a := range g.snap.agents {
		out = append(out, a)
	}
	return out
}

func (g *GitStore) GetAgent(name string) (Agent, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	a, ok := g.snap.agents[name]
	return a, ok
}

func (g *GitStore) FindByCapability(intents ...string) []Agent {
	g.mu.RLock()
	defer g.mu.RUnlock()
	intentSet := make(map[string]bool, len(intents))
	for _, i := range intents {
		intentSet[i] = true
	}
	var out []Agent
	for _, a := range g.snap.agents {
		for _, cap := range a.Capabilities {
			matched := false
			for _, t := range cap.Triggers.Intents {
				if intentSet[t] {
					matched = true
					break
				}
			}
			if matched {
				out = append(out, a)
				break
			}
		}
	}
	return out
}

func (g *GitStore) FindByConversationKind(kind string) []Agent {
	g.mu.RLock()
	defer g.mu.RUnlock()
	var out []Agent
	for _, a := range g.snap.agents {
		for _, cap := range a.Capabilities {
			matched := false
			for _, k := range cap.Triggers.ConversationKinds {
				if k == kind {
					matched = true
					break
				}
			}
			if matched {
				out = append(out, a)
				break
			}
		}
	}
	return out
}

func (g *GitStore) FindBySequencing(afterAgent string) []Agent {
	g.mu.RLock()
	defer g.mu.RUnlock()
	var out []Agent
	for _, a := range g.snap.agents {
		for _, cap := range a.Capabilities {
			matched := false
			for _, ag := range cap.Triggers.AfterAgents {
				if ag == afterAgent {
					matched = true
					break
				}
			}
			if matched {
				out = append(out, a)
				break
			}
		}
	}
	return out
}
