package main

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/clagentic/clagentic-directory/internal/update"
)

type cmdFS struct{ files map[string][]byte }

func (m *cmdFS) ReadFile(p string) ([]byte, error) {
	b, ok := m.files[p]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: p, Err: fs.ErrNotExist}
	}
	return b, nil
}
func (m *cmdFS) WriteAtomic(p string, d []byte, _ uint32) error { m.files[p] = d; return nil }
func (m *cmdFS) Rename(o, n string) error {
	m.files[n] = m.files[o]
	delete(m.files, o)
	return nil
}
func (m *cmdFS) Remove(p string) error {
	if _, ok := m.files[p]; !ok {
		return &fs.PathError{Op: "remove", Path: p, Err: fs.ErrNotExist}
	}
	delete(m.files, p)
	return nil
}

type cmdSD struct {
	active, enabled map[string]bool
	calls           []string
}

func (s *cmdSD) DaemonReload() error              { s.calls = append(s.calls, "reload"); return nil }
func (s *cmdSD) IsActive(u string) (bool, error)  { return s.active[u], nil }
func (s *cmdSD) IsEnabled(u string) (bool, error) { return s.enabled[u], nil }
func (s *cmdSD) Enable(u string) error {
	s.enabled[u] = true
	s.calls = append(s.calls, "enable "+u)
	return nil
}
func (s *cmdSD) Disable(u string) error {
	s.enabled[u] = false
	s.calls = append(s.calls, "disable "+u)
	return nil
}
func (s *cmdSD) Stop(u string) error {
	s.active[u] = false
	s.calls = append(s.calls, "stop "+u)
	return nil
}
func (s *cmdSD) Restart(u string) error {
	s.active[u] = true
	s.calls = append(s.calls, "restart "+u)
	return nil
}

type cmdProber struct{ err error }

func (p cmdProber) Ready(string, string) error { return p.err }

type cmdBuilder struct {
	fs      *cmdFS
	content string
}

func (b cmdBuilder) Build(_, _, out, _ string) error { b.fs.files[out] = []byte(b.content); return nil }

type cmdSource struct{ err error }

func (s cmdSource) Export(string, string, string) (*update.Export, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &update.Export{Dir: "/export", Commit: "0123456789abcdef0123456789abcdef01234567", Cleanup: func() {}}, nil
}

type cmdHost bool

func (h cmdHost) IsPrivileged() bool { return bool(h) }

const updateValues = `
schema_version: 1
unit: {dir: /units, user: svc}
binary: {install_path: /bin/clagentic-directory, source_dir: /src}
daemon:
  registry_source: git
  registry_git_url: git@git.example.com:org/reg.git
  registry_git_vocab: vocabulary.v1.yaml
  registry_cache_dir: /cache
supersedes: [sync.timer]
`

