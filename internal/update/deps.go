package update

import "time"

// FS is the filesystem surface the converge logic touches. Not-exist errors
// must satisfy errors.Is(err, fs.ErrNotExist).
type FS interface {
	ReadFile(path string) ([]byte, error)
	// WriteAtomic writes data to a temp file beside path and renames it in.
	WriteAtomic(path string, data []byte, perm uint32) error
	Rename(oldPath, newPath string) error
	Remove(path string) error
}

// Systemd is the systemctl surface. Stop and Disable on an already-stopped or
// disabled unit are not called: callers check IsActive / IsEnabled first.
type Systemd interface {
	DaemonReload() error
	IsActive(unit string) (bool, error)
	IsEnabled(unit string) (bool, error)
	Enable(unit string) error
	Disable(unit string) error
	Stop(unit string) error
	// Restart restarts the unit, starting it if it is not running.
	Restart(unit string) error
}

// Prober performs one readiness attempt against url, returning nil when ready.
type Prober interface {
	Ready(url, token string) error
}

// Builder compiles pkg from srcDir into outPath, stamping revision (the
// resolved source commit) into the binary.
type Builder interface {
	Build(srcDir, pkg, outPath, revision string) error
}

// Source produces a private export of reviewed source. It never reads or
// modifies the working tree, branch or index of srcDir.
type Source interface {
	// Export fetches ref (the remote default branch when empty) from fetchURL,
	// or from srcDir's origin when fetchURL is empty, and exports that exact
	// commit into a fresh private directory.
	Export(srcDir, fetchURL, ref string) (*Export, error)
}

// Export is a private tree holding one commit's files.
type Export struct {
	Dir    string
	Commit string
	// Cleanup removes Dir. Always safe to call.
	Cleanup func()
}

// Host answers questions about the executing process.
type Host interface {
	IsPrivileged() bool
}

// Deps bundles every side-effecting collaborator.
type Deps struct {
	FS      FS
	Systemd Systemd
	Prober  Prober
	Source  Source
	Builder Builder
	Host    Host
	Sleep   func(time.Duration)
}
