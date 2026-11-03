package store

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// gitRecorder puts a wrapper first on PATH that appends each git argv line to a
// log and then runs the host git. Creating the file "failclone" in dir makes
// `git clone` fail; creating "hang" makes every git call block.
type gitRecorder struct{ dir string }

func installGitRecorder(t *testing.T) gitRecorder {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"echo \"$@\" >> " + filepath.Join(dir, "log") + "\n" +
		"[ -e " + filepath.Join(dir, "hang") + " ] && { : > " + filepath.Join(dir, "started") + "; exec sleep 60; }\n" +
		"for a in \"$@\"; do\n" +
		"  if [ \"$a\" = clone ] && [ -e " + filepath.Join(dir, "failclone") + " ]; then echo boom >&2; exit 1; fi\n" +
		"done\n" +
		"exec " + real + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return gitRecorder{dir: dir}
}

func (r gitRecorder) set(t *testing.T, flag string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(r.dir, flag), nil, 0600); err != nil {
		t.Fatal(err)
	}
}

// count returns how many logged git calls used the subcommand.
func (r gitRecorder) count(t *testing.T, sub string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(r.dir, "log"))
	if os.IsNotExist(err) {
		return 0
	} else if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, line := range strings.Split(string(data), "\n") {
		for _, f := range strings.Fields(line) {
			if f == sub {
				n++
				break
			}
		}
	}
	return n
}

func (r gitRecorder) reset(t *testing.T) {
	t.Helper()
	_ = os.Remove(filepath.Join(r.dir, "log"))
}

func newGitStoreCfg(t *testing.T, bare string) (GitStoreConfig, string) {
	t.Helper()
	cache := t.TempDir()
	return GitStoreConfig{URL: "file://" + bare, Ref: "main", CacheDir: cache, Poll: time.Hour}, cache
}

func cacheEntries(t *testing.T, cache string) []string {
	t.Helper()
	ents, err := os.ReadDir(cache)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	return names
}

func TestGitStoreSyncAdvancesToOriginHead(t *testing.T) {
	bare, work := newOrigin(t)
	gs := newTestGitStore(t, bare, time.Hour)
	if _, ok := gs.GetAgent("alpha"); !ok {
		t.Fatal("alpha not loaded after clone")
	}
	oldDir := gs.curDir

	advanceOrigin(t, work, "beta")
	if !gs.syncOnce("test") {
		t.Fatal("sync failed with host git")
	}
	want := runHostGit(t, work, "rev-parse", "HEAD")
	got := runHostGit(t, gs.curDir, "rev-parse", "HEAD")
	if got != want {
		t.Fatalf("served clone HEAD %q != origin %q", got, want)
	}
	if _, ok := gs.GetAgent("beta"); !ok {
		t.Fatal("beta not served after sync")
	}
	if gs.curDir == oldDir {
		t.Fatal("sync reused the previous directory")
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("previous clone directory not removed: %v", err)
	}
	if n := len(cacheEntries(t, gs.cacheDir)); n != 1 {
		t.Fatalf("cache dir holds %d entries, want only the served clone: %v", n, cacheEntries(t, gs.cacheDir))
	}
}

func TestGitStoreUnchangedOriginMakesNoClone(t *testing.T) {
	bare, _ := newOrigin(t)
	cfg, cache := newGitStoreCfg(t, bare)
	rec := installGitRecorder(t)
	gs, err := NewGitStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = gs.Close() })
	before := cacheEntries(t, cache)
	rec.reset(t)

	for i := 0; i < 3; i++ {
		if !gs.syncOnce("test") {
			t.Fatal("sync failed")
		}
	}
	if n := rec.count(t, "clone"); n != 0 {
		t.Fatalf("unchanged origin triggered %d clones", n)
	}
	if n := rec.count(t, "ls-remote"); n != 3 {
		t.Fatalf("ls-remote calls = %d, want 3", n)
	}
	if after := cacheEntries(t, cache); strings.Join(after, ",") != strings.Join(before, ",") {
		t.Fatalf("cache dir changed with no new commit: %v -> %v", before, after)
	}
}

