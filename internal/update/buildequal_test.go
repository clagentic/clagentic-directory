package update

import (
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// buildFixture is a throwaway module shaped like this repo (same Makefile, same
// package path) so the test builds with the real build recipe and no network.
func buildFixture(t *testing.T) string {
	t.Helper()
	for _, tool := range []string{"git", "make", "go"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed", tool)
		}
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	mk, err := os.ReadFile(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(t.TempDir(), "checkout")
	if out, err := runGit("git", "init", "--quiet", "--initial-branch=main", repo); err != nil {
		t.Fatalf("init: %v: %s", err, out)
	}
	putFile(t, filepath.Join(repo, "Makefile"), string(mk))
	putFile(t, filepath.Join(repo, "go.mod"), "module fixture\n\ngo 1.21\n")
	putFile(t, filepath.Join(repo, "cmd", "clagentic-directory", "main.go"),
		"package main\n\nimport \"fmt\"\n\nvar buildRevision string\n\nfunc main() { fmt.Println(buildRevision) }\n")
	gitRun(t, repo, "add", "Makefile", "go.mod", "cmd")
	gitRun(t, repo, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "--quiet", "-m", "fixture")
	return repo
}

func putFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, content)
}

func makeBuild(t *testing.T, repo string) string {
	t.Helper()
	if out, err := runCommand("make", "-C", repo, "build"); err != nil {
		t.Fatalf("make build: %v: %s", err, out)
	}
	return filepath.Join(repo, "clagentic-directory")
}

func fileDigest(t *testing.T, path string) [32]byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(b)
}

// The Makefile build (inside a checkout) and update's export build (no .git)
// must produce the same bytes at one commit, or the first update after a
// Makefile deploy would rebuild, differ and restart.
func TestMakefileAndExportBuildsAreByteIdentical(t *testing.T) {
	repo := buildFixture(t)
	commit := gitRun(t, repo, "rev-parse", "HEAD")
	fromMake := makeBuild(t, repo)

	exp, err := testGit().Export(repo, repo, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	defer exp.Cleanup()
	if exp.Commit != commit {
		t.Fatalf("export commit %s, want %s", exp.Commit, commit)
	}
	out := filepath.Join(t.TempDir(), "from-export")
	if err := (GoBuilder{Run: runCommand}).Build(exp.Dir, "./cmd/clagentic-directory", out, exp.Commit); err != nil {
		t.Fatal(err)
	}
	if fileDigest(t, fromMake) != fileDigest(t, out) {
		t.Fatal("Makefile build and update export build differ at the same commit")
	}
	if got, err := runCommand(out); err != nil || got != commit {
		t.Fatalf("revision = %q (%v), want %s", got, err, commit)
	}
}

func TestMakefileDirtyMarkerTracksWhatGoBuildCompiles(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, repo string)
		dirty bool
	}{
		{"clean", func(*testing.T, string) {}, false},
		{"untracked non-go directory", func(t *testing.T, repo string) {
			putFile(t, filepath.Join(repo, "attempts", "notes.txt"), "x\n")
		}, false},
		{"untracked go file", func(t *testing.T, repo string) {
			putFile(t, filepath.Join(repo, "cmd", "clagentic-directory", "extra.go"), "package main\n")
		}, true},
		{"untracked go file in new directory", func(t *testing.T, repo string) {
			putFile(t, filepath.Join(repo, "internal", "x", "x.go"), "package x\n")
		}, true},
		{"untracked go.sum", func(t *testing.T, repo string) {
			putFile(t, filepath.Join(repo, "go.sum"), "\n")
		}, true},
		{"modified tracked file", func(t *testing.T, repo string) {
			putFile(t, filepath.Join(repo, "go.mod"), "module fixture\n\ngo 1.22\n")
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := buildFixture(t)
			commit := gitRun(t, repo, "rev-parse", "HEAD")
			tc.setup(t, repo)
			want := commit
			if tc.dirty {
				want += "-dirty"
			}
			got, err := runCommand(makeBuild(t, repo))
			if err != nil || got != want {
				t.Fatalf("revision = %q (%v), want %q", got, err, want)
			}
		})
	}
}
