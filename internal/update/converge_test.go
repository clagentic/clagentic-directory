package update

import (
	"errors"
	"strings"
	"testing"
)

func testValues(t *testing.T) *Values {
	t.Helper()
	v, err := ParseValues([]byte(gitValuesYAML))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

const gitValuesYAML = `
schema_version: 1
unit:
  dir: /units
  user: svc
  env_file: /etc/dir.env
binary:
  install_path: /bin/clagentic-directory
  source_dir: /src
daemon:
  registry_source: git
  registry_git_url: git@git.example.com:org/reg.git
  registry_git_ref: main
  registry_git_vocab: vocabulary.v1.yaml
  registry_git_poll: 60s
  registry_cache_dir: /cache
supersedes:
  - sync.timer
  - sync.service
`

// seedConverged makes the rig look like a host already converged to v.
func seedConverged(r *rig, v *Values) {
	r.fs.files[v.UnitPath()] = v.RenderUnit()
	r.fs.files[v.Binary.InstallPath] = []byte(r.bd.content)
	r.sd.active[v.UnitName()] = true
	r.sd.enabled[v.UnitName()] = true
}

func code(err error) int {
	var f *Failure
	if errors.As(err, &f) {
		return f.Code
	}
	return -1
}

func TestConvergedHostIsNoOp(t *testing.T) {
	v := testValues(t)
	r := newRig()
	seedConverged(r, v)
	res, err := Converge(r.d, v)
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed() {
		t.Fatalf("expected no changes, got %+v", res)
	}
	if len(r.sd.calls) != 0 || len(r.fs.log) != 0 {
		t.Fatalf("no-op run mutated host: systemd=%v fs=%v", r.sd.calls, r.fs.log)
	}
	if got := r.fs.paths(); len(got) != 2 {
		t.Fatalf("staged file left behind: %v", got)
	}
}

func TestNotInstalledHostIsInstalled(t *testing.T) {
	v := testValues(t)
	r := newRig()
	res, err := Converge(r.d, v)
	if err != nil {
		t.Fatal(err)
	}
	if !res.UnitChanged || !res.BinaryChanged || !res.Enabled || !res.Restarted {
		t.Fatalf("expected full install, got %+v", res)
	}
	if string(r.fs.files[v.UnitPath()]) != string(v.RenderUnit()) {
		t.Fatal("installed unit does not match rendered values")
	}
	if string(r.fs.files[v.Binary.InstallPath]) != "binary-v1" {
		t.Fatal("binary not installed")
	}
	want := "daemon-reload,enable clagentic-directory.service,restart clagentic-directory.service"
	if got := strings.Join(r.sd.calls, ","); got != want {
		t.Fatalf("systemd calls = %s, want %s", got, want)
	}
}

func TestChangedUnitRestartsAndReloads(t *testing.T) {
	v := testValues(t)
	r := newRig()
	seedConverged(r, v)
	r.fs.files[v.UnitPath()] = []byte("stale unit")
	res, err := Converge(r.d, v)
	if err != nil {
		t.Fatal(err)
	}
	if !res.UnitChanged || res.BinaryChanged || !res.Restarted || res.Enabled {
		t.Fatalf("unexpected result %+v", res)
	}
	if got := strings.Join(r.sd.calls, ","); got != "daemon-reload,restart clagentic-directory.service" {
		t.Fatalf("calls = %s", got)
	}
}

func TestChangedBinaryRestartsWithoutReload(t *testing.T) {
	v := testValues(t)
	r := newRig()
	seedConverged(r, v)
	r.bd.content = "binary-v2"
	res, err := Converge(r.d, v)
	if err != nil {
		t.Fatal(err)
	}
	if res.UnitChanged || !res.BinaryChanged || !res.Restarted {
		t.Fatalf("unexpected result %+v", res)
	}
	if got := strings.Join(r.sd.calls, ","); got != "restart clagentic-directory.service" {
		t.Fatalf("calls = %s", got)
	}
	if string(r.fs.files[v.Binary.InstallPath]) != "binary-v2" {
		t.Fatal("binary not replaced")
	}
}

func TestSecondRunMakesZeroChanges(t *testing.T) {
	v := testValues(t)
	r := newRig()
	r.sd.active["sync.timer"], r.sd.enabled["sync.timer"] = true, true
	if _, err := Converge(r.d, v); err != nil {
		t.Fatal(err)
	}
	r.sd.calls, r.fs.log = nil, nil
	res, err := Converge(r.d, v)
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed() || len(r.sd.calls) != 0 || len(r.fs.log) != 0 {
		t.Fatalf("second run changed things: %+v systemd=%v fs=%v", res, r.sd.calls, r.fs.log)
	}
}

func TestInactiveUnitIsStartedEvenWhenConverged(t *testing.T) {
	v := testValues(t)
	r := newRig()
	seedConverged(r, v)
	r.sd.active[v.UnitName()] = false
	res, err := Converge(r.d, v)
	if err != nil || !res.Restarted || res.UnitChanged || res.BinaryChanged {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestSupersededUnitsStoppedDisabledOnlyWhenNeeded(t *testing.T) {
	v := testValues(t)
	r := newRig()
	seedConverged(r, v)
	r.sd.active["sync.timer"], r.sd.enabled["sync.timer"] = true, true
	r.sd.enabled["sync.service"] = true // enabled but not running
	res, err := Converge(r.d, v)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Stopped, ",") != "sync.timer" || strings.Join(res.Disabled, ",") != "sync.timer,sync.service" {
		t.Fatalf("stopped=%v disabled=%v", res.Stopped, res.Disabled)
	}
	// already stopped and disabled: no calls at all
	r.sd.calls = nil
	if _, err := Converge(r.d, v); err != nil {
		t.Fatal(err)
	}
	if len(r.sd.calls) != 0 {
		t.Fatalf("expected no-op, got %v", r.sd.calls)
	}
}

func TestOwnUnitNeverStoppedOrDisabled(t *testing.T) {
	v := testValues(t)
	v.Supersedes = append(v.Supersedes, v.UnitName())
	r := newRig()
	seedConverged(r, v)
	res, err := Converge(r.d, v)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Skipped) != 1 || res.Changed() {
		t.Fatalf("res=%+v", res)
	}
	for _, c := range r.sd.calls {
		if strings.HasPrefix(c, "stop") || strings.HasPrefix(c, "disable") {
			t.Fatalf("own unit touched: %s", c)
		}
	}
}

func TestStepOrder(t *testing.T) {
	v := testValues(t)
	r := newRig()
	r.sd.active["sync.timer"], r.sd.enabled["sync.timer"] = true, true
	if _, err := Converge(r.d, v); err != nil {
		t.Fatal(err)
	}
	want := "daemon-reload,stop sync.timer,disable sync.timer,enable clagentic-directory.service,restart clagentic-directory.service"
	if got := strings.Join(r.sd.calls, ","); got != want {
		t.Fatalf("order = %s", got)
	}
	// unit written before the binary is renamed into place
	if len(r.fs.log) != 2 || !strings.HasPrefix(r.fs.log[0], "write /units/") || !strings.HasPrefix(r.fs.log[1], "rename") {
		t.Fatalf("fs order = %v", r.fs.log)
	}
}

func TestInsufficientPrivilegeWritesNothing(t *testing.T) {
	v := testValues(t)
	r := newRig()
	r.d.Host = fakeHost{root: false}
	_, err := Converge(r.d, v)
	if code(err) != ExitPrivilege {
		t.Fatalf("code = %d err=%v", code(err), err)
	}
	if len(r.fs.files) != 0 || len(r.sd.calls) != 0 || r.bd.built != 0 {
		t.Fatal("host touched without privilege")
	}
}

func TestInvalidValuesWriteNothing(t *testing.T) {
	v := testValues(t)
	v.Daemon.LogLevel = "loud"
	r := newRig()
	_, err := Converge(r.d, v)
	if code(err) != ExitInvalid {
		t.Fatalf("code = %d", code(err))
	}
	if len(r.fs.files) != 0 || len(r.sd.calls) != 0 || r.bd.built != 0 {
		t.Fatal("host touched with invalid values")
	}
}

func TestMissingTokenFileIsInvalidBeforeAnyWrite(t *testing.T) {
	v := testValues(t)
	v.Readyz.TokenFile = "/missing/token"
	r := newRig()
	_, err := Converge(r.d, v)
	if code(err) != ExitInvalid || len(r.fs.files) != 0 || r.bd.built != 0 {
		t.Fatalf("code=%d files=%v", code(err), r.fs.paths())
	}
}

func TestBuildFailureLeavesHostUntouched(t *testing.T) {
	v := testValues(t)
	r := newRig()
	seedConverged(r, v)
	r.fs.files[v.UnitPath()] = []byte("stale unit") // would be rewritten if the build had not failed first
	r.bd.err = errors.New("compile error")
	_, err := Converge(r.d, v)
	if code(err) != ExitApply {
		t.Fatalf("code = %d", code(err))
	}
	if string(r.fs.files[v.UnitPath()]) != "stale unit" || len(r.sd.calls) != 0 {
		t.Fatal("unit or systemd touched after build failure")
	}
	if string(r.fs.files[v.Binary.InstallPath]) != "binary-v1" {
		t.Fatal("installed binary altered")
	}
}

func TestApplyFailureReportsPartialChanges(t *testing.T) {
	v := testValues(t)
	r := newRig()
	seedConverged(r, v)
	r.sd.active["sync.timer"], r.sd.enabled["sync.timer"] = true, true
	r.sd.failOn = "disable sync.timer"
	r.bd.content = "binary-v2"
	res, err := Converge(r.d, v)
	if code(err) != ExitApply {
		t.Fatalf("code = %d", code(err))
	}
	if !res.BinaryChanged || len(res.Stopped) != 1 || res.Restarted {
		t.Fatalf("partial result not reported: %+v", res)
	}
}

func TestUnitWriteFailureStopsBeforeReload(t *testing.T) {
	v := testValues(t)
	r := newRig()
	r.fs.failWrite = v.UnitPath()
	_, err := Converge(r.d, v)
	if code(err) != ExitApply || len(r.sd.calls) != 0 {
		t.Fatalf("code=%d calls=%v", code(err), r.sd.calls)
	}
}

func TestReadyzFailureExitsWithJournalHint(t *testing.T) {
	v := testValues(t)
	r := newRig()
	r.pr.failures = -1
	res, err := Converge(r.d, v)
	if code(err) != ExitReadyz {
		t.Fatalf("code = %d", code(err))
	}
	if !strings.Contains(err.Error(), "journalctl -u clagentic-directory.service") {
		t.Fatalf("no journal hint: %v", err)
	}
	if !res.UnitChanged || !res.Restarted {
		t.Fatalf("changes not reported on readyz failure: %+v", res)
	}
	if r.pr.calls != int(v.ReadyzTimeout().Seconds())+1 {
		t.Fatalf("probe attempts = %d", r.pr.calls)
	}
}

func TestReadyzRetriesThenSucceedsWithToken(t *testing.T) {
	v := testValues(t)
	v.Readyz.TokenFile = "/tok"
	r := newRig()
	r.fs.files["/tok"] = []byte("s3cret\n")
	r.pr.failures = 3
	if _, err := Converge(r.d, v); err != nil {
		t.Fatal(err)
	}
	if r.pr.calls != 4 || r.pr.token != "s3cret" {
		t.Fatalf("calls=%d token=%q", r.pr.calls, r.pr.token)
	}
}
