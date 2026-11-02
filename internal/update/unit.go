package update

import (
	"fmt"
	"regexp"
	"strings"
)

var safeArg = regexp.MustCompile(`^[A-Za-z0-9_@+=:,./-]+$`)

// quoteArg quotes one ExecStart word using systemd's command-line rules:
// backslash and double-quote are escaped, and % and $ are doubled so systemd
// does not expand them. It must only be used for ExecStart words; directives
// that take a raw value (EnvironmentFile=) do not unquote. It panics on a
// control byte, which would let a value inject a new unit line; Validate
// rejects those first, so a panic means a value path skipped Validate.
func quoteArg(s string) string {
	if hasControl(s) {
		panic(fmt.Sprintf("update: refusing to render control character in unit value %q", s))
	}
	if safeArg.MatchString(s) {
		return s
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `%`, `%%`, `$`, `$$`)
	return `"` + r.Replace(s) + `"`
}

// rawPath returns a path for EnvironmentFile=. systemd does not unquote that
// value, treats a leading "-" as "optional file", and expands % specifiers in
// it. A leading "-" cannot occur because Validate requires an absolute path;
// whitespace, control bytes and % are refused rather than escaped.
func rawPath(s string) string {
	if hasControl(s) || strings.ContainsAny(s, " \t%") {
		panic(fmt.Sprintf("update: refusing to render whitespace, control character or %% in raw unit path %q", s))
	}
	return s
}

// DaemonArgs returns the daemon flags, one slice per flag, in a fixed order. Flags the values
// leave unset are omitted so the daemon's own defaults apply.
func (v *Values) DaemonArgs() [][]string {
	d := &v.Daemon
	var args [][]string
	str := func(flag, val string) {
		if val != "" {
			args = append(args, []string{"--" + flag, val})
		}
	}
	str("registry-source", d.RegistrySource)
	str("registry-dir", d.RegistryDir)
	str("vocab-file", d.VocabFile)
	str("registry-git-url", d.RegistryGitURL)
	str("registry-git-ref", d.RegistryGitRef)
	str("registry-git-subpath", d.RegistryGitSubpath)
	str("registry-git-vocab", d.RegistryGitVocab)
	str("registry-git-poll", d.RegistryGitPoll)
	str("registry-cache-dir", d.RegistryCacheDir)
	str("registry-secret-keyfile", d.RegistrySecretKeyfile)
	str("listen", d.Listen)
	str("log-level", d.LogLevel)
	str("self-build-base-dir", d.SelfBuildBaseDir)
	if d.SelfBuildMCPDiscovery {
		args = append(args, []string{"--self-build-mcp-discovery"})
	}
	str("self-build-source-watch-url", d.SelfBuildSourceWatchURL)
	str("self-build-source-watch-interval", d.SelfBuildSourceWatchInterval)
	str("self-build-source-watch-window", d.SelfBuildSourceWatchWindow)
	str("self-build-usage-event-store-url", d.SelfBuildUsageEventStoreURL)
	str("self-build-usage-window", d.SelfBuildUsageWindow)
	return args
}

// RenderUnit renders the systemd unit text. Output is deterministic so that
// a byte comparison against the installed unit detects drift.
func (v *Values) RenderUnit() []byte {
	var b strings.Builder
	b.WriteString("# Managed by `clagentic-directory update`; edits are overwritten on the next run.\n")
	b.WriteString("[Unit]\nDescription=clagentic-directory agent registry\nAfter=network.target\n\n")
	b.WriteString("[Service]\nType=simple\n")
	b.WriteString("User=" + v.Unit.User + "\n")
	b.WriteString("SyslogIdentifier=clagentic-directory\n")
	b.WriteString("ExecStart=" + quoteArg(v.Binary.InstallPath))
	for _, a := range v.DaemonArgs() {
		b.WriteString(" \\\n ")
		for _, w := range a {
			b.WriteString(" " + quoteArg(w))
		}
	}
	b.WriteString("\nRestart=on-failure\nRestartSec=5s\n")
	if v.Unit.EnvFile != "" {
		// No "-" prefix: a configured env file carries the auth token, so a
		// missing file must fail the unit start instead of running unauthenticated.
		b.WriteString("EnvironmentFile=" + rawPath(v.Unit.EnvFile) + "\n")
	}
	b.WriteString("\n[Install]\nWantedBy=multi-user.target\n")
	return []byte(b.String())
}
