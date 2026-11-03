package update

import (
	"fmt"
	"io/fs"
	"sort"
	"time"
)

// memFS is an in-memory FS. No test touches the real filesystem.
type memFS struct {
	files map[string][]byte
	log   []string
	// failWrite makes WriteAtomic fail for the given path.
	failWrite string
}

func newMemFS() *memFS { return &memFS{files: map[string][]byte{}} }

func (m *memFS) ReadFile(p string) ([]byte, error) {
	b, ok := m.files[p]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: p, Err: fs.ErrNotExist}
	}
	return append([]byte(nil), b...), nil
}

func (m *memFS) WriteAtomic(p string, data []byte, _ uint32) error {
	if p == m.failWrite {
		return fmt.Errorf("disk full")
	}
	m.files[p] = append([]byte(nil), data...)
	m.log = append(m.log, "write "+p)
	return nil
}

func (m *memFS) Rename(o, n string) error {
	b, ok := m.files[o]
	if !ok {
		return &fs.PathError{Op: "rename", Path: o, Err: fs.ErrNotExist}
	}
	m.files[n] = b
	delete(m.files, o)
	m.log = append(m.log, "rename "+o+" "+n)
	return nil
}

func (m *memFS) Remove(p string) error {
	if _, ok := m.files[p]; !ok {
		return &fs.PathError{Op: "remove", Path: p, Err: fs.ErrNotExist}
	}
	delete(m.files, p)
	return nil
}

func (m *memFS) paths() []string {
	var out []string
	for k := range m.files {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// fakeSystemd records calls and models unit state.
type fakeSystemd struct {
	active, enabled map[string]bool
	calls           []string
	failOn          string
}

func newFakeSystemd() *fakeSystemd {
	return &fakeSystemd{active: map[string]bool{}, enabled: map[string]bool{}}
}

func (f *fakeSystemd) rec(c string) error {
	f.calls = append(f.calls, c)
	if c == f.failOn {
		return fmt.Errorf("boom")
	}
	return nil
}
func (f *fakeSystemd) DaemonReload() error { return f.rec("daemon-reload") }
func (f *fakeSystemd) IsActive(u string) (bool, error) {
	return f.active[u], nil
}
func (f *fakeSystemd) IsEnabled(u string) (bool, error) {
	return f.enabled[u], nil
}
func (f *fakeSystemd) Enable(u string) error {
	f.enabled[u] = true
	return f.rec("enable " + u)
}
func (f *fakeSystemd) Disable(u string) error {
	f.enabled[u] = false
	return f.rec("disable " + u)
}
func (f *fakeSystemd) Stop(u string) error {
	f.active[u] = false
	return f.rec("stop " + u)
}
func (f *fakeSystemd) Restart(u string) error {
	f.active[u] = true
	return f.rec("restart " + u)
}

type fakeProber struct {
	failures int // leading failures before success; -1 = always fail
	calls    int
	token    string
}

func (p *fakeProber) Ready(_, token string) error {
	p.calls++
	p.token = token
	if p.failures < 0 || p.calls <= p.failures {
		return fmt.Errorf("status 503")
	}
	return nil
}

type fakeBuilder struct {
	fs      *memFS
	content string
	err     error
	built   int
	srcDirs []string
	// revisions records the revision stamped into each build.
	revisions []string
}

func (b *fakeBuilder) Build(src, _, out, revision string) error {
	b.built++
	b.revisions = append(b.revisions, revision)
	b.srcDirs = append(b.srcDirs, src)
	if b.err != nil {
		return b.err
	}
	b.fs.files[out] = []byte(b.content)
	return nil
}

// fakeSource models source_dir: a checkout on a feature branch with a dirty
// tracked file, whose reviewed ref lives only in the object store.
type fakeSource struct {
	err       error
	commit    string
	calls     []string // "srcDir@ref"
	urls      []string
	cleanups  int
	exportDir string
}

func (f *fakeSource) Export(src, fetchURL, ref string) (*Export, error) {
	f.calls = append(f.calls, src+"@"+ref)
	f.urls = append(f.urls, fetchURL)
	if f.err != nil {
		return nil, f.err
	}
	return &Export{Dir: f.exportDir, Commit: f.commit, Cleanup: func() { f.cleanups++ }}, nil
}

type fakeHost struct{ root bool }

func (h fakeHost) IsPrivileged() bool { return h.root }

type rig struct {
	fs *memFS
	sd *fakeSystemd
	pr *fakeProber
	bd *fakeBuilder
	sc *fakeSource
	d  Deps
}

func newRig() *rig {
	m := newMemFS()
	r := &rig{fs: m, sd: newFakeSystemd(), pr: &fakeProber{}, bd: &fakeBuilder{fs: m, content: "binary-v1"},
		sc: &fakeSource{commit: "0123456789abcdef0123456789abcdef01234567", exportDir: "/private/export"}}
	r.d = Deps{FS: m, Systemd: r.sd, Prober: r.pr, Source: r.sc, Builder: r.bd, Host: fakeHost{root: true}, Sleep: func(time.Duration) {}}
	return r
}
