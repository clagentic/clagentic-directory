package update

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// DefaultValuesRel is the default values file, relative to the user config
// root (clagentic CLI naming standard: <config root>/clagentic/directory/).
var DefaultValuesRel = filepath.Join("clagentic", "directory", "update-values.yaml")

// DefaultValuesPath resolves the values file used when --values is omitted:
// $XDG_CONFIG_HOME, else $HOME/.config, joined with DefaultValuesRel. It is
// os.UserConfigDir's rule with an injectable environment; there is no other
// fallback. A relative or unset root is an error rather than a guess.
func DefaultValuesPath(getenv func(string) string) (string, error) {
	root := getenv("XDG_CONFIG_HOME")
	if root == "" {
		home := getenv("HOME")
		if home == "" {
			return "", errors.New("cannot resolve default values path: neither $XDG_CONFIG_HOME nor $HOME is set")
		}
		root = filepath.Join(home, ".config")
	}
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("cannot resolve default values path: config root %q is not absolute", root)
	}
	return filepath.Join(root, DefaultValuesRel), nil
}

// readValuesFile reads path after checking it is a regular file (not a
// symlink), owned by euid, and neither group- nor world-writable. The checks
// run on the opened descriptor so the file read is the file checked.
func readValuesFile(path string, euid uint32) ([]byte, error) {
	li, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("read values %s: %w", path, err)
	}
	if li.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("values file %s: is a symlink; must be a regular file", path)
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("read values %s: %w", path, err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("read values %s: %w", path, err)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("values file %s: not a regular file", path)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, fmt.Errorf("values file %s: cannot determine owner on this platform", path)
	}
	if st.Uid != euid {
		return nil, fmt.Errorf("values file %s: owned by uid %d, must be owned by the running uid %d", path, st.Uid, euid)
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return nil, fmt.Errorf("values file %s: mode %04o is group- or world-writable; must not be", path, fi.Mode().Perm())
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("read values %s: %w", path, err)
	}
	return data, nil
}
