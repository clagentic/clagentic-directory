package store

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func runHostGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func writeAgent(t *testing.T, work, name string) {
	t.Helper()
	body := []byte("schema_version: 2\nidentity:\n  name: " + name + "\n  version: 1.0.0\n  description: sync test agent\n" +
		"capabilities:\n  - id: do-thing\n    name: Do thing\n    description: does a thing\n" +
		"    triggers:\n      intents:\n        - do-thing\n    returns:\n      verdict_field: result\n      format: text\n")
	if err := os.WriteFile(filepath.Join(work, name+".yaml"), body, 0600); err != nil {
		t.Fatal(err)
	}
}

// newOrigin builds a bare origin with one agent on main and a work tree to advance it.
func newOrigin(t *testing.T) (bare, work string) {
	t.Helper()
	root := t.TempDir()
	bare = filepath.Join(root, "origin.git")
	work = filepath.Join(root, "work")
	runHostGit(t, root, "init", "--bare", "-b", "main", bare)
	runHostGit(t, root, "init", "-b", "main", work)
	runHostGit(t, work, "remote", "add", "origin", bare)
	writeAgent(t, work, "alpha")
	runHostGit(t, work, "add", "alpha.yaml")
	runHostGit(t, work, "commit", "-m", "alpha")
	runHostGit(t, work, "push", "origin", "main")
	return bare, work
}

func advanceOrigin(t *testing.T, work, name string) {
	t.Helper()
	writeAgent(t, work, name)
	runHostGit(t, work, "add", name+".yaml")
	runHostGit(t, work, "commit", "-m", name)
	runHostGit(t, work, "push", "origin", "main")
}

func newTestGitStore(t *testing.T, bare string, poll time.Duration) *GitStore {
	t.Helper()
	// file:// is required: --depth is ignored for plain local paths.
	gs, err := NewGitStore(GitStoreConfig{
		URL: "file://" + bare, Ref: "main", CacheDir: t.TempDir(), Poll: poll,
	})
	if err != nil {
		t.Fatalf("NewGitStore: %v", err)
	}
	t.Cleanup(func() { _ = gs.Close() })
	return gs
}

func TestGitStoreFetchAdvancesClone(t *testing.T) {
	bare, work := newOrigin(t)
	gs := newTestGitStore(t, bare, time.Hour)
	if _, ok := gs.GetAgent("alpha"); !ok {
		t.Fatal("alpha not loaded after clone")
	}

	advanceOrigin(t, work, "beta")
	if err := gs.fetch(); err != nil {
		t.Fatalf("fetch with host git: %v", err)
	}
	want := runHostGit(t, work, "rev-parse", "HEAD")
	got := runHostGit(t, gs.repoDir(), "rev-parse", "HEAD")
	if got != want {
		t.Fatalf("clone HEAD %q != origin %q", got, want)
	}
	if err := gs.Reload(); err != nil {
		t.Fatal(err)
	}
	if _, ok := gs.GetAgent("beta"); !ok {
		t.Fatal("beta not served after fetch+reload")
	}
}

func TestGitStoreFetchSuppressesHooks(t *testing.T) {
	bare, work := newOrigin(t)
	gs := newTestGitStore(t, bare, time.Hour)
	marker := filepath.Join(t.TempDir(), "hook-ran")
	hook := "#!/bin/sh\ntouch " + marker + "\n"
	hooksDir := filepath.Join(gs.repoDir(), ".git", "hooks")
	for _, name := range []string{"reference-transaction", "post-checkout", "post-merge"} {
		if err := os.WriteFile(filepath.Join(hooksDir, name), []byte(hook), 0700); err != nil {
			t.Fatal(err)
		}
	}
	advanceOrigin(t, work, "beta")
	if err := gs.fetch(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a repo hook ran during fetch/reset")
	}
}

func TestGitStoreFetchNeutralizesPlantedConfigCommands(t *testing.T) {
	bare, work := newOrigin(t)
	gs := newTestGitStore(t, bare, time.Hour)
	markers := t.TempDir()
	fsm := filepath.Join(markers, "fsmonitor-ran")
	up := filepath.Join(markers, "uploadpack-ran")
	fsmScript := filepath.Join(markers, "fsm.sh")
	upScript := filepath.Join(markers, "up.sh")
	if err := os.WriteFile(fsmScript, []byte("#!/bin/sh\ntouch "+fsm+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(upScript, []byte("#!/bin/sh\ntouch "+up+"\nexec git-upload-pack \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	runHostGit(t, gs.repoDir(), "config", "core.fsmonitor", fsmScript)
	runHostGit(t, gs.repoDir(), "config", "remote.origin.uploadpack", upScript)

	advanceOrigin(t, work, "beta")
	if err := gs.fetch(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(fsm); err == nil {
		t.Fatal("planted core.fsmonitor command ran during fetch/reset")
	}
	if _, err := os.Stat(up); err == nil {
		t.Fatal("planted remote.origin.uploadpack command ran during fetch")
	}
}

func TestGitStoreStartupSyncAdvancesStaleCache(t *testing.T) {
	bare, work := newOrigin(t)
	cache := t.TempDir()
	cfg := GitStoreConfig{URL: "file://" + bare, Ref: "main", CacheDir: cache, Poll: time.Hour}
	first, err := NewGitStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	_ = first.Close()

	advanceOrigin(t, work, "beta")
	second, err := NewGitStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	if _, ok := second.GetAgent("beta"); !ok {
		t.Fatal("restart served stale cache: startup sync did not run")
	}
	if st := second.SyncStatus(); st.LastSuccess.IsZero() || st.ConsecutiveFailures != 0 {
		t.Fatalf("unexpected sync status after startup sync: %+v", st)
	}
}

func TestGitStorePollLoopAdvances(t *testing.T) {
	bare, work := newOrigin(t)
	gs := newTestGitStore(t, bare, 50*time.Millisecond)
	advanceOrigin(t, work, "beta")
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := gs.GetAgent("beta"); ok {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("poll loop did not advance the registry")
}

func TestGitStoreFailedFetchRecordedThenRecovers(t *testing.T) {
	bare, work := newOrigin(t)
	gs := newTestGitStore(t, bare, time.Hour)

	moved := bare + ".moved"
	if err := os.Rename(bare, moved); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		if gs.syncOnce("test") {
			t.Fatal("fetch unexpectedly succeeded against missing origin")
		}
		if got := gs.SyncStatus().ConsecutiveFailures; got != i {
			t.Fatalf("failures = %d, want %d", got, i)
		}
	}
	if gs.SyncStatus().LastError == "" {
		t.Fatal("LastError empty after failures")
	}

	if err := os.Rename(moved, bare); err != nil {
		t.Fatal(err)
	}
	advanceOrigin(t, work, "beta")
	if !gs.syncOnce("test") {
		t.Fatal("fetch did not recover")
	}
	if st := gs.SyncStatus(); st.ConsecutiveFailures != 0 || st.LastError != "" {
		t.Fatalf("status not reset: %+v", st)
	}
}

func TestGitStorePollLoopRecordsFailures(t *testing.T) {
	bare, _ := newOrigin(t)
	gs := newTestGitStore(t, bare, 20*time.Millisecond)
	if err := os.Rename(bare, bare+".moved"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if gs.SyncStatus().ConsecutiveFailures >= 3 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("poll loop failures were not recorded in SyncStatus")
}