// plantAttack writes every known config-execution vector into a git directory,
// as an attacker with cache-dir write access would, including a commondir that
// redirects config reads. It returns the marker files the commands would create.
func plantAttack(t *testing.T, repo, bare string) []string {
	t.Helper()
	markers := t.TempDir()
	var paths []string
	script := func(name string) string {
		s := filepath.Join(markers, name+".sh")
		m := filepath.Join(markers, name+"-ran")
		if err := os.WriteFile(s, []byte("#!/bin/sh\ntouch "+m+"\ncat\n"), 0700); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, m)
		return s
	}
	cfg := "[url \"ext::sh -c '" + script("ext") + "'\"]\n\tinsteadOf = file://" + bare + "\n" +
		"[protocol]\n\tallow = always\n" +
		"[core]\n\tfsmonitor = " + script("fsm") + "\n\tgitProxy = " + script("proxy") + "\n\tsshCommand = " + script("ssh") + "\n" +
		"\thooksPath = " + filepath.Join(repo, ".git", "hooks") + "\n" +
		"[filter \"x\"]\n\tsmudge = " + script("smudge") + "\n\tprocess = " + script("process") + "\n" +
		"[credential]\n\thelper = !" + script("cred") + "\n" +
		"[remote \"origin\"]\n\tuploadpack = " + script("up") + "\n"

	gitDir := filepath.Join(repo, ".git")
	common := t.TempDir()
	for _, d := range []string{gitDir, common} {
		if err := os.MkdirAll(filepath.Join(d, "info"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "info", "attributes"), []byte("*.yaml filter=x\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(common, "config"), []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(gitDir, "config"), os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(cfg); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	if err := os.WriteFile(filepath.Join(gitDir, "commondir"), []byte(common+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	hooks := filepath.Join(gitDir, "hooks")
	if err := os.MkdirAll(hooks, 0700); err != nil {
		t.Fatal(err)
	}
	hookMarker := filepath.Join(markers, "hook-ran")
	paths = append(paths, hookMarker)
	for _, h := range []string{"reference-transaction", "post-checkout", "post-merge", "pre-auto-gc"} {
		if err := os.WriteFile(filepath.Join(hooks, h), []byte("#!/bin/sh\ntouch "+hookMarker+"\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	return paths
}

func ranMarkers(markers []string) []string {
	var ran []string
	for _, m := range markers {
		if _, err := os.Stat(m); err == nil {
			ran = append(ran, m)
		}
	}
	return ran
}

func assertNoMarkers(t *testing.T, markers []string) {
	t.Helper()
	if ran := ranMarkers(markers); len(ran) > 0 {
		t.Fatalf("planted command ran: %s", filepath.Base(ran[0]))
	}
}

// TestPlantedExtVectorTripsMarkerAssertion proves the ext:: marker is wired to
// the command the vector runs: invoking host git directly inside a planted
// directory executes it, and assertNoMarkers must then fail.
func TestPlantedExtVectorTripsMarkerAssertion(t *testing.T) {
	bare, _ := newOrigin(t)
	repo := t.TempDir()
	runHostGit(t, repo, "init", "--quiet")
	markers := plantAttack(t, repo, bare)
	if err := os.Remove(filepath.Join(repo, ".git", "commondir")); err != nil {
		t.Fatal(err)
	}

	// Exit status is irrelevant: the planted command only has to start.
	// The marker is touched as the command starts; the ext:: helper then stalls
	// on its pipe, so cut it off quickly.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, "git", "-C", repo, "ls-remote", "file://"+bare).Run()

	var ranExt bool
	for _, m := range ranMarkers(markers) {
		if filepath.Base(m) == "ext-ran" {
			ranExt = true
		}
	}
	if !ranExt {
		t.Fatalf("forced ext:: vector did not trip the marker check (ran: %v)", ranMarkers(markers))
	}
}

func TestGitStoreIgnoresPlantedServedDirectory(t *testing.T) {
	bare, work := newOrigin(t)
	gs := newTestGitStore(t, bare, time.Hour)
	markers := plantAttack(t, gs.curDir, bare)

	advanceOrigin(t, work, "beta")
	if !gs.syncOnce("test") {
		t.Fatal("sync failed with planted directory present")
	}
	assertNoMarkers(t, markers)
	want := runHostGit(t, work, "rev-parse", "HEAD")
	if got := runHostGit(t, gs.curDir, "rev-parse", "HEAD"); got != want {
		t.Fatalf("clone did not advance: HEAD %q != origin %q", got, want)
	}
}

func TestGitStoreStartupIgnoresPlantedDirectories(t *testing.T) {
	bare, _ := newOrigin(t)
	cfg, cache := newGitStoreCfg(t, bare)

	// A leftover per-sync directory and the legacy persistent clone named
	// after the URL, both with planted config.
	stale := filepath.Join(cache, "clone-leftover")
	runHostGit(t, cache, "clone", "--quiet", cfg.URL, stale)
	legacy := filepath.Join(cache, "origin")
	runHostGit(t, cache, "clone", "--quiet", cfg.URL, legacy)
	markers := append(plantAttack(t, stale, bare), plantAttack(t, legacy, bare)...)

	gs, err := NewGitStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = gs.Close() })
	assertNoMarkers(t, markers)
	if _, ok := gs.GetAgent("alpha"); !ok {
		t.Fatal("registry not loaded at startup")
	}
	for _, p := range []string{stale, legacy} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("stale directory %s not removed", filepath.Base(p))
		}
	}
	if gs.curDir == stale || gs.curDir == legacy {
		t.Fatal("startup served a pre-existing directory")
	}
}

func TestGitStoreStartupSyncAdvancesStaleCache(t *testing.T) {
	bare, work := newOrigin(t)
	cfg, _ := newGitStoreCfg(t, bare)
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
		t.Fatal("restart served a stale registry")
	}
	if st := second.SyncStatus(); st.LastSuccess.IsZero() || st.ConsecutiveFailures != 0 {
		t.Fatalf("unexpected sync status after startup sync: %+v", st)
	}
}

