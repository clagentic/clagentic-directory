package update

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitRun runs git for test setup with an identity and no user config.
func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-c", "user.name=t", "-c", "user.email=t@example.invalid", "-c", "commit.gpgsign=false", "-C", dir}, args...)
	out, err := runGit("git", full...)
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return out
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// checkoutFixture builds an upstream repo, a clone of it (the source_dir),
// and leaves the clone on a feature branch with a dirty tracked file.
func checkoutFixture(t *testing.T) (upstream, src string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	root := t.TempDir()
	upstream, src = filepath.Join(root, "upstream"), filepath.Join(root, "src")
	if out, err := runGit("git", "init", "--quiet", "--initial-branch=main", upstream); err != nil {
		t.Fatalf("init: %v: %s", err, out)
	}
	writeFile(t, filepath.Join(upstream, "main.go"), "reviewed\n")
	gitRun(t, upstream, "add", "main.go")
	gitRun(t, upstream, "commit", "--quiet", "-m", "reviewed")
	if out, err := runGit("git", "clone", "--quiet", upstream, src); err != nil {
		t.Fatalf("clone: %v: %s", err, out)
	}
	gitRun(t, src, "checkout", "--quiet", "-b", "feature")
	writeFile(t, filepath.Join(src, "main.go"), "dirty and unreviewed\n")
	writeFile(t, filepath.Join(src, "extra.go"), "untracked\n")
	return upstream, src
}

// testGit is the git source with the test-only file transport on: the
// fixtures fetch from a local repository.
func testGit() GitSource { return GitSource{Run: runGit, fileTransport: true} }

