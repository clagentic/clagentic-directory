package store

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const testVocab = "schema_version: 1\nintents:\n  do-thing: x\nformats:\n  text: x\n"

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func captureLog(t *testing.T) *lockedBuf {
	t.Helper()
	buf := &lockedBuf{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

// vocabOrigin returns an origin whose agents live under agents/ next to a
// vocabulary file at vocabulary.v1.yaml.
func vocabOrigin(t *testing.T) (bare, work string) {
	t.Helper()
	bare, work = newOrigin(t)
	if err := os.MkdirAll(filepath.Join(work, "agents"), 0700); err != nil {
		t.Fatal(err)
	}
	runHostGit(t, work, "mv", "alpha.yaml", "agents/alpha.yaml")
	if err := os.WriteFile(filepath.Join(work, "vocabulary.v1.yaml"), []byte(testVocab), 0600); err != nil {
		t.Fatal(err)
	}
	runHostGit(t, work, "add", "vocabulary.v1.yaml")
	runHostGit(t, work, "commit", "-m", "layout")
	runHostGit(t, work, "push", "origin", "main")
	return bare, work
}

func vocabCfg(t *testing.T, bare string) GitStoreConfig {
	cfg, _ := newGitStoreCfg(t, bare)
	cfg.Subpath = "agents"
	cfg.VocabularyPath = "vocabulary.v1.yaml"
	return cfg
}

func TestGitStoreLogsLoadedVocabularyAndStrictMode(t *testing.T) {
	bare, work := vocabOrigin(t)
	buf := captureLog(t)
	gs, err := NewGitStore(vocabCfg(t, bare))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = gs.Close() })

	sha := strings.TrimSpace(runHostGit(t, work, "rev-parse", "HEAD"))
	out := buf.String()
	for _, want := range []string{"vocabulary loaded", "vocab_path=vocabulary.v1.yaml", "validation_mode=ValidateStrict", "sha=" + sha} {
		if !strings.Contains(out, want) {
			t.Errorf("startup log missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "UNSAFE") {
		t.Errorf("startup log mentions ValidateOpen with a vocabulary configured:\n%s", out)
	}
}

func TestGitStoreWithoutVocabularyWarnsValidateOpen(t *testing.T) {
	bare, _ := newOrigin(t)
	buf := captureLog(t)
	gs := newTestGitStore(t, bare, time.Hour)
	_ = gs
	out := buf.String()
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "validation_mode=ValidateOpen") {
		t.Errorf("no ValidateOpen warning without a vocabulary:\n%s", out)
	}
}

func TestGitStoreRejectsOutOfVocabularyCard(t *testing.T) {
	bare, work := vocabOrigin(t)
	gs, err := NewGitStore(vocabCfg(t, bare))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = gs.Close() })
	servedDir, servedSHA := gs.curDir, gs.curSHA

	rogue := "schema_version: 2\nidentity:\n  name: rogue\n  version: 1.0.0\n  description: out of vocabulary\n" +
		"capabilities:\n  - id: x\n    name: X\n    description: x\n" +
		"    triggers:\n      intents:\n        - not-in-vocabulary\n    returns:\n      verdict_field: result\n      format: text\n"
	if err := os.WriteFile(filepath.Join(work, "agents", "rogue.yaml"), []byte(rogue), 0600); err != nil {
		t.Fatal(err)
	}
	runHostGit(t, work, "add", "agents/rogue.yaml")
	runHostGit(t, work, "commit", "-m", "rogue")
	runHostGit(t, work, "push", "origin", "main")

	if gs.syncOnce("test") {
		t.Fatal("sync accepted a card whose intent is outside the vocabulary")
	}
	if st := gs.SyncStatus(); st.ConsecutiveFailures != 1 {
		t.Fatalf("consecutive_failures = %d, want 1", st.ConsecutiveFailures)
	}
	if gs.curDir != servedDir || gs.curSHA != servedSHA {
		t.Fatal("served snapshot changed after a rejected sync")
	}
	if _, ok := gs.GetAgent("rogue"); ok {
		t.Fatal("rogue card is served")
	}
	if _, ok := gs.GetAgent("alpha"); !ok {
		t.Fatal("old snapshot lost")
	}
}

func TestGitStoreMissingVocabularyFileDoesNotFallOpen(t *testing.T) {
	bare, work := vocabOrigin(t)

	cfg := vocabCfg(t, bare)
	cfg.VocabularyPath = "no-such-vocabulary.yaml"
	if gs, err := NewGitStore(cfg); err == nil {
		_ = gs.Close()
		t.Fatal("startup succeeded with a missing vocabulary file")
	}

	gs, err := NewGitStore(vocabCfg(t, bare))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = gs.Close() })
	servedDir := gs.curDir

	runHostGit(t, work, "rm", "vocabulary.v1.yaml")
	runHostGit(t, work, "commit", "-m", "drop vocab")
	runHostGit(t, work, "push", "origin", "main")
	if gs.syncOnce("test") {
		t.Fatal("sync fell open after the vocabulary file disappeared")
	}
	if st := gs.SyncStatus(); st.ConsecutiveFailures != 1 {
		t.Fatalf("consecutive_failures = %d, want 1", st.ConsecutiveFailures)
	}
	if gs.curDir != servedDir {
		t.Fatal("served snapshot changed on a missing vocabulary")
	}
}