func TestGitStoreStartupFailsWithoutOrigin(t *testing.T) {
	bare, _ := newOrigin(t)
	cfg, _ := newGitStoreCfg(t, bare)
	if err := os.Rename(bare, bare+".moved"); err != nil {
		t.Fatal(err)
	}
	if gs, err := NewGitStore(cfg); err == nil {
		_ = gs.Close()
		t.Fatal("NewGitStore succeeded with no origin")
	}
}

func TestGitStoreFailedCloneKeepsOldSnapshot(t *testing.T) {
	bare, work := newOrigin(t)
	rec := installGitRecorder(t)
	gs := newTestGitStore(t, bare, time.Hour)
	servedDir := gs.curDir

	advanceOrigin(t, work, "beta")
	rec.set(t, "failclone")
	for i := 1; i <= 2; i++ {
		if gs.syncOnce("test") {
			t.Fatal("sync succeeded though clone fails")
		}
		if got := gs.SyncStatus().ConsecutiveFailures; got != i {
			t.Fatalf("failures = %d, want %d", got, i)
		}
	}
	if _, ok := gs.GetAgent("alpha"); !ok {
		t.Fatal("old snapshot lost after failed clone")
	}
	if _, ok := gs.GetAgent("beta"); ok {
		t.Fatal("new snapshot served despite failed clone")
	}
	if gs.curDir != servedDir {
		t.Fatal("served directory changed on failure")
	}
	if got := cacheEntries(t, gs.cacheDir); len(got) != 1 {
		t.Fatalf("failed clone left directories behind: %v", got)
	}

	if err := os.Remove(filepath.Join(rec.dir, "failclone")); err != nil {
		t.Fatal(err)
	}
	if !gs.syncOnce("test") {
		t.Fatal("sync did not recover")
	}
	if st := gs.SyncStatus(); st.ConsecutiveFailures != 0 || st.LastError != "" {
		t.Fatalf("status not reset: %+v", st)
	}
	if _, ok := gs.GetAgent("beta"); !ok {
		t.Fatal("beta not served after recovery")
	}
}