func writeValues(t *testing.T, y string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "values.yaml")
	if err := os.WriteFile(p, []byte(y), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func newDeps(root bool, probe error) (update.Deps, *cmdFS, *cmdSD) {
	f := &cmdFS{files: map[string][]byte{}}
	sd := &cmdSD{active: map[string]bool{}, enabled: map[string]bool{}}
	return update.Deps{FS: f, Systemd: sd, Prober: cmdProber{probe}, Source: cmdSource{}, Builder: cmdBuilder{f, "bin"}, Host: cmdHost(root), Sleep: func(time.Duration) {}}, f, sd
}

// callerShape models how one caller invokes update: its environment and exact
// argument list. update has no interactive path, so the environment is what
// differs between callers; it reaches the build step through envBuilder.
type callerShape struct {
	name string
	// env is the full environment the shape runs with.
	env map[string]string
	// argv is the exact argument list the caller passes.
	argv func(values string) []string
}

// envBuilder refuses to build when its environment lacks what a real build
// needs (PATH to find the toolchain), so a caller shape with a stripped
// environment fails if update starts to depend on it.
type envBuilder struct {
	inner cmdBuilder
	env   map[string]string
}

func (b envBuilder) Build(src, pkg, out, rev string) error {
	if b.env["PATH"] == "" {
		return errors.New("build: PATH is empty")
	}
	return b.inner.Build(src, pkg, out, rev)
}

func TestCallerShapesAcrossHostStates(t *testing.T) {
	shapes := []callerShape{
		{
			// Minimal PATH, HOME unset.
			name: "post-merge step",
			env:  map[string]string{"PATH": "/usr/sbin:/usr/bin:/sbin:/bin"},
			argv: func(v string) []string { return []string{"--values", v} },
		},
		{
			name: "operator by hand",
			env:  map[string]string{"PATH": "/usr/local/bin:/usr/bin", "HOME": filepath.Join(t.TempDir(), "home-never-created"), "TERM": "xterm", "LANG": "C.UTF-8"},
			argv: func(v string) []string { return []string{"--values=" + v} },
		},
		{
			// Same stripped environment as the post-merge step, but it
			// always meets a host an earlier run already converged.
			name: "rerun",
			env:  map[string]string{"PATH": "/usr/sbin:/usr/bin:/sbin:/bin"},
			argv: func(v string) []string { return []string{"--values", v} },
		},
	}
	states := []string{"not installed", "changed unit", "converged"}
	vp := writeValues(t, updateValues)
	v, err := update.LoadValues(vp)
	if err != nil {
		t.Fatal(err)
	}
	for _, shape := range shapes {
		for _, state := range states {
			t.Run(shape.name+"/"+state, func(t *testing.T) {
				deps, f, sd := newDeps(true, nil)
				deps.Builder = envBuilder{cmdBuilder{f, "bin"}, shape.env}
				effective := state
				if shape.name == "rerun" {
					// A rerun is only meaningful against a converged host.
					effective = "converged"
				}
				if effective != "not installed" {
					f.files[v.UnitPath()] = v.RenderUnit()
					f.files[v.Binary.InstallPath] = []byte("bin")
					sd.active[v.UnitName()], sd.enabled[v.UnitName()] = true, true
				}
				if effective == "changed unit" {
					f.files[v.UnitPath()] = []byte("old")
					sd.active["sync.timer"], sd.enabled["sync.timer"] = true, true
				}
				var out, errb bytes.Buffer
				for run := 1; run <= 2; run++ {
					out.Reset()
					sd.calls = nil
					if rc := runUpdateWith(shape.argv(vp), deps, noEnv, &out, &errb); rc != 0 {
						t.Fatalf("run %d rc=%d stderr=%s", run, rc, errb.String())
					}
					if run == 1 && effective == "converged" && len(sd.calls) != 0 {
						t.Fatalf("converged host mutated: %v", sd.calls)
					}
					if !strings.Contains(out.String(), "source: built commit 0123456789abcdef0123456789abcdef01234567") {
						t.Fatalf("run %d does not report the built commit: %s", run, out.String())
					}
					if run == 2 && (len(sd.calls) != 0 || !strings.Contains(out.String(), "no changes")) {
						t.Fatalf("second run not a no-op: calls=%v out=%s", sd.calls, out.String())
					}
				}
				if string(f.files[v.UnitPath()]) != string(v.RenderUnit()) {
					t.Fatal("unit does not match values")
				}
			})
		}
	}
}

// A shape whose environment lacks PATH must fail loudly, proving the stripped
// shapes really exercise the environment dependency.
func TestBuildNeedsPATH(t *testing.T) {
	vp := writeValues(t, updateValues)
	deps, f, _ := newDeps(true, nil)
	deps.Builder = envBuilder{cmdBuilder{f, "bin"}, map[string]string{}}
	var out, errb bytes.Buffer
	if rc := runUpdateWith([]string{"--values", vp}, deps, noEnv, &out, &errb); rc != update.ExitApply {
		t.Fatalf("rc=%d want %d", rc, update.ExitApply)
	}
}

func TestUpdateExitCodes(t *testing.T) {
	vp := writeValues(t, updateValues)
	bad := writeValues(t, "schema_version: 1\nnope: true\n")
	pct := writeValues(t, strings.Replace(updateValues, "user: svc", "user: svc, env_file: /etc/%h.env", 1))
	cases := []struct {
		name  string
		args  []string
		root  bool
		probe error
		want  int
	}{
		{"ok", []string{"--values", vp}, true, nil, 0},
		{"missing flag", nil, true, nil, 2},
		{"unreadable values", []string{"--values", vp + ".absent"}, true, nil, 2},
		{"invalid values", []string{"--values", bad}, true, nil, 2},
		{"env_file with percent", []string{"--values", pct}, true, nil, 2},
		{"no privilege", []string{"--values", vp}, false, nil, 3},
		{"readyz failure", []string{"--values", vp}, true, errors.New("503"), 5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			deps, _, _ := newDeps(c.root, c.probe)
			var out, errb bytes.Buffer
			if rc := runUpdateWith(c.args, deps, noEnv, &out, &errb); rc != c.want {
				t.Fatalf("rc=%d want %d stderr=%s", rc, c.want, errb.String())
			}
		})
	}
}

