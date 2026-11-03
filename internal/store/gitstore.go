package store

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DefaultGitTimeout bounds each git subprocess (ls-remote, clone, rev-parse)
// when GitStoreConfig.GitTimeout is zero. A hung git call would otherwise never
// record a failure and /readyz would stay 200 on a stale registry.
const DefaultGitTimeout = 2 * time.Minute

// GitStore implements Store by cloning a git repo and polling for changes.
//
// The daemon never runs git inside a directory that existed before it created
// it. Each sync reads the origin SHA with `git ls-remote` (outside any repo);
// only when the SHA changed does it shallow-clone into a fresh directory it
// creates under the cache dir, validate the registry from that directory, swap
// the served snapshot, and delete the previous directory. Anything an attacker
// with cache-dir write access plants in an old directory therefore has nothing
// to execute it.
//
// When a VocabularyPath is configured, the vocabulary file is loaded from the
// same new directory and validated atomically with the agent entries.
type GitStore struct {
	url      string
	ref      string
	cacheDir string
	subpath  string
	keyFile  string
	poll     time.Duration
	// gitTimeout bounds every git subprocess; see DefaultGitTimeout.
	gitTimeout time.Duration
	// ctx is cancelled by Close so an in-flight git call does not block shutdown.
	ctx    context.Context
	cancel context.CancelFunc
	// vocabPath is the path within the cloned repo to the vocabulary file.
	// Empty means ValidateOpen.
	vocabPath string
	ext       VocabularyExtensions

	syncRunMu sync.Mutex   // serializes whole sync runs
	mu        sync.RWMutex // protects snap, curDir, curSHA; held only for swap and reads
	snap      snapshot
	curDir    string // directory the served snapshot was loaded from
	curSHA    string // commit the served snapshot was loaded from

	syncMu sync.Mutex // protects sync
	sync   SyncStatus

	done     chan struct{}
	loopDone chan struct{} // closed when pollLoop has exited
}

// GitStoreConfig holds configuration for a GitStore.
type GitStoreConfig struct {
	URL      string
	Ref      string        // default: main
	CacheDir string        // parent of the per-sync clone directories
	Subpath  string        // subdirectory within repo for registry files
	KeyFile  string        // SSH deploy key or HTTPS token file (optional)
	Poll     time.Duration // default: 60s
	// GitTimeout bounds each git subprocess; default DefaultGitTimeout. A
	// timeout counts as a failed sync.
	GitTimeout time.Duration
	// VocabularyPath is the path within the cloned repo to a vocabulary.v1.yaml
	// file. When set, schema_version: 2 entries are validated against it.
	// When empty, vocabulary checking is skipped (ValidateOpen).
	VocabularyPath string
	// Ext holds optional vocabulary extensions.
	//
	// Deprecated: use VocabularyPath and a vocabulary.v1.yaml file instead.
	Ext VocabularyExtensions
}

