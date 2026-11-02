package update

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"
)

// Exit codes returned by the update subcommand. 0 covers both "already
// converged" and "converged after changes".
const (
	ExitOK        = 0
	ExitInvalid   = 2
	ExitPrivilege = 3
	ExitApply     = 4
	ExitReadyz    = 5
)

// Failure is a converge error carrying the process exit code.
type Failure struct {
	Code int
	Err  error
}

func (f *Failure) Error() string { return f.Err.Error() }
func (f *Failure) Unwrap() error { return f.Err }

func fail(code int, format string, a ...any) *Failure {
	return &Failure{Code: code, Err: fmt.Errorf(format, a...)}
}

// Result reports what a run changed. On failure it holds the changes made
// before the failing step; nothing is rolled back.
type Result struct {
	UnitChanged   bool
	BinaryChanged bool
	// Commit is the source commit the binary was built from; empty when the
	// run failed before the source was exported.
	Commit    string
	Enabled   bool
	Restarted bool
	Stopped   []string
	Disabled  []string
	Skipped   []string
}

// Changed reports whether the run altered the host.
func (r Result) Changed() bool {
	return r.UnitChanged || r.BinaryChanged || r.Enabled || r.Restarted ||
		len(r.Stopped) > 0 || len(r.Disabled) > 0
}

const stageSuffix = ".update-new"

// Converge brings the host in line with v. Order: validate, privilege check,
// export the reviewed source, stage the binary build (not live), unit, daemon-reload, binary swap,
// superseded units, enable, restart when needed, readiness.
func Converge(d Deps, v *Values) (res Result, err error) {
	if verr := v.Validate(); verr != nil {
		return res, &Failure{Code: ExitInvalid, Err: verr}
	}
	if !d.Host.IsPrivileged() {
		return res, fail(ExitPrivilege, "update must run as root: it writes the systemd unit and controls units")
	}

	var token string
	if v.Readyz.TokenFile != "" {
		raw, rerr := d.FS.ReadFile(v.Readyz.TokenFile)
		if rerr != nil {
			return res, fail(ExitInvalid, "readyz.token_file: %v", rerr)
		}
		token = strings.TrimSpace(string(raw))
	}

	unitText := v.RenderUnit()
	unitPath := v.UnitPath()
	existing, rerr := d.FS.ReadFile(unitPath)
	if rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
		return res, fail(ExitApply, "read unit %s: %v", unitPath, rerr)
	}
	unitChanged := rerr != nil || !bytes.Equal(existing, unitText)

	// Export the reviewed commit before anything is written. The build never
	// sees source_dir's working tree, so whatever branch or uncommitted state
	// a builder left there cannot reach a root-run binary.
	exp, xerr := d.Source.Export(v.Binary.SourceDir, v.Binary.SourceURL, v.Binary.SourceRef)
	if xerr != nil {
		code := ExitApply
		if errors.Is(xerr, ErrNotCheckout) {
			code = ExitInvalid
		}
		return res, fail(code, "source: %v", xerr)
	}
	defer exp.Cleanup()
	res.Commit = exp.Commit

	// Build into a staged path first so a build failure leaves the host
	// untouched. The staged file sits beside the install path so the final
	// rename stays on one filesystem.
	install := v.Binary.InstallPath
	staged := install + stageSuffix
	if berr := d.Builder.Build(exp.Dir, v.Package(), staged); berr != nil {
		_ = d.FS.Remove(staged)
		return res, fail(ExitApply, "build: %v", berr)
	}
	defer func() {
		if rmErr := d.FS.Remove(staged); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) && err == nil {
			err = fail(ExitApply, "remove staged binary %s: %v", staged, rmErr)
		}
	}()
	newBin, rerr := d.FS.ReadFile(staged)
	if rerr != nil {
		return res, fail(ExitApply, "read staged binary: %v", rerr)
	}
	oldBin, rerr := d.FS.ReadFile(install)
	if rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
		return res, fail(ExitApply, "read installed binary %s: %v", install, rerr)
	}
	binaryChanged := rerr != nil || sha256.Sum256(oldBin) != sha256.Sum256(newBin)

	if unitChanged {
		if werr := d.FS.WriteAtomic(unitPath, unitText, 0o644); werr != nil {
			return res, fail(ExitApply, "write unit %s: %v", unitPath, werr)
		}
		res.UnitChanged = true
		if cerr := d.Systemd.DaemonReload(); cerr != nil {
			return res, fail(ExitApply, "daemon-reload: %v", cerr)
		}
	}

	if binaryChanged {
		if merr := d.FS.Rename(staged, install); merr != nil {
			return res, fail(ExitApply, "install binary %s: %v", install, merr)
		}
		res.BinaryChanged = true
	}

	own := v.UnitName()
	for _, u := range v.Supersedes {
		if u == own {
			res.Skipped = append(res.Skipped, u)
			continue
		}
		active, aerr := d.Systemd.IsActive(u)
		if aerr != nil {
			return res, fail(ExitApply, "is-active %s: %v", u, aerr)
		}
		if active {
			if serr := d.Systemd.Stop(u); serr != nil {
				return res, fail(ExitApply, "stop %s: %v", u, serr)
			}
			res.Stopped = append(res.Stopped, u)
		}
		enabled, eerr := d.Systemd.IsEnabled(u)
		if eerr != nil {
			return res, fail(ExitApply, "is-enabled %s: %v", u, eerr)
		}
		if enabled {
			if derr := d.Systemd.Disable(u); derr != nil {
				return res, fail(ExitApply, "disable %s: %v", u, derr)
			}
			res.Disabled = append(res.Disabled, u)
		}
	}

	enabled, eerr := d.Systemd.IsEnabled(own)
	if eerr != nil {
		return res, fail(ExitApply, "is-enabled %s: %v", own, eerr)
	}
	if !enabled {
		if xerr := d.Systemd.Enable(own); xerr != nil {
			return res, fail(ExitApply, "enable %s: %v", own, xerr)
		}
		res.Enabled = true
	}

	active, aerr := d.Systemd.IsActive(own)
	if aerr != nil {
		return res, fail(ExitApply, "is-active %s: %v", own, aerr)
	}
	if res.UnitChanged || res.BinaryChanged || !active {
		if rerr := d.Systemd.Restart(own); rerr != nil {
			return res, fail(ExitApply, "restart %s: %v", own, rerr)
		}
		res.Restarted = true
	}

	if perr := waitReady(d, v, token); perr != nil {
		return res, fail(ExitReadyz, "%v (inspect with: journalctl -u %s)", perr, own)
	}
	return res, nil
}

func waitReady(d Deps, v *Values, token string) error {
	url := v.ReadyzURL()
	attempts := int(v.ReadyzTimeout()/defaultReadyzInterval) + 1
	var last error
	for i := 0; i < attempts; i++ {
		if last = d.Prober.Ready(url, token); last == nil {
			return nil
		}
		if i < attempts-1 {
			d.Sleep(defaultReadyzInterval)
		}
	}
	return fmt.Errorf("readyz %s not ready after %s: %w", url, v.ReadyzTimeout().Round(time.Second), last)
}
