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

func (b cmdBuilder) Build(_, _, out string) error { b.fs.files[out] = []byte(b.content); return nil }

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
	return update.Deps{FS: f, Systemd: sd, Prober: cmdProber{probe}, Builder: cmdBuilder{f, "bin"}, Host: cmdHost(root), Sleep: func(time.Duration) {}}, f, sd
}

// The post-merge runners (loadout-merge, loadout-post-merge) and an operator
// all invoke the same argv in the foreground with no stdin or tty use, so the
// shapes share one entry point; the state matrix is what varies.
func TestCallerShapesAcrossHostStates(t *testing.T) {
	shapes := []string{"loadout-merge post-merge step", "loadout-post-merge", "operator by hand"}
	states := []string{"not installed", "changed unit", "converged"}
	vp := writeValues(t, updateValues)
	v, err := update.LoadValues(vp)
	if err != nil {
		t.Fatal(err)
	}
	for _, shape := range shapes {
		for _, state := range states {
			t.Run(shape+"/"+state, func(t *testing.T) {
				deps, f, sd := newDeps(true, nil)
				if state != "not installed" {
					f.files[v.UnitPath()] = v.RenderUnit()
					f.files[v.Binary.InstallPath] = []byte("bin")
					sd.active[v.UnitName()], sd.enabled[v.UnitName()] = true, true
				}
				if state == "changed unit" {
					f.files[v.UnitPath()] = []byte("old")
					sd.active["sync.timer"], sd.enabled["sync.timer"] = true, true
				}
				var out, errb bytes.Buffer
				for run := 1; run <= 2; run++ {
					out.Reset()
					sd.calls = nil
					if rc := runUpdateWith([]string{"--values", vp}, deps, &out, &errb); rc != 0 {
						t.Fatalf("run %d rc=%d stderr=%s", run, rc, errb.String())
					}
					if run == 1 && state == "converged" && len(sd.calls) != 0 {
						t.Fatalf("converged host mutated: %v", sd.calls)
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

func TestUpdateExitCodes(t *testing.T) {
	vp := writeValues(t, updateValues)
	bad := writeValues(t, "schema_version: 1\nnope: true\n")
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
		{"no privilege", []string{"--values", vp}, false, nil, 3},
		{"readyz failure", []string{"--values", vp}, true, errors.New("503"), 5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			deps, _, _ := newDeps(c.root, c.probe)
			var out, errb bytes.Buffer
			if rc := runUpdateWith(c.args, deps, &out, &errb); rc != c.want {
				t.Fatalf("rc=%d want %d stderr=%s", rc, c.want, errb.String())
			}
		})
	}
}

func TestUpdateRegisteredInMain(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil || !strings.Contains(string(src), `os.Args[1] == "update"`) {
		t.Fatalf("update dispatch missing from main.go: %v", err)
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
