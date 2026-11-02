package update

import (
	"os"
	"strings"
	"testing"

	"github.com/clagentic/clagentic-directory/internal/selfbuild"
)

func TestExampleValuesFileIsValid(t *testing.T) {
	data, err := os.ReadFile("../../examples/update-values.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseValues(data); err != nil {
		t.Fatalf("example values invalid: %v", err)
	}
}

func TestParseRejectsBadInput(t *testing.T) {
	cases := map[string]struct{ yaml, want string }{
		"unknown key":       {gitValuesYAML + "\nbogus: 1\n", "bogus"},
		"unknown nested":    {strings.Replace(gitValuesYAML, "user: svc", "user: svc\n  shell: x", 1), "shell"},
		"wrong version":     {strings.Replace(gitValuesYAML, "schema_version: 1", "schema_version: 2", 1), "schema_version"},
		"empty":             {"", "empty"},
		"relative path":     {strings.Replace(gitValuesYAML, "/cache", "cache", 1), "absolute"},
		"bad duration":      {strings.Replace(gitValuesYAML, "60s", "soon", 1), "duration"},
		"missing user":      {strings.Replace(gitValuesYAML, "user: svc", "", 1), "unit.user"},
		"git needs url":     {strings.Replace(gitValuesYAML, "registry_git_url: git@git.example.com:org/reg.git", "", 1), "registry_git_url"},
		"dir in git":        {strings.Replace(gitValuesYAML, "registry_git_ref: main", "registry_dir: /r", 1), "registry_dir"},
		"bad unit":          {strings.Replace(gitValuesYAML, "sync.timer", "../evil;rm", 1), "supersedes"},
		"bad source":        {strings.Replace(gitValuesYAML, "registry_source: git", "registry_source: s3", 1), "registry_source"},
		"selfbuild no base": {strings.Replace(gitValuesYAML, "registry_cache_dir: /cache", "registry_cache_dir: /cache\n  self_build_mcp_discovery: true", 1), "base_dir"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseValues([]byte(c.yaml))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want mention of %q", err, c.want)
			}
		})
	}
}

func TestFileModeRejectsGitKeysAndRequiresDir(t *testing.T) {
	y := `
schema_version: 1
unit: {dir: /u, user: svc}
binary: {install_path: /b, source_dir: /s}
daemon: {registry_source: file, registry_git_url: x}
`
	_, err := ParseValues([]byte(y))
	if err == nil || !strings.Contains(err.Error(), "registry_dir") || !strings.Contains(err.Error(), "registry_git_url") {
		t.Fatalf("err = %v", err)
	}
}