func TestGitSourceExportsRemoteCommitNotWorkingTree(t *testing.T) {
	upstream, src := checkoutFixture(t)
	// The remote moves on after the clone; the export must follow it.
	writeFile(t, filepath.Join(upstream, "main.go"), "reviewed v2\n")
	gitRun(t, upstream, "commit", "--quiet", "-am", "v2")
	want := gitRun(t, upstream, "rev-parse", "HEAD")

	statusBefore := gitRun(t, src, "status", "--porcelain=v2", "--branch")
	indexBefore, err := os.ReadFile(filepath.Join(src, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}

	exp, err := testGit().Export(src, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if exp.Commit != want {
		t.Fatalf("commit = %s, want %s", exp.Commit, want)
	}
	if got := readFile(t, filepath.Join(exp.Dir, "main.go")); got != "reviewed v2\n" {
		t.Fatalf("export holds %q", got)
	}
	if _, err := os.Stat(filepath.Join(exp.Dir, "extra.go")); !os.IsNotExist(err) {
		t.Fatalf("untracked working-tree file leaked into export: %v", err)
	}

	// source_dir is untouched: branch, dirty content, index, no new objects
	// from the fetch.
	if got := gitRun(t, src, "status", "--porcelain=v2", "--branch"); got != statusBefore {
		t.Fatalf("status changed:\n%s\n--\n%s", statusBefore, got)
	}
	if got := readFile(t, filepath.Join(src, "main.go")); got != "dirty and unreviewed\n" {
		t.Fatalf("working tree rewritten: %q", got)
	}
	if indexAfter, _ := os.ReadFile(filepath.Join(src, ".git", "index")); string(indexAfter) != string(indexBefore) {
		t.Fatal("index modified")
	}
	if out, err := runGit("git", "-C", src, "cat-file", "-e", want); err == nil {
		t.Fatalf("fetch wrote into source_dir: %s", out)
	}

	exp.Cleanup()
	if _, err := os.Stat(exp.Dir); !os.IsNotExist(err) {
		t.Fatalf("export dir survives cleanup: %v", err)
	}
	exp.Cleanup()
}

func TestGitSourceExplicitRefAndFailures(t *testing.T) {
	upstream, src := checkoutFixture(t)
	gitRun(t, upstream, "branch", "release")
	writeFile(t, filepath.Join(upstream, "main.go"), "on main only\n")
	gitRun(t, upstream, "commit", "--quiet", "-am", "main moves")

	exp, err := testGit().Export(src, "", "release")
	if err != nil {
		t.Fatal(err)
	}
	defer exp.Cleanup()
	if got := readFile(t, filepath.Join(exp.Dir, "main.go")); got != "reviewed\n" {
		t.Fatalf("release export holds %q", got)
	}

	if _, err := testGit().Export(src, "", "no-such-ref"); err == nil || errors.Is(err, ErrNotCheckout) {
		t.Fatalf("unknown ref: %v", err)
	}
	if _, err := testGit().Export(t.TempDir(), "", ""); !errors.Is(err, ErrNotCheckout) {
		t.Fatalf("plain dir: %v", err)
	}
	gitRun(t, src, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone"))
	if _, err := testGit().Export(src, "", ""); err == nil || errors.Is(err, ErrNotCheckout) {
		t.Fatalf("fetch failure: %v", err)
	}
}

func TestGitSourceIgnoresInheritedRepositoryVariables(t *testing.T) {
	_, src := checkoutFixture(t)
	// A post-merge step launched from a git hook carries these.
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "elsewhere"))
	t.Setenv("GIT_WORK_TREE", t.TempDir())
	exp, err := testGit().Export(src, "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer exp.Cleanup()
	if got := readFile(t, filepath.Join(exp.Dir, "main.go")); !strings.HasPrefix(got, "reviewed") {
		t.Fatalf("export holds %q", got)
	}
}

// gitConfigFailure runs a GitSource whose `git config` call fails with err
// and out; any later git call fails the test, since Export must stop there.
func gitConfigFailure(t *testing.T, err error, out string) error {
	t.Helper()
	calls := 0
	run := func(name string, args ...string) (string, error) {
		calls++
		return out, err
	}
	_, xerr := GitSource{Run: run}.Export("/srv/src", "", "")
	if calls != 1 {
		t.Fatalf("git ran %d times after the config read failed", calls)
	}
	return xerr
}

func TestGitSourceTellsConfigFailuresApart(t *testing.T) {
	exit := func(code string) error { return exec.Command("sh", "-c", "exit "+code).Run() }

	// Key absent: exit 1 with no output.
	if err := gitConfigFailure(t, exit("1"), ""); !errors.Is(err, ErrNotCheckout) || !strings.Contains(err.Error(), "/srv/src") {
		t.Fatalf("key absent: %v", err)
	}
	// Confirmed not a repository.
	if err := gitConfigFailure(t, exit("128"), "fatal: not a git repository (or any of the parent directories): .git"); !errors.Is(err, ErrNotCheckout) {
		t.Fatalf("not a repository: %v", err)
	}
	// Missing git binary is an apply-class failure, not an invalid values file.
	missing := &exec.Error{Name: "git", Err: exec.ErrNotFound}
	if err := gitConfigFailure(t, missing, ""); err == nil || errors.Is(err, ErrNotCheckout) || !strings.Contains(err.Error(), "executable file not found") {
		t.Fatalf("missing binary: %v", err)
	}
	// Permission or other failure: git's text is kept, the URL never appears.
	err := gitConfigFailure(t, exit("128"), "fatal: detected dubious ownership in repository")
	if err == nil || errors.Is(err, ErrNotCheckout) || !strings.Contains(err.Error(), "dubious ownership") {
		t.Fatalf("other exit: %v", err)
	}
	err = gitConfigFailure(t, errors.New("fork/exec /usr/bin/git: permission denied"), "")
	if err == nil || errors.Is(err, ErrNotCheckout) || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("io error: %v", err)
	}
}

// recordingGit answers each git verb the way a successful fetch would and
// records every argument list.
type recordingGit struct{ calls [][]string }

func (r *recordingGit) run(_ string, args ...string) (string, error) {
	r.calls = append(r.calls, args)
	switch gitVerb(args) {
	case "config":
		return "https://git.example.invalid/origin.git", nil
	case "rev-parse":
		return "0123456789abcdef0123456789abcdef01234567", nil
	}
	return "", nil
}

func (r *recordingGit) verbs() []string {
	var out []string
	for _, a := range r.calls {
		out = append(out, gitVerb(a))
	}
	return out
}

func hasArgs(args, want []string) bool {
	return strings.Contains(" "+strings.Join(args, " ")+" ", " "+strings.Join(want, " ")+" ")
}

func TestGitSourceWithURLNeverReadsSourceDirConfig(t *testing.T) {
	rec := &recordingGit{}
	const want = "https://git.example.invalid/pinned.git"
	exp, err := GitSource{Run: rec.run}.Export("/srv/src", want, "release")
	if err != nil {
		t.Fatal(err)
	}
	defer exp.Cleanup()
	for _, a := range rec.calls {
		if gitVerb(a) == "config" || hasArgs(a, []string{"-C", "/srv/src"}) {
			t.Fatalf("source_dir was touched: %v", a)
		}
	}
	var fetch []string
	for _, a := range rec.calls {
		if gitVerb(a) == "fetch" {
			fetch = a
		}
	}
	if fetch == nil || !hasArgs(fetch, []string{"--", want, "release"}) {
		t.Fatalf("fetch did not use source_url: %v", fetch)
	}
	if !hasArgs(fetch, fetchTransportArgs) {
		t.Fatalf("fetch lacks the transport allowlist: %v", fetch)
	}
	if hasArgs(fetch, []string{"protocol.file.allow=always"}) {
		t.Fatalf("production source enables file transport: %v", fetch)
	}
}

func TestGitSourceOriginFallbackReadsOnlyConfig(t *testing.T) {
	rec := &recordingGit{}
	exp, err := GitSource{Run: rec.run}.Export("/srv/src", "", "")
	if err != nil {
		t.Fatal(err)
	}
	defer exp.Cleanup()
	if rec.verbs()[0] != "config" || !hasArgs(rec.calls[0], []string{"config", "--get", "remote.origin.url"}) {
		t.Fatalf("first call = %v", rec.calls[0])
	}
	for _, a := range rec.calls[1:] {
		if hasArgs(a, []string{"-C", "/srv/src"}) {
			t.Fatalf("source_dir touched beyond the config read: %v", a)
		}
	}
}

func TestGitSourceRefusesDisallowedTransports(t *testing.T) {
	for _, u := range []string{
		"file:///srv/x", "/srv/x", "ext::sh -c id", "ext::id", "git@host.example:org/r.git",
		"http://host.example/r.git", "git://host.example/r.git", "https:host.example/r", "-uhttps://h/r",
	} {
		rec := &recordingGit{}
		_, err := GitSource{Run: rec.run}.Export("/srv/src", u, "")
		if !errors.Is(err, ErrFetchTransport) || errors.Is(err, ErrNotCheckout) {
			t.Fatalf("url %q: %v", u, err)
		}
		if err != nil && strings.Contains(err.Error(), "srv/x") {
			t.Fatalf("error echoes the URL: %v", err)
		}
		if len(rec.calls) != 0 {
			t.Fatalf("url %q: git ran: %v", u, rec.calls)
		}
	}
	for _, u := range []string{"https://host.example/r.git", "ssh://git@host.example/org/r.git", "HTTPS://host.example/r"} {
		if err := ValidateFetchURL(u); err != nil {
			t.Fatalf("url %q rejected: %v", u, err)
		}
	}
}

func TestGitSourceRefusesFileOrigin(t *testing.T) {
	_, src := checkoutFixture(t)
	// The fixture's origin is a local path; production GitSource must refuse
	// it after the config read and before any fetch or init.
	rec := 0
	run := func(name string, args ...string) (string, error) {
		rec++
		return runGit(name, args...)
	}
	_, err := GitSource{Run: run}.Export(src, "", "")
	if !errors.Is(err, ErrFetchTransport) || rec != 1 {
		t.Fatalf("err=%v git calls=%d", err, rec)
	}
}

func TestFetchTransportArgsBlockLocalPathInGit(t *testing.T) {
	upstream, _ := checkoutFixture(t)
	repo := filepath.Join(t.TempDir(), "r")
	if out, err := runGit("git", "init", "--quiet", repo); err != nil {
		t.Fatalf("init: %v: %s", err, out)
	}
	args := append(append([]string{}, fetchTransportArgs...), "-C", repo, "fetch", "--quiet", "--", upstream, "HEAD")
	if out, err := runGit("git", args...); err == nil {
		t.Fatalf("local path fetched despite protocol.allow=never: %s", out)
	}
}
