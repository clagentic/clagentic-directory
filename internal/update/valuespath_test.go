package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultValuesPath(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	cases := []struct {
		name    string
		env     map[string]string
		want    string
		wantErr bool
	}{
		{"XDG wins", map[string]string{"XDG_CONFIG_HOME": "/x", "HOME": "/h"}, "/x/clagentic/directory/update-values.yaml", false},
		{"HOME fallback", map[string]string{"HOME": "/h"}, "/h/.config/clagentic/directory/update-values.yaml", false},
		{"neither", map[string]string{}, "", true},
		{"relative XDG", map[string]string{"XDG_CONFIG_HOME": "rel"}, "", true},
	}
	for _, c := range cases {
		got, err := DefaultValuesPath(env(c.env))
		if (err != nil) != c.wantErr || got != c.want {
			t.Fatalf("%s: got %q, %v", c.name, got, err)
		}
	}
}

func TestValuesFileChecks(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	euid := uint32(os.Geteuid())
	cases := []struct {
		name string
		path string
		euid uint32
		want string
	}{
		{"ok 0600", write("a", 0o600), euid, ""},
		{"ok 0644", write("b", 0o644), euid, ""},
		{"group writable", write("c", 0o660), euid, "group- or world-writable"},
		{"world writable", write("d", 0o602), euid, "group- or world-writable"},
		{"wrong owner", write("e", 0o600), euid + 1, "owned by uid"},
		{"directory", dir, euid, "not a regular file"},
		{"missing", filepath.Join(dir, "absent"), euid, "absent"},
	}
	for _, c := range cases {
		_, err := readValuesFile(c.path, c.euid)
		switch {
		case c.want == "" && err != nil:
			t.Fatalf("%s: %v", c.name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), c.path)):
			t.Fatalf("%s: err=%v, want it to name the path and %q", c.name, err, c.want)
		}
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(filepath.Join(dir, "a"), link); err != nil {
		t.Fatal(err)
	}
	if _, err := readValuesFile(link, euid); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink: err=%v", err)
	}
}
