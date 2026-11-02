package update

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// SystemDeps wires the real host implementations.
func SystemDeps() Deps {
	return Deps{
		FS:      OSFS{},
		Systemd: ExecSystemd{Run: runCommand},
		Prober:  HTTPProber{Client: &http.Client{Timeout: 5 * time.Second}},
		Source:  GitSource{Run: runGit},
		Builder: GoBuilder{Run: runCommand},
		Host:    osHost{},
		Sleep:   time.Sleep,
	}
}

type osHost struct{}

func (osHost) IsPrivileged() bool { return os.Geteuid() == 0 }

// runCommand runs name with args (no shell) and returns combined output.
func runCommand(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// OSFS is the real filesystem.
type OSFS struct{}

func (OSFS) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }
func (OSFS) Rename(o, n string) error             { return os.Rename(o, n) }
func (OSFS) Remove(path string) error             { return os.Remove(path) }

// WriteAtomic writes through a temp file in the destination directory so a
// reader never observes a partial file.
func (OSFS) WriteAtomic(path string, data []byte, perm uint32) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	cleanup := func() { _ = os.Remove(name) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(os.FileMode(perm)); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(name, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

// CommandRunner executes a command and returns its combined output.
type CommandRunner func(name string, args ...string) (string, error)

// ExecSystemd drives systemctl through Run.
type ExecSystemd struct{ Run CommandRunner }

func (s ExecSystemd) ctl(args ...string) error {
	if out, err := s.Run("systemctl", args...); err != nil {
		return fmt.Errorf("systemctl %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return nil
}

// query returns true when systemctl exits 0, false on a non-zero exit (the
// documented "no" answer of is-active / is-enabled), and an error when
// systemctl could not run at all.
func (s ExecSystemd) query(args ...string) (bool, error) {
	_, err := s.Run("systemctl", args...)
	if err == nil {
		return true, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return false, nil
	}
	return false, err
}

func (s ExecSystemd) DaemonReload() error { return s.ctl("daemon-reload") }
func (s ExecSystemd) IsActive(u string) (bool, error) {
	return s.query("is-active", "--quiet", u)
}
func (s ExecSystemd) IsEnabled(u string) (bool, error) {
	return s.query("is-enabled", "--quiet", u)
}
func (s ExecSystemd) Enable(u string) error  { return s.ctl("enable", u) }
func (s ExecSystemd) Disable(u string) error { return s.ctl("disable", u) }
func (s ExecSystemd) Stop(u string) error    { return s.ctl("stop", u) }
func (s ExecSystemd) Restart(u string) error { return s.ctl("restart", u) }

// HTTPProber checks a readiness endpoint for HTTP 200.
type HTTPProber struct{ Client *http.Client }

func (p HTTPProber) Ready(url, token string) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := p.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

// GoBuilder runs `go build` from the source checkout.
type GoBuilder struct{ Run CommandRunner }

func (b GoBuilder) Build(srcDir, pkg, outPath string) error {
	if out, err := b.Run("go", "build", "-C", srcDir, "-o", outPath, pkg); err != nil {
		return fmt.Errorf("go build: %v: %s", err, out)
	}
	return nil
}
