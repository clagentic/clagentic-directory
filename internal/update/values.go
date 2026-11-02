// Package update converges a host's clagentic-directory install (systemd
// unit, binary, superseded units, readiness) from a versioned values file.
package update

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/clagentic/clagentic-directory/internal/selfbuild"
)

// SchemaVersion is the only values-file schema this build understands.
const SchemaVersion = 1

// DefaultUnitName is the tool's own unit name when unit.name is omitted.
const DefaultUnitName = "clagentic-directory.service"

const (
	defaultPackage        = "./cmd/clagentic-directory"
	defaultListen         = ":8444"
	defaultReadyzTimeout  = 30 * time.Second
	defaultReadyzInterval = time.Second
)

var (
	ownUnitPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*\.service$`)
	unitNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:_.@-]{0,200}\.(service|timer|socket|path)$`)
	userPattern     = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}\$?$`)
)

// Values is the deployment values file. Unknown keys are an error.
type Values struct {
	SchemaVersion int          `yaml:"schema_version"`
	Unit          UnitValues   `yaml:"unit"`
	Binary        BinaryValues `yaml:"binary"`
	Daemon        DaemonValues `yaml:"daemon"`
	Readyz        ReadyzValues `yaml:"readyz"`
	// Supersedes lists units this deployment replaces; update stops and
	// disables them. Names come only from this file.
	Supersedes []string `yaml:"supersedes"`
}

// UnitValues describes the rendered systemd unit.
type UnitValues struct {
	Name    string `yaml:"name"`
	Dir     string `yaml:"dir"`
	User    string `yaml:"user"`
	EnvFile string `yaml:"env_file"`
}

// BinaryValues describes how the binary is kept current: a rebuild from a
// private export of a reviewed ref, installed by atomic rename.
type BinaryValues struct {
	InstallPath string `yaml:"install_path"`
	SourceDir   string `yaml:"source_dir"`
	// SourceRef is the ref built from, fetched from the checkout's origin.
	// Empty means the remote's default branch.
	SourceRef string `yaml:"source_ref"`
	Package   string `yaml:"package"`
}

// DaemonValues maps one-to-one onto daemon flags. The bearer token flag is
// deliberately absent: pass it through unit.env_file.
type DaemonValues struct {
	RegistrySource        string `yaml:"registry_source"`
	RegistryDir           string `yaml:"registry_dir"`
	VocabFile             string `yaml:"vocab_file"`
	RegistryGitURL        string `yaml:"registry_git_url"`
	RegistryGitRef        string `yaml:"registry_git_ref"`
	RegistryGitSubpath    string `yaml:"registry_git_subpath"`
	RegistryGitVocab      string `yaml:"registry_git_vocab"`
	RegistryGitPoll       string `yaml:"registry_git_poll"`
	RegistryCacheDir      string `yaml:"registry_cache_dir"`
	RegistrySecretKeyfile string `yaml:"registry_secret_keyfile"`
	Listen                string `yaml:"listen"`
	LogLevel              string `yaml:"log_level"`

	SelfBuildBaseDir             string `yaml:"self_build_base_dir"`
	SelfBuildMCPDiscovery        bool   `yaml:"self_build_mcp_discovery"`
	SelfBuildSourceWatchURL      string `yaml:"self_build_source_watch_url"`
	SelfBuildSourceWatchInterval string `yaml:"self_build_source_watch_interval"`
	SelfBuildSourceWatchWindow   string `yaml:"self_build_source_watch_window"`
	SelfBuildUsageEventStoreURL  string `yaml:"self_build_usage_event_store_url"`
	SelfBuildUsageWindow         string `yaml:"self_build_usage_window"`
}

// ReadyzValues controls the post-converge readiness check.
type ReadyzValues struct {
	// URL overrides the probe URL derived from daemon.listen.
	URL string `yaml:"url"`
	// TokenFile holds the bearer token when the daemon enforces auth.
	TokenFile string `yaml:"token_file"`
	// Timeout is how long to wait for readiness (Go duration, default 30s).
	Timeout string `yaml:"timeout"`
}

// LoadValues reads, strictly decodes and validates the values file at path.
func LoadValues(path string) (*Values, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read values %s: %w", path, err)
	}
	return ParseValues(data)
}

// ParseValues strictly decodes and validates values-file bytes.
func ParseValues(data []byte) (*Values, error) {
	var v Values
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&v); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("values file is empty")
		}
		return nil, fmt.Errorf("parse values: %w", err)
	}
	if err := v.Validate(); err != nil {
		return nil, err
	}
	return &v, nil
}

// UnitName returns the effective own-unit name.
func (v *Values) UnitName() string {
	if v.Unit.Name == "" {
		return DefaultUnitName
	}
	return v.Unit.Name
}

// UnitPath is where the unit file is written.
func (v *Values) UnitPath() string { return filepath.Join(v.Unit.Dir, v.UnitName()) }

// Package returns the effective go package to build.
func (v *Values) Package() string {
	if v.Binary.Package == "" {
		return defaultPackage
	}
	return v.Binary.Package
}

// ReadyzURL returns the effective readiness URL.
func (v *Values) ReadyzURL() string {
	if v.Readyz.URL != "" {
		return v.Readyz.URL
	}
	listen := v.Daemon.Listen
	if listen == "" {
		listen = defaultListen
	}
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return ""
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/readyz"
}

// ReadyzTimeout returns the effective readiness timeout.
func (v *Values) ReadyzTimeout() time.Duration {
	if v.Readyz.Timeout == "" {
		return defaultReadyzTimeout
	}
	d, err := time.ParseDuration(v.Readyz.Timeout)
	if err != nil {
		return defaultReadyzTimeout
	}
	return d
}

// Validate checks the whole file and reports every problem at once.
func (v *Values) Validate() error {
	var errs []string
	add := func(format string, a ...any) { errs = append(errs, fmt.Sprintf(format, a...)) }

	if v.SchemaVersion != SchemaVersion {
		add("schema_version: got %d, this build supports %d", v.SchemaVersion, SchemaVersion)
	}

	if v.Unit.Name != "" && !ownUnitPattern.MatchString(v.Unit.Name) {
		add("unit.name: %q is not a valid .service unit name", v.Unit.Name)
	}
	reqAbs := func(key, val string) {
		if val == "" {
			add("%s: required", key)
			return
		}
		optAbs(&errs, key, val)
	}
	reqAbs("unit.dir", v.Unit.Dir)
	if v.Unit.User == "" {
		add("unit.user: required")
	} else if !userPattern.MatchString(v.Unit.User) {
		add("unit.user: %q is not a valid account name", v.Unit.User)
	}
	optAbs(&errs, "unit.env_file", v.Unit.EnvFile)
	if strings.ContainsAny(v.Unit.EnvFile, " \t%") {
		add("unit.env_file: %q contains whitespace or %%; systemd does not unquote this value and expands %% specifiers", v.Unit.EnvFile)
	}

	reqAbs("binary.install_path", v.Binary.InstallPath)
	reqAbs("binary.source_dir", v.Binary.SourceDir)
	if r := v.Binary.SourceRef; r != "" && (hasControl(r) || strings.ContainsAny(r, " \t") || strings.HasPrefix(r, "-")) {
		add("binary.source_ref: %q must not contain whitespace or control characters or start with -", r)
	}
	if p := v.Binary.Package; p != "" && (hasControl(p) || strings.ContainsAny(p, " \t")) {
		add("binary.package: %q contains whitespace or control characters", p)
	}

	v.validateDaemon(&errs)

	if u := v.Readyz.URL; u != "" {
		if err := selfbuild.ValidateSelfBuildURL(u, "readyz"); err != nil {
			add("readyz.url: %v", err)
		}
	} else if v.ReadyzURL() == "" {
		add("daemon.listen: %q is not host:port; set readyz.url", v.Daemon.Listen)
	}
	optAbs(&errs, "readyz.token_file", v.Readyz.TokenFile)
	if t := v.Readyz.Timeout; t != "" {
		if d, err := time.ParseDuration(t); err != nil || d <= 0 {
			add("readyz.timeout: %q is not a positive duration", t)
		}
	}

	seen := map[string]bool{}
	for i, u := range v.Supersedes {
		if !unitNamePattern.MatchString(u) {
			add("supersedes[%d]: %q is not a valid systemd unit name", i, u)
			continue
		}
		if seen[u] {
			add("supersedes[%d]: %q listed twice", i, u)
		}
		seen[u] = true
	}

	if len(errs) > 0 {
		return fmt.Errorf("invalid values: %s", strings.Join(errs, "; "))
	}
	return nil
}

func (v *Values) validateDaemon(errs *[]string) {
	d := &v.Daemon
	add := func(format string, a ...any) { *errs = append(*errs, fmt.Sprintf(format, a...)) }

	switch d.RegistrySource {
	case "file":
		if d.RegistryDir == "" {
			add("daemon.registry_dir: required when registry_source is file")
		}
		for k, val := range map[string]string{
			"registry_git_url": d.RegistryGitURL, "registry_git_ref": d.RegistryGitRef,
			"registry_git_subpath": d.RegistryGitSubpath, "registry_git_vocab": d.RegistryGitVocab,
			"registry_git_poll": d.RegistryGitPoll, "registry_cache_dir": d.RegistryCacheDir,
			"registry_secret_keyfile": d.RegistrySecretKeyfile,
		} {
			if val != "" {
				add("daemon.%s: only valid when registry_source is git", k)
			}
		}
	case "git":
		if d.RegistryGitURL == "" {
			add("daemon.registry_git_url: required when registry_source is git")
		}
		if d.RegistryDir != "" {
			add("daemon.registry_dir: only valid when registry_source is file")
		}
		if d.VocabFile != "" {
			add("daemon.vocab_file: ignored by the daemon in git mode; use registry_git_vocab")
		}
	case "":
		add("daemon.registry_source: required (file|git)")
	default:
		add("daemon.registry_source: %q must be file or git", d.RegistrySource)
	}

	for key, val := range map[string]string{
		"registry_dir": d.RegistryDir, "vocab_file": d.VocabFile,
		"registry_cache_dir": d.RegistryCacheDir, "registry_secret_keyfile": d.RegistrySecretKeyfile,
		"self_build_base_dir": d.SelfBuildBaseDir,
	} {
		optAbs(errs, "daemon."+key, val)
	}
	for key, val := range map[string]string{
		"registry_git_url": d.RegistryGitURL, "registry_git_ref": d.RegistryGitRef,
		"registry_git_subpath": d.RegistryGitSubpath, "registry_git_vocab": d.RegistryGitVocab,
		"listen": d.Listen,
	} {
		if hasControl(val) {
			add("daemon.%s: contains control characters", key)
		}
	}

	switch d.LogLevel {
	case "", "debug", "info", "warn", "error":
	default:
		add("daemon.log_level: %q must be debug|info|warn|error", d.LogLevel)
	}

	for key, val := range map[string]string{
		"registry_git_poll": d.RegistryGitPoll, "self_build_source_watch_interval": d.SelfBuildSourceWatchInterval,
		"self_build_source_watch_window": d.SelfBuildSourceWatchWindow, "self_build_usage_window": d.SelfBuildUsageWindow,
	} {
		if val == "" {
			continue
		}
		if dur, err := time.ParseDuration(val); err != nil || dur <= 0 {
			add("daemon.%s: %q is not a positive duration", key, val)
		}
	}

	if d.SelfBuildSourceWatchURL != "" {
		if err := selfbuild.ValidateSelfBuildURL(d.SelfBuildSourceWatchURL, "source-watch"); err != nil {
			add("daemon.self_build_source_watch_url: %v", err)
		}
	} else if d.SelfBuildSourceWatchInterval != "" || d.SelfBuildSourceWatchWindow != "" {
		add("daemon.self_build_source_watch_interval/window: require self_build_source_watch_url")
	}
	if d.SelfBuildUsageEventStoreURL != "" {
		if err := selfbuild.ValidateSelfBuildURL(d.SelfBuildUsageEventStoreURL, "usage-event-store"); err != nil {
			add("daemon.self_build_usage_event_store_url: %v", err)
		}
	} else if d.SelfBuildUsageWindow != "" {
		add("daemon.self_build_usage_window: requires self_build_usage_event_store_url")
	}
	selfBuild := d.SelfBuildMCPDiscovery || d.SelfBuildSourceWatchURL != "" || d.SelfBuildUsageEventStoreURL != ""
	if selfBuild && d.SelfBuildBaseDir == "" {
		add("daemon.self_build_base_dir: required when any self-build mechanism is enabled")
	}
	if !selfBuild && d.SelfBuildBaseDir != "" {
		add("daemon.self_build_base_dir: set but no self-build mechanism is enabled")
	}
}

// optAbs records an error when val is set but is not a clean absolute path.
func optAbs(errs *[]string, key, val string) {
	if val == "" {
		return
	}
	switch {
	case hasControl(val):
		*errs = append(*errs, fmt.Sprintf("%s: contains control characters", key))
	case !filepath.IsAbs(val):
		*errs = append(*errs, fmt.Sprintf("%s: %q must be an absolute path", key, val))
	}
}

func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}
