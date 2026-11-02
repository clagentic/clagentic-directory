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

	exp, err := GitSource{Run: runGit}.Export(src, "")
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

	exp, err := GitSource{Run: runGit}.Export(src, "release")
	if err != nil {
		t.Fatal(err)
	}
	defer exp.Cleanup()
	if got := readFile(t, filepath.Join(exp.Dir, "main.go")); got != "reviewed\n" {
		t.Fatalf("release export holds %q", got)
	}

	if _, err := (GitSource{Run: runGit}).Export(src, "no-such-ref"); err == nil || errors.Is(err, ErrNotCheckout) {
		t.Fatalf("unknown ref: %v", err)
	}
	if _, err := (GitSource{Run: runGit}).Export(t.TempDir(), ""); !errors.Is(err, ErrNotCheckout) {
		t.Fatalf("plain dir: %v", err)
	}
	gitRun(t, src, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone"))
	if _, err := (GitSource{Run: runGit}).Export(src, ""); err == nil || errors.Is(err, ErrNotCheckout) {
		t.Fatalf("fetch failure: %v", err)
	}
}

func TestGitSourceIgnoresInheritedRepositoryVariables(t *testing.T) {
	_, src := checkoutFixture(t)
	// A post-merge step launched from a git hook carries these.
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "elsewhere"))
	t.Setenv("GIT_WORK_TREE", t.TempDir())
	exp, err := GitSource{Run: runGit}.Export(src, "")
	if err != nil {
		t.Fatal(err)
	}
	defer exp.Cleanup()
	if got := readFile(t, filepath.Join(exp.Dir, "main.go")); !strings.HasPrefix(got, "reviewed") {
		t.Fatalf("export holds %q", got)
	}
}
