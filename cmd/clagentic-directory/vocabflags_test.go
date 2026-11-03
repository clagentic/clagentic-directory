package main

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCheckVocabFlags(t *testing.T) {
	cases := []struct {
		name, source, vocabFile, gitVocab string
		wantErr                           bool
	}{
		{"git with git vocab", "git", "", "v.yaml", false},
		{"git without any vocab", "git", "", "", false},
		{"git with vocab-file is ignored so rejected", "git", "v.yaml", "", true},
		{"file with vocab-file", "file", "v.yaml", "", false},
		{"file with git vocab is ignored so rejected", "file", "", "v.yaml", true},
	}
	for _, c := range cases {
		err := checkVocabFlags(c.source, c.vocabFile, c.gitVocab)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err=%v wantErr=%v", c.name, err, c.wantErr)
		}
	}
}

func TestFileModeOpen(t *testing.T) {
	if !fileModeOpen("file", "", "") {
		t.Error("file mode without vocabulary must warn")
	}
	if fileModeOpen("file", "v.yaml", "") || fileModeOpen("file", "", "ext.yaml") {
		t.Error("file mode with a vocabulary must not warn")
	}
	if fileModeOpen("git", "", "") {
		t.Error("git mode reports its own mode from the store")
	}
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func buildDaemon(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "clagentic-directory")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

// gitOrigin makes a bare origin holding one in-vocabulary v2 agent under
// agents/ and the vocabulary file at the repo root.
func gitOrigin(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	bare, work := filepath.Join(root, "origin.git"), filepath.Join(root, "work")
	runGit(t, root, "init", "--bare", "-b", "main", bare)
	runGit(t, root, "init", "-b", "main", work)
	runGit(t, work, "remote", "add", "origin", bare)
	if err := os.MkdirAll(filepath.Join(work, "agents"), 0700); err != nil {
		t.Fatal(err)
	}
	agent := "schema_version: 2\nidentity:\n  name: alpha\n  version: 1.0.0\n  description: d\n" +
		"capabilities:\n  - id: do-thing\n    name: Do\n    description: d\n" +
		"    triggers:\n      intents:\n        - do-thing\n    returns:\n      verdict_field: result\n      format: text\n"
	vocab := "schema_version: 1\nintents:\n  do-thing: x\nformats:\n  text: x\n"
	for p, body := range map[string]string{"agents/alpha.yaml": agent, "directory/vocabulary.v1.yaml": vocab} {
		full := filepath.Join(work, p)
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, work, "add", "agents/alpha.yaml", "directory/vocabulary.v1.yaml")
	runGit(t, work, "commit", "-m", "init")
	runGit(t, work, "push", "origin", "main")
	return "file://" + bare
}

// startDaemon runs the binary and waits until it logs that it is listening, or exits.
func startDaemon(t *testing.T, bin string, args ...string) (*syncBuf, *exec.Cmd, <-chan error) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	buf := &syncBuf{}
	pr, pw := io.Pipe()
	cmd.Stderr = io.MultiWriter(buf, pw)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait(); _ = pw.Close() }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	ready := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(pr)
		closed := false
		for sc.Scan() {
			if !closed && strings.Contains(sc.Text(), "clagentic-directory listening") {
				close(ready)
				closed = true
			}
		}
	}()
	select {
	case <-ready:
	case <-time.After(60 * time.Second):
		t.Fatalf("daemon never listened:\n%s", buf.String())
	case err := <-exited:
		t.Fatalf("daemon exited early: %v\n%s", err, buf.String())
	}
	return buf, cmd, exited
}

func TestDaemonGitModeLogsVocabularyNotValidateOpen(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the daemon")
	}
	bin := buildDaemon(t)
	url := gitOrigin(t)
	buf, _, _ := startDaemon(t, bin,
		"--registry-source", "git", "--registry-git-url", url,
		"--registry-git-subpath", "agents",
		"--registry-git-vocab", "directory/vocabulary.v1.yaml",
		"--registry-cache-dir", t.TempDir(),
		"--listen", "127.0.0.1:0")
	out := buf.String()
	for _, want := range []string{"vocabulary loaded", "vocab_path=directory/vocabulary.v1.yaml", "validation_mode=ValidateStrict"} {
		if !strings.Contains(out, want) {
			t.Errorf("log missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "UNSAFE") {
		t.Errorf("log mentions ValidateOpen:\n%s", out)
	}
}

func TestDaemonGitModeRejectsVocabFile(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the daemon")
	}
	bin := buildDaemon(t)
	out, err := exec.Command(bin, "--registry-source", "git", "--registry-git-url", "file:///nonexistent",
		"--vocab-file", "/x.yaml", "--registry-cache-dir", t.TempDir(), "--listen", "127.0.0.1:0").CombinedOutput()
	if err == nil || !strings.Contains(string(out), "--vocab-file is not used with --registry-source=git") {
		t.Fatalf("expected rejection, err=%v out=%s", err, out)
	}
}