func TestSupersededNamesValidatedStrictly(t *testing.T) {
	for _, bad := range []string{"a b.timer", "x.timer\n", "-x.timer", "x", "x.sh", "a/b.service", `a\x2d.service`} {
		v := testValues(t)
		v.Supersedes = []string{bad}
		if err := v.Validate(); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	v := testValues(t)
	v.Supersedes = []string{"a-b_c.d@x.timer", "z.service"}
	if err := v.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestRenderUnitGitMode(t *testing.T) {
	got := string(testValues(t).RenderUnit())
	for _, want := range []string{
		"User=svc\n",
		"ExecStart=/bin/clagentic-directory \\\n  --registry-source git \\\n",
		"  --registry-git-url git@git.example.com:org/reg.git \\\n",
		"  --registry-git-poll 60s \\\n  --registry-cache-dir /cache\n",
		"EnvironmentFile=/etc/dir.env\n",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("unit missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "--vocab-file") || strings.Contains(got, "--listen") {
		t.Errorf("unset flags rendered:\n%s", got)
	}
}

func TestRenderUnitFileModeWithVocab(t *testing.T) {
	y := `
schema_version: 1
unit: {dir: /u, user: svc}
binary: {install_path: /b, source_dir: /s}
daemon: {registry_source: file, registry_dir: /r, vocab_file: /v.yaml, listen: ":9000"}
`
	v, err := ParseValues([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	got := string(v.RenderUnit())
	if !strings.Contains(got, "--vocab-file /v.yaml") || !strings.Contains(got, "--listen :9000") {
		t.Fatalf("unit:\n%s", got)
	}
}

func TestQuoteArg(t *testing.T) {
	cases := map[string]string{
		"plain/path-1.2": "plain/path-1.2",
		"has space":      `"has space"`,
		`q"uote`:         `"q\"uote"`,
		"100%":           `"100%%"`,
		"$HOME":          `"$$HOME"`,
		"":               `""`,
	}
	for in, want := range cases {
		if got := quoteArg(in); got != want {
			t.Errorf("quoteArg(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestReadyzURLDerivation(t *testing.T) {
	cases := map[string]string{
		"":              "http://127.0.0.1:8444/readyz",
		":9000":         "http://127.0.0.1:9000/readyz",
		"0.0.0.0:9000":  "http://127.0.0.1:9000/readyz",
		"[::1]:9000":    "http://[::1]:9000/readyz",
		"10.0.0.5:9000": "http://10.0.0.5:9000/readyz",
	}
	for listen, want := range cases {
		v := testValues(t)
		v.Daemon.Listen = listen
		if got := v.ReadyzURL(); got != want {
			t.Errorf("listen %q: got %s want %s", listen, got, want)
		}
	}
	v := testValues(t)
	v.Readyz.URL = "https://dir.example.com/readyz"
	if v.ReadyzURL() != "https://dir.example.com/readyz" {
		t.Fatal("override ignored")
	}
}

func TestValidateRejectsNonHTTPSelfBuildURL(t *testing.T) {
	cases := []struct{ mechanism, raw string }{
		{"source-watch", "file:///etc/passwd"},
		{"usage-event-store", "ftp://example.invalid/x"},
	}
	for _, c := range cases {
		v := testValues(t)
		v.Daemon.SelfBuildBaseDir = "/b"
		if c.mechanism == "source-watch" {
			v.Daemon.SelfBuildSourceWatchURL = c.raw
		} else {
			v.Daemon.SelfBuildUsageEventStoreURL = c.raw
		}
		err := v.Validate()
		if err == nil {
			t.Fatalf("%s: non-http(s) URL accepted", c.mechanism)
		}
		// The message must be the shared rule's own text, not a local copy.
		shared := selfbuild.ValidateSelfBuildURL(c.raw, c.mechanism)
		if shared == nil || !strings.Contains(err.Error(), shared.Error()) {
			t.Errorf("%s: err = %v, want it to contain %v", c.mechanism, err, shared)
		}
	}
}

func TestRenderUnitEnvironmentFileIsRaw(t *testing.T) {
	v := testValues(t)
	got := string(v.RenderUnit())
	if !strings.Contains(got, "\nEnvironmentFile=/etc/dir.env\n") {
		t.Fatalf("EnvironmentFile line wrong:\n%s", got)
	}
	if strings.Contains(got, "EnvironmentFile=-") || strings.Contains(got, `EnvironmentFile="`) {
		t.Fatalf("EnvironmentFile must be raw and required:\n%s", got)
	}
}

func TestValidateRejectsEnvFileWhitespace(t *testing.T) {
	for _, p := range []string{"/etc/my dir.env", "/etc/a\tb.env", "/etc/a\nb.env"} {
		v := testValues(t)
		v.Unit.EnvFile = p
		if err := v.Validate(); err == nil || !strings.Contains(err.Error(), "unit.env_file") {
			t.Errorf("env_file %q accepted: %v", p, err)
		}
	}
}

func TestQuoteArgPanicsOnControlBytes(t *testing.T) {
	for _, in := range []string{"a\nb", "a\rb", "a\x00b", "a\x7fb"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("quoteArg(%q) did not panic", in)
				}
			}()
			quoteArg(in)
		}()
	}
}

func TestRawPathRefusesUnsafeValues(t *testing.T) {
	for _, in := range []string{"/a b", "/a\tb", "/a\nb"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("rawPath(%q) did not panic", in)
				}
			}()
			rawPath(in)
		}()
	}
	if got := rawPath("/etc/x.env"); got != "/etc/x.env" {
		t.Fatalf("rawPath altered a clean path: %s", got)
	}
}
