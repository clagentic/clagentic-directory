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

// newerCommit is a resolved commit different from the rig's default.
const newerCommit = "fedcba9876543210fedcba9876543210fedcba98"

// seedConverged makes the rig look like a host already converged to v.
func seedConverged(r *rig, v *Values) {
	r.fs.files[v.UnitPath()] = v.RenderUnit()
	r.fs.files[v.Binary.InstallPath] = []byte(r.bd.content)
	r.fs.files[v.Binary.InstallPath+recordSuffix] = installRecord{
		Commit: r.sc.commit, Package: v.Package(), SHA256: digest([]byte(r.bd.content)),
	}.render()
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
	if got := r.fs.paths(); len(got) != 3 {
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
	r.sc.commit = newerCommit
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
	// unit written before the binary is renamed into place; the install record
	// goes last so a crash before it leaves a record that forces a rebuild.
	if len(r.fs.log) != 3 || !strings.HasPrefix(r.fs.log[0], "write /units/") || !strings.HasPrefix(r.fs.log[1], "rename") ||
		r.fs.log[2] != "write /bin/clagentic-directory.source" {
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
	r.sc.commit = newerCommit
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
	r.sc.commit = newerCommit
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

func TestRecordWriteFailureStillRestartsAndFailsLoudly(t *testing.T) {
	v := testValues(t)
	r := newRig()
	seedConverged(r, v)
	r.bd.content = "binary-v2"
	r.sc.commit = newerCommit
	r.fs.failWrite = v.Binary.InstallPath + recordSuffix
	res, err := Converge(r.d, v)
	if code(err) != ExitApply || !strings.Contains(err.Error(), "install record") {
		t.Fatalf("want loud ExitApply on record write, got %v", err)
	}
	if !res.BinaryChanged || !res.Restarted {
		t.Fatalf("binary swapped but restart lost: %+v", res)
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

func TestBuildsExportedCommitNotSourceDir(t *testing.T) {
	v := testValues(t)
	v.Binary.SourceRef = "release"
	r := newRig()
	res, err := Converge(r.d, v)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.sc.calls) != 1 || r.sc.calls[0] != "/src@release" {
		t.Fatalf("export calls = %v", r.sc.calls)
	}
	// source_dir holds a feature branch and a dirty tracked file; the build
	// input must be the export alone.
	if len(r.bd.srcDirs) != 1 || r.bd.srcDirs[0] != "/private/export" {
		t.Fatalf("built from %v, want only the private export", r.bd.srcDirs)
	}
	if res.Commit != r.sc.commit {
		t.Fatalf("commit = %q", res.Commit)
	}
	if r.sc.cleanups != 1 {
		t.Fatalf("export cleanups = %d", r.sc.cleanups)
	}
}

func TestEmptySourceRefMeansRemoteDefault(t *testing.T) {
	v := testValues(t)
	r := newRig()
	if _, err := Converge(r.d, v); err != nil {
		t.Fatal(err)
	}
	if r.sc.calls[0] != "/src@" {
		t.Fatalf("export calls = %v", r.sc.calls)
	}
}

func TestSourceFailuresWriteNothing(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"unknown ref", errors.New("git fetch: exit status 128: couldn't find remote ref nope"), ExitApply},
		{"fetch failure", errors.New("git fetch: exit status 128: unable to access remote"), ExitApply},
		{"not a checkout", ErrNotCheckout, ExitInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := testValues(t)
			r := newRig()
			seedConverged(r, v)
			r.fs.files[v.UnitPath()] = []byte("stale unit")
			r.bd.content = "binary-v2"
			r.sc.err = c.err
			before := r.fs.paths()
			res, err := Converge(r.d, v)
			if code(err) != c.want {
				t.Fatalf("code = %d err=%v", code(err), err)
			}
			if r.bd.built != 0 || len(r.fs.log) != 0 || len(r.sd.calls) != 0 {
				t.Fatalf("host touched: built=%d fs=%v systemd=%v", r.bd.built, r.fs.log, r.sd.calls)
			}
			if strings.Join(r.fs.paths(), ",") != strings.Join(before, ",") || res.Changed() || res.Commit != "" {
				t.Fatalf("files=%v res=%+v", r.fs.paths(), res)
			}
		})
	}
}

func TestInvalidPackageIsRejected(t *testing.T) {
	for _, pkg := range []string{"-toolexec=x", "a b", "a\nb"} {
		v := testValues(t)
		v.Binary.Package = pkg
		if err := v.Validate(); err == nil || !strings.Contains(err.Error(), "binary.package") {
			t.Fatalf("package %q accepted: %v", pkg, err)
		}
	}
}

func TestInvalidSourceRefIsRejected(t *testing.T) {
	for _, ref := range []string{"-upload-pack=x", "a b", "a\nb"} {
		v := testValues(t)
		v.Binary.SourceRef = ref
		if err := v.Validate(); err == nil || !strings.Contains(err.Error(), "binary.source_ref") {
			t.Fatalf("ref %q accepted: %v", ref, err)
		}
	}
	v := testValues(t)
	v.Binary.SourceRef = "refs/heads/release/1.x"
	if err := v.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestSourceURLIsPassedToSource(t *testing.T) {
	v := testValues(t)
	v.Binary.SourceURL = "https://git.example.invalid/reviewed.git"
	r := newRig()
	if _, err := Converge(r.d, v); err != nil {
		t.Fatal(err)
	}
	if len(r.sc.urls) != 1 || r.sc.urls[0] != v.Binary.SourceURL {
		t.Fatalf("urls = %v", r.sc.urls)
	}
}

func TestInvalidSourceURLExitsInvalidWithoutHostWrites(t *testing.T) {
	for _, u := range []string{"file:///srv/evil", "ext::sh -c id", "/srv/evil", "git@host.example:org/r.git", "http://h.example/r"} {
		v := testValues(t)
		v.Binary.SourceURL = u
		if err := v.Validate(); err == nil || !strings.Contains(err.Error(), "binary.source_url") || strings.Contains(err.Error(), "evil") {
			t.Fatalf("url %q: %v", u, err)
		}
		r := newRig()
		seedConverged(r, v)
		r.fs.files[v.UnitPath()] = []byte("stale unit")
		before := r.fs.paths()
		_, err := Converge(r.d, v)
		if code(err) != ExitInvalid {
			t.Fatalf("url %q: code = %d err=%v", u, code(err), err)
		}
		if len(r.sc.calls) != 0 || r.bd.built != 0 || len(r.fs.log) != 0 || len(r.sd.calls) != 0 ||
			strings.Join(r.fs.paths(), ",") != strings.Join(before, ",") {
			t.Fatalf("host touched for %q: fs=%v systemd=%v", u, r.fs.log, r.sd.calls)
		}
	}
	v := testValues(t)
	v.Binary.SourceURL = "ssh://git@host.example/org/r.git"
	if err := v.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestFileOriginFallbackExitsApplyWithoutHostWrites(t *testing.T) {
	v := testValues(t)
	r := newRig()
	seedConverged(r, v)
	r.fs.files[v.UnitPath()] = []byte("stale unit")
	before := r.fs.paths()
	gitCalls := 0
	r.d.Source = GitSource{Run: func(_ string, args ...string) (string, error) {
		gitCalls++
		return "file:///srv/evil", nil
	}}
	_, err := Converge(r.d, v)
	if code(err) != ExitApply {
		t.Fatalf("code = %d err=%v", code(err), err)
	}
	if strings.Contains(err.Error(), "evil") {
		t.Fatalf("error echoes the URL: %v", err)
	}
	if gitCalls != 1 || r.bd.built != 0 || len(r.fs.log) != 0 || len(r.sd.calls) != 0 ||
		strings.Join(r.fs.paths(), ",") != strings.Join(before, ",") {
		t.Fatalf("git=%d built=%d fs=%v systemd=%v", gitCalls, r.bd.built, r.fs.log, r.sd.calls)
	}
}

func TestSecondRunSameCommitDoesNotRebuildOrRestart(t *testing.T) {
	v := testValues(t)
	r := newRig()
	if _, err := Converge(r.d, v); err != nil {
		t.Fatal(err)
	}
	if r.bd.built != 1 || len(r.bd.revisions) != 1 || r.bd.revisions[0] != r.sc.commit {
		t.Fatalf("first run builds=%d revisions=%v, want one build stamped with %s", r.bd.built, r.bd.revisions, r.sc.commit)
	}
	r.sd.calls = nil
	r.fs.log = nil
	// A rebuild from a fresh export would not be byte-identical.
	r.bd.content = "binary-v1-different-bytes"
	res, err := Converge(r.d, v)
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed() || res.Built {
		t.Fatalf("second run changed the host: %+v", res)
	}
	if r.bd.built != 1 {
		t.Fatalf("rebuilt an installed commit: builds=%d", r.bd.built)
	}
	if len(r.sd.calls) != 0 || len(r.fs.log) != 0 {
		t.Fatalf("second run mutated host: systemd=%v fs=%v", r.sd.calls, r.fs.log)
	}
	if string(r.fs.files[v.Binary.InstallPath]) != "binary-v1" {
		t.Fatal("installed binary replaced")
	}
}

func TestChangedCommitRebuildsAndRestarts(t *testing.T) {
	v := testValues(t)
	r := newRig()
	if _, err := Converge(r.d, v); err != nil {
		t.Fatal(err)
	}
	r.sd.calls = nil
	r.sc.commit = newerCommit
	r.bd.content = "binary-v2"
	res, err := Converge(r.d, v)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Built || !res.BinaryChanged || !res.Restarted {
		t.Fatalf("unexpected result %+v", res)
	}
	if got := strings.Join(r.sd.calls, ","); got != "restart clagentic-directory.service" {
		t.Fatalf("calls = %s", got)
	}
	if r.bd.revisions[len(r.bd.revisions)-1] != r.sc.commit {
		t.Fatalf("revisions = %v", r.bd.revisions)
	}
	// The new commit is now the recorded one.
	r.sd.calls = nil
	if res, err := Converge(r.d, v); err != nil || res.Changed() || res.Built {
		t.Fatalf("not converged after upgrade: %+v %v", res, err)
	}
}

func TestChangedUnitSameCommitUpdatesUnitWithoutRebuild(t *testing.T) {
	v := testValues(t)
	r := newRig()
	if _, err := Converge(r.d, v); err != nil {
		t.Fatal(err)
	}
	r.sd.calls = nil
	r.fs.files[v.UnitPath()] = []byte("stale unit")
	res, err := Converge(r.d, v)
	if err != nil {
		t.Fatal(err)
	}
	if !res.UnitChanged || res.BinaryChanged || res.Built || !res.Restarted {
		t.Fatalf("unexpected result %+v", res)
	}
	if r.bd.built != 1 {
		t.Fatalf("rebuilt for a unit-only change: builds=%d", r.bd.built)
	}
	if got := strings.Join(r.sd.calls, ","); got != "daemon-reload,restart clagentic-directory.service" {
		t.Fatalf("calls = %s", got)
	}
}

func TestSwappedBinaryIsRebuiltDespiteMatchingRecord(t *testing.T) {
	v := testValues(t)
	r := newRig()
	seedConverged(r, v)
	r.fs.files[v.Binary.InstallPath] = []byte("not what update installed")
	res, err := Converge(r.d, v)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Built || !res.BinaryChanged || !res.Restarted {
		t.Fatalf("unexpected result %+v", res)
	}
	if string(r.fs.files[v.Binary.InstallPath]) != "binary-v1" {
		t.Fatal("swapped binary not replaced")
	}
}

func TestChangedPackageSameCommitRebuilds(t *testing.T) {
	v := testValues(t)
	r := newRig()
	if _, err := Converge(r.d, v); err != nil {
		t.Fatal(err)
	}
	v.Binary.Package = "./cmd/other"
	r.bd.content = "binary-other"
	res, err := Converge(r.d, v)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Built || !res.BinaryChanged {
		t.Fatalf("unexpected result %+v", res)
	}
}

func TestInstallWithoutRecordRebuildsAndRecordsWithoutRestartWhenBytesMatch(t *testing.T) {
	v := testValues(t)
	r := newRig()
	seedConverged(r, v)
	delete(r.fs.files, v.Binary.InstallPath+recordSuffix)
	res, err := Converge(r.d, v)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Built || res.BinaryChanged || res.Restarted {
		t.Fatalf("unexpected result %+v", res)
	}
	if _, ok := r.fs.files[v.Binary.InstallPath+recordSuffix]; !ok {
		t.Fatal("record not written")
	}
	if res2, err := Converge(r.d, v); err != nil || res2.Built {
		t.Fatalf("second run rebuilt: %+v %v", res2, err)
	}
}