func noEnv(string) string { return "" }

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// installDefault writes the values file at the default location under root.
func installDefault(t *testing.T, root string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(root, update.DefaultValuesRel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(updateValues), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestUpdateDefaultValuesPath(t *testing.T) {
	xdg, home := t.TempDir(), t.TempDir()
	xp := installDefault(t, xdg, 0o600)
	hp := installDefault(t, filepath.Join(home, ".config"), 0o644)
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"XDG set", map[string]string{"XDG_CONFIG_HOME": xdg, "HOME": home}, xp},
		{"HOME only", map[string]string{"HOME": home}, hp},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			deps, _, _ := newDeps(true, nil)
			var out, errb bytes.Buffer
			if rc := runUpdateWith(nil, deps, envOf(c.env), &out, &errb); rc != 0 {
				t.Fatalf("rc=%d stderr=%s", rc, errb.String())
			}
			if want := "values: " + c.want + " (default)\n"; !strings.HasPrefix(out.String(), want) {
				t.Fatalf("stdout %q does not start with %q", out.String(), want)
			}
		})
	}
	t.Run("neither set", func(t *testing.T) {
		deps, _, _ := newDeps(true, nil)
		var out, errb bytes.Buffer
		if rc := runUpdateWith(nil, deps, noEnv, &out, &errb); rc != update.ExitInvalid {
			t.Fatalf("rc=%d want 2", rc)
		}
	})
	t.Run("missing file names the path", func(t *testing.T) {
		empty := t.TempDir()
		deps, _, _ := newDeps(true, nil)
		var out, errb bytes.Buffer
		rc := runUpdateWith(nil, deps, envOf(map[string]string{"XDG_CONFIG_HOME": empty}), &out, &errb)
		want := filepath.Join(empty, update.DefaultValuesRel)
		if rc != update.ExitInvalid || !strings.Contains(errb.String(), want) {
			t.Fatalf("rc=%d stderr=%s, want 2 naming %s", rc, errb.String(), want)
		}
	})
	t.Run("unsafe default file", func(t *testing.T) {
		root := t.TempDir()
		installDefault(t, root, 0o666)
		deps, _, _ := newDeps(true, nil)
		var out, errb bytes.Buffer
		if rc := runUpdateWith(nil, deps, envOf(map[string]string{"XDG_CONFIG_HOME": root}), &out, &errb); rc != update.ExitInvalid {
			t.Fatalf("rc=%d want 2", rc)
		}
	})
}

func TestUpdatePrintsExplicitValuesSource(t *testing.T) {
	vp := writeValues(t, updateValues)
	deps, _, _ := newDeps(true, nil)
	var out, errb bytes.Buffer
	if rc := runUpdateWith([]string{"--values", vp}, deps, noEnv, &out, &errb); rc != 0 {
		t.Fatalf("rc=%d stderr=%s", rc, errb.String())
	}
	if want := "values: " + vp + " (explicit)\n"; !strings.HasPrefix(out.String(), want) {
		t.Fatalf("stdout %q does not start with %q", out.String(), want)
	}
}

