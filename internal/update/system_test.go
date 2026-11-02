package update

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExecSystemdBuildsArgs(t *testing.T) {
	var got []string
	s := ExecSystemd{Run: func(name string, args ...string) (string, error) {
		got = append(got, name+" "+strings.Join(args, " "))
		return "", nil
	}}
	_ = s.DaemonReload()
	_ = s.Enable("a.service")
	_ = s.Disable("a.service")
	_ = s.Stop("a.service")
	_ = s.Restart("a.service")
	if on, err := s.IsActive("a.service"); !on || err != nil {
		t.Fatal("IsActive")
	}
	want := "systemctl daemon-reload|systemctl enable a.service|systemctl disable a.service|systemctl stop a.service|systemctl restart a.service|systemctl is-active --quiet a.service"
	if strings.Join(got, "|") != want {
		t.Fatalf("got %v", got)
	}
}

func TestExecSystemdQueryDistinguishesNoFromFailure(t *testing.T) {
	// A real non-zero exit is the documented "no"; a missing binary is an error.
	exitErr := exec.Command("sh", "-c", "exit 3").Run()
	s := ExecSystemd{Run: func(string, ...string) (string, error) { return "", exitErr }}
	if on, err := s.IsEnabled("x.service"); on || err != nil {
		t.Fatalf("exit status should mean no: on=%v err=%v", on, err)
	}
	s = ExecSystemd{Run: func(string, ...string) (string, error) { return "", errors.New("not found") }}
	if _, err := s.IsActive("x.service"); err == nil {
		t.Fatal("exec failure swallowed")
	}
}

func TestOSFSWriteAtomicInTempDir(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.service")
	var f OSFS
	if err := f.WriteAtomic(p, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.WriteAtomic(p, []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, _ := f.ReadFile(p)
	if string(b) != "two" {
		t.Fatalf("got %q", b)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 1 {
		t.Fatalf("temp file leaked: %v", ents)
	}
	if err := f.WriteAtomic(filepath.Join(dir, "missing", "x"), nil, 0o644); err == nil {
		t.Fatal("expected error for missing dir")
	}
}

func TestHTTPProber(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.URL.Path != "/readyz" {
			w.WriteHeader(404)
			return
		}
		if auth != "Bearer tok" {
			w.WriteHeader(401)
		}
	}))
	defer srv.Close()
	p := HTTPProber{Client: &http.Client{Timeout: time.Second}}
	if err := p.Ready(srv.URL+"/readyz", "tok"); err != nil {
		t.Fatal(err)
	}
	if err := p.Ready(srv.URL+"/readyz", ""); err == nil {
		t.Fatal("401 treated as ready")
	}
	if err := p.Ready(srv.URL+"/other", "tok"); err == nil {
		t.Fatal("404 treated as ready")
	}
}

func TestGoBuilderArgs(t *testing.T) {
	var got string
	b := GoBuilder{Run: func(name string, args ...string) (string, error) {
		got = name + " " + strings.Join(args, " ")
		return "", nil
	}}
	if err := b.Build("/src", "./cmd/x", "/out/bin.update-new"); err != nil {
		t.Fatal(err)
	}
	if got != "go build -C /src -o /out/bin.update-new -- ./cmd/x" {
		t.Fatalf("got %s", got)
	}
	b.Run = func(string, ...string) (string, error) { return "syntax error", errors.New("exit 1") }
	if err := b.Build("/src", "./x", "/o"); err == nil || !strings.Contains(err.Error(), "syntax error") {
		t.Fatalf("err = %v", err)
	}
}