func TestGitStoreInvalidRegistryKeepsOldSnapshot(t *testing.T) {
	bare, work := newOrigin(t)
	gs := newTestGitStore(t, bare, time.Hour)
	servedDir := gs.curDir

	if err := os.WriteFile(filepath.Join(work, "bad.yaml"), []byte("schema_version: 2\nidentity: [\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runHostGit(t, work, "add", "bad.yaml")
	runHostGit(t, work, "commit", "-m", "bad")
	runHostGit(t, work, "push", "origin", "main")

	if gs.syncOnce("test") {
		t.Fatal("sync accepted an invalid registry")
	}
	if gs.curDir != servedDir {
		t.Fatal("served directory changed on invalid registry")
	}
	if got := cacheEntries(t, gs.cacheDir); len(got) != 1 {
		t.Fatalf("invalid clone left directories behind: %v", got)
	}
}

func TestGitStoreVocabPathFollowsNewDirectory(t *testing.T) {
	bare, work := newOrigin(t)
	// Agents move under a subpath so the vocabulary file is not read as an agent.
	if err := os.MkdirAll(filepath.Join(work, "agents"), 0700); err != nil {
		t.Fatal(err)
	}
	runHostGit(t, work, "mv", "alpha.yaml", "agents/alpha.yaml")
	vocab := "schema_version: 1\nintents:\n  do-thing: x\nformats:\n  text: x\n"
	if err := os.WriteFile(filepath.Join(work, "vocabulary.v1.yaml"), []byte(vocab), 0600); err != nil {
		t.Fatal(err)
	}
	runHostGit(t, work, "add", "vocabulary.v1.yaml")
	runHostGit(t, work, "commit", "-m", "layout")
	runHostGit(t, work, "push", "origin", "main")

	cfg, _ := newGitStoreCfg(t, bare)
	cfg.Subpath = "agents"
	cfg.VocabularyPath = "vocabulary.v1.yaml"
	gs, err := NewGitStore(cfg)
	if err != nil {
		t.Fatalf("NewGitStore with vocabulary: %v", err)
	}
	t.Cleanup(func() { _ = gs.Close() })
	first := gs.curDir

	// The new commit's vocabulary drops the intent: the sync must read the new
	// directory's vocabulary and reject the registry, keeping the old snapshot.
	bad := "schema_version: 1\nintents:\n  other: x\nformats:\n  text: x\n"
	if err := os.WriteFile(filepath.Join(work, "vocabulary.v1.yaml"), []byte(bad), 0600); err != nil {
		t.Fatal(err)
	}
	runHostGit(t, work, "commit", "-am", "narrow vocab")
	runHostGit(t, work, "push", "origin", "main")
	if gs.syncOnce("test") {
		t.Fatal("sync accepted a registry that violates the new directory's vocabulary")
	}
	if gs.curDir != first {
		t.Fatal("served directory changed on vocabulary conflict")
	}

	if err := os.WriteFile(filepath.Join(work, "vocabulary.v1.yaml"), []byte(vocab), 0600); err != nil {
		t.Fatal(err)
	}
	runHostGit(t, work, "commit", "-am", "restore vocab")
	runHostGit(t, work, "push", "origin", "main")
	if !gs.syncOnce("test") {
		t.Fatalf("sync with vocabulary failed: %+v", gs.SyncStatus())
	}
	if gs.curDir == first {
		t.Fatal("vocabulary sync did not move to a new directory")
	}
}

func TestGitURLSchemeAllowlist(t *testing.T) {
	cases := map[string]string{
		"https://example.invalid/r.git": "https",
		"ssh://git@example.invalid/r":   "ssh",
		"git@example.invalid:org/r.git": "ssh",
		"file:///srv/r.git":             "file",
		"/srv/r.git":                    "file",
	}
	for url, want := range cases {
		got, err := gitURLScheme(url)
		if err != nil || got != want {
			t.Errorf("gitURLScheme(%q) = %q, %v; want %q", url, got, err, want)
		}
	}
	for _, url := range []string{"ext::sh -c id", "git://example.invalid/r", "ftp://example.invalid/r", "--upload-pack=x@y:z"} {
		if _, err := gitURLScheme(url); err == nil {
			t.Errorf("gitURLScheme(%q) accepted", url)
		}
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

func TestGitStoreFailedSyncRecordedThenRecovers(t *testing.T) {
	bare, work := newOrigin(t)
	gs := newTestGitStore(t, bare, time.Hour)

	moved := bare + ".moved"
	if err := os.Rename(bare, moved); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		if gs.syncOnce("test") {
			t.Fatal("sync unexpectedly succeeded against missing origin")
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
		t.Fatal("sync did not recover")
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

func TestGitStoreHangingGitTimesOutAndCountsFailure(t *testing.T) {
	bare, _ := newOrigin(t)
	rec := installGitRecorder(t)
	gs, err := NewGitStore(GitStoreConfig{
		URL: "file://" + bare, Ref: "main", CacheDir: t.TempDir(),
		Poll: time.Hour, GitTimeout: 300 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = gs.Close() })
	rec.set(t, "hang")

	start := time.Now()
	if gs.syncOnce("test") {
		t.Fatal("hanging git reported success")
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("sync not bounded: took %s", d)
	}
	st := gs.SyncStatus()
	if st.ConsecutiveFailures != 1 || !strings.Contains(st.LastError, "timed out") {
		t.Fatalf("timeout not recorded as failed sync: %+v", st)
	}
}

func TestGitStoreCloseCancelsInFlightGit(t *testing.T) {
	bare, _ := newOrigin(t)
	rec := installGitRecorder(t)
	gs, err := NewGitStore(GitStoreConfig{
		URL: "file://" + bare, Ref: "main", CacheDir: t.TempDir(),
		Poll: 20 * time.Millisecond, GitTimeout: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	rec.set(t, "hang")
	started := filepath.Join(rec.dir, "started")
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(started); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("poll loop never entered the hung git call")
		}
		time.Sleep(5 * time.Millisecond)
	}

	closed := make(chan struct{})
	go func() { _ = gs.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("Close blocked on a hung git call")
	}
}