func TestExplicitValuesFileChecks(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.yaml")
	if err := os.WriteFile(real, []byte(updateValues), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.yaml")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	gw := filepath.Join(dir, "gw.yaml")
	if err := os.WriteFile(gw, []byte(updateValues), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(gw, 0o620); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{link, gw} {
		deps, _, _ := newDeps(true, nil)
		var out, errb bytes.Buffer
		rc := runUpdateWith([]string{"--values", p}, deps, noEnv, &out, &errb)
		if rc != update.ExitInvalid || !strings.Contains(errb.String(), p) {
			t.Fatalf("%s: rc=%d stderr=%s", p, rc, errb.String())
		}
	}
}

func TestDispatchRoutesSubcommands(t *testing.T) {
	// An empty config root keeps the real default values file, if this host
	// has one, out of the test.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	// "update" with no --values reaches the update handler, finds no default
	// values file, and returns its invalid-input code without touching the host.
	if rc, handled := dispatch([]string{"update"}); !handled || rc != update.ExitInvalid {
		t.Fatalf("update: rc=%d handled=%v, want rc=%d handled=true", rc, handled, update.ExitInvalid)
	}
	if rc, handled := dispatch([]string{"update", "--help"}); !handled || rc != update.ExitInvalid {
		t.Fatalf("update --help: rc=%d handled=%v", rc, handled)
	}
	for _, args := range [][]string{nil, {"--listen", ":1"}, {"other"}} {
		if _, handled := dispatch(args); handled {
			t.Fatalf("dispatch(%v) claimed a non-subcommand", args)
		}
	}
}

// Every flag the update verb can render must be a flag main.go defines.
func TestRenderedFlagsExistInDaemon(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	defined := map[string]bool{}
	for _, m := range regexp.MustCompile(`flag\.(?:String|Bool|Duration)\("([a-z-]+)"`).FindAllStringSubmatch(string(src), -1) {
		defined[m[1]] = true
	}
	full := `
schema_version: 1
unit: {dir: /u, user: svc}
binary: {install_path: /b, source_dir: /s}
daemon:
  registry_source: git
  registry_git_url: u
  registry_git_ref: main
  registry_git_subpath: r/
  registry_git_vocab: v.yaml
  registry_git_poll: 60s
  registry_cache_dir: /c
  registry_secret_keyfile: /k
  listen: ":1"
  log_level: info
  self_build_base_dir: /sb
  self_build_mcp_discovery: true
  self_build_source_watch_url: http://x
  self_build_source_watch_interval: 1s
  self_build_source_watch_window: 1s
  self_build_usage_event_store_url: http://y
  self_build_usage_window: 1s
`
	v, err := update.ParseValues([]byte(full))
	if err != nil {
		t.Fatal(err)
	}
	file := *v
	file.Daemon.RegistrySource = "file"
	file.Daemon.RegistryDir, file.Daemon.VocabFile = "/r", "/v.yaml"
	for _, vv := range []*update.Values{v, &file} {
		for _, a := range vv.DaemonArgs() {
			if name := strings.TrimPrefix(a[0], "--"); !defined[name] {
				t.Errorf("rendered flag %s is not defined in main.go", a[0])
			}
		}
	}
	if len(defined) < 15 {
		t.Fatalf("flag scrape found only %d flags", len(defined))
	}
}

func TestSourceFailureExitCodesLeaveHostUntouched(t *testing.T) {
	vp := writeValues(t, updateValues)
	for _, c := range []struct {
		name string
		err  error
		want int
	}{
		{"fetch or unknown ref", errors.New("git fetch: exit status 128"), update.ExitApply},
		{"not a checkout", update.ErrNotCheckout, update.ExitInvalid},
	} {
		t.Run(c.name, func(t *testing.T) {
			deps, f, sd := newDeps(true, nil)
			deps.Source = cmdSource{c.err}
			var out, errb bytes.Buffer
			if rc := runUpdateWith([]string{"--values", vp}, deps, noEnv, &out, &errb); rc != c.want {
				t.Fatalf("rc=%d want %d stderr=%s", rc, c.want, errb.String())
			}
			if len(f.files) != 0 || len(sd.calls) != 0 || strings.Contains(out.String(), "changed:") {
				t.Fatalf("host touched: files=%d calls=%v out=%s", len(f.files), sd.calls, out.String())
			}
		})
	}
}