// NewGitStore clones the repo into a fresh directory and starts the poll loop.
// Startup never loads an existing directory under the cache dir, so unlike a
// persistent-clone design a failed first sync is fatal: there is no trusted
// stale copy to fall back on.
func NewGitStore(cfg GitStoreConfig) (*GitStore, error) {
	if cfg.Ref == "" {
		cfg.Ref = "main"
	}
	if cfg.Poll == 0 {
		cfg.Poll = 60 * time.Second
	}
	if cfg.GitTimeout == 0 {
		cfg.GitTimeout = DefaultGitTimeout
	}
	cacheDir, err := filepath.Abs(cfg.CacheDir)
	if err != nil {
		return nil, fmt.Errorf("resolving cache dir: %w", err)
	}
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		return nil, fmt.Errorf("creating cache dir: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	gs := &GitStore{
		gitTimeout: cfg.GitTimeout,
		ctx:        ctx,
		cancel:     cancel,
		url:        cfg.URL,
		ref:        cfg.Ref,
		cacheDir:   cacheDir,
		subpath:    cfg.Subpath,
		keyFile:    cfg.KeyFile,
		poll:       cfg.Poll,
		vocabPath:  cfg.VocabularyPath,
		ext:        cfg.Ext,
		done:       make(chan struct{}),
		loopDone:   make(chan struct{}),
	}

	gs.removeStaleDirs()
	if err := gs.syncNow(); err != nil {
		cancel()
		return nil, fmt.Errorf("initial git sync: %w", err)
	}
	gs.recordSync(nil)

	go gs.pollLoop()
	return gs, nil
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

// buildSnapshot loads and validates the vocabulary and registry from dir, a
// directory the daemon just cloned into. It touches nothing served.
func (g *GitStore) buildSnapshot(dir string) (snapshot, error) {
	var vocab *vocabulary
	if g.vocabPath == "" {
		v, _, err := resolveVocabulary("", g.ext)
		if err != nil {
			return snapshot{}, err
		}
		vocab = v
	} else {
		absPath, err := resolveGitVocabPath(dir, g.vocabPath)
		if err != nil {
			return snapshot{}, err
		}
		v, _, err := resolveVocabulary(absPath, g.ext)
		if err != nil {
			slog.Error("git sync: failed to load vocabulary; keeping old snapshot",
				"vocab_path", g.vocabPath, "err", err)
			return snapshot{}, err
		}
		vocab = v
	}

	regDir := dir
	if g.subpath != "" {
		regDir = filepath.Join(dir, g.subpath)
	}
	entries, err := loadDir(regDir, vocab)
	if err != nil {
		var ve RegistryValidationErrors
		if asVE(err, &ve) {
			slog.Error("git sync: vocabulary conflicts; keeping old snapshot",
				"vocab_path", g.vocabPath,
				"conflict_count", len(ve),
				"conflicts", ve.Error())
		}
		return snapshot{}, err
	}
	return snapshot{agents: entries, vocab: vocab}, nil
}

func (g *GitStore) pollLoop() {
	defer close(g.loopDone)
	ticker := time.NewTicker(g.poll)
	defer ticker.Stop()
	for {
		select {
		case <-g.done:
			return
		case <-ticker.C:
			g.syncOnce("poll")
		}
	}
}

// SyncStatus describes the outcome of the git store's sync attempts.
type SyncStatus struct {
	// LastSuccess is the time of the last successful sync; zero if none yet
	// in this process.
	LastSuccess time.Time
	// ConsecutiveFailures counts sync failures since the last success.
	ConsecutiveFailures int
	// LastError is the most recent sync error; empty after a success.
	LastError string
}

// SyncStatus returns a snapshot of the sync outcome counters.
func (g *GitStore) SyncStatus() SyncStatus {
	g.syncMu.Lock()
	defer g.syncMu.Unlock()
	return g.sync
}

// syncOnce runs a sync and records the outcome. It reports whether it succeeded.
func (g *GitStore) syncOnce(phase string) bool {
	err := g.syncNow()
	g.recordSync(err)
	if err != nil {
		slog.Error("git sync failed", "phase", phase, "err", err)
		return false
	}
	return true
}

func (g *GitStore) recordSync(err error) {
	g.syncMu.Lock()
	defer g.syncMu.Unlock()
	if err != nil {
		g.sync.ConsecutiveFailures++
		g.sync.LastError = err.Error()
		return
	}
	g.sync.LastSuccess = time.Now()
	g.sync.ConsecutiveFailures = 0
	g.sync.LastError = ""
}

// syncNow brings the served snapshot up to origin. With an unchanged origin SHA
// it makes no git call beyond ls-remote. On any failure the previous snapshot
// and directory stay in place and the new directory is removed.
func (g *GitStore) syncNow() error {
	g.syncRunMu.Lock()
	defer g.syncRunMu.Unlock()

	sha, err := g.remoteSHA()
	if err != nil {
		return err
	}
	g.mu.RLock()
	served := g.curSHA
	g.mu.RUnlock()
	if sha == served {
		return nil
	}

	dir, err := g.cloneFresh(sha)
	if err != nil {
		return err
	}
	snap, err := g.buildSnapshot(dir)
	if err != nil {
		g.removeDir(dir)
		return err
	}

	g.mu.Lock()
	old := g.curDir
	g.snap = snap
	g.curDir = dir
	g.curSHA = sha
	g.mu.Unlock()
	if old != "" {
		g.removeDir(old)
	}
	return nil
}

// Reload syncs with origin now. It is safe to call from multiple goroutines.
func (g *GitStore) Reload() error {
	err := g.syncNow()
	g.recordSync(err)
	return err
}

// Close stops the poll loop, cancels any in-flight git call, waits for the loop
// to exit, and removes the served clone directory, so shutdown is bounded.
func (g *GitStore) Close() error {
	close(g.done)
	g.cancel()
	<-g.loopDone
	g.mu.Lock()
	dir := g.curDir
	g.curDir = ""
	g.mu.Unlock()
	if dir != "" {
		g.removeDir(dir)
	}
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
	return findByCapability(g.snap.agents, intents...)
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
