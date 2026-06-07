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
type GitStore struct {
	url      string
	ref      string
	cacheDir string
	subpath  string
	keyFile  string
	poll     time.Duration

	mu     sync.RWMutex
	agents map[string]Agent
	done   chan struct{}
}

// GitStoreConfig holds configuration for a GitStore.
type GitStoreConfig struct {
	URL      string
	Ref      string        // default: main
	CacheDir string        // where to clone
	Subpath  string        // subdirectory within repo for registry files
	KeyFile  string        // SSH deploy key or HTTPS token file (optional)
	Poll     time.Duration // default: 60s
	// Ext holds optional vocabulary extensions to merge into the base enums
	// before the first registry load.
	Ext VocabularyExtensions
}

// NewGitStore clones the repo and starts the poll loop.
func NewGitStore(cfg GitStoreConfig) (*GitStore, error) {
	applyExtensions(cfg.Ext)
	if cfg.Ref == "" {
		cfg.Ref = "main"
	}
	if cfg.Poll == 0 {
		cfg.Poll = 60 * time.Second
	}
	gs := &GitStore{
		url:      cfg.URL,
		ref:      cfg.Ref,
		cacheDir: cfg.CacheDir,
		subpath:  cfg.Subpath,
		keyFile:  cfg.KeyFile,
		poll:     cfg.Poll,
		agents:   make(map[string]Agent),
		done:     make(chan struct{}),
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

	if err := gs.Reload(); err != nil {
		return nil, err
	}

	go gs.pollLoop()
	return gs, nil
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

// Reload re-reads the registry files from the cloned repo.
func (g *GitStore) Reload() error {
	entries, err := loadDir(g.registryDir())
	if err != nil {
		return err
	}
	g.mu.Lock()
	g.agents = entries
	g.mu.Unlock()
	return nil
}

func (g *GitStore) ListAgents() []Agent {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]Agent, 0, len(g.agents))
	for _, a := range g.agents {
		out = append(out, a)
	}
	return out
}

func (g *GitStore) GetAgent(name string) (Agent, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	a, ok := g.agents[name]
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
	for _, a := range g.agents {
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
	for _, a := range g.agents {
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
	for _, a := range g.agents {
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
