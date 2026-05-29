package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"forgejo.akuehner.com/clagentic/clagentic-directory/internal/api"
	"forgejo.akuehner.com/clagentic/clagentic-directory/internal/selfbuild"
	"forgejo.akuehner.com/clagentic/clagentic-directory/internal/store"
)

// buildRevision is injected by the binary-rebuild script via -ldflags "-X main.buildRevision=<sha>".
// When not injected, the value is empty and the running binary relies on Go's built-in
// debug/buildinfo VCS metadata (set automatically by go build from a git working tree).
// lr-8fa1: version-drift detection for binary auto-rebuild.
var buildRevision string

// directoryConfig is a minimal representation of the clagentic-directory config file.
// Only fields needed by the inspect subcommand are decoded here.
type directoryConfig struct {
	SelfBuild struct {
		BaseDir string `yaml:"base_dir"`
	} `yaml:"self_build"`
}

// loadConfig reads the config file at path and returns the decoded config.
// Missing files are not an error — the caller falls back to defaults.
func loadConfig(path string) (directoryConfig, error) {
	var cfg directoryConfig
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("read config %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse config %s: %w", path, err)
	}
	return cfg, nil
}

// runInspect implements the 'inspect' subcommand: one-shot MCP introspection
// that writes a proposed_changes/ file without starting the HTTP service.
func runInspect(args []string) int {
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	agentName := fs.String("agent", "", "agent name (required)")
	mcpURL := fs.String("mcp-url", "", "MCP server endpoint to introspect (required)")
	configPath := fs.String("config", "", "path to clagentic-directory config (default: ~/.config/clagentic/directory.yaml)")
	outputDir := fs.String("output-dir", "", "proposed_changes root (default: from config, or ./proposed_changes)")

	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "inspect: %v\n", err)
		return 1
	}
	if *agentName == "" {
		fmt.Fprintln(os.Stderr, "inspect: --agent is required")
		return 1
	}
	if *mcpURL == "" {
		fmt.Fprintln(os.Stderr, "inspect: --mcp-url is required")
		return 1
	}

	// Resolve config path.
	cfgPath := *configPath
	if cfgPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "inspect: cannot resolve home dir: %v\n", err)
			return 1
		}
		cfgPath = filepath.Join(home, ".config", "clagentic", "directory.yaml")
	}

	// Resolve output-dir: flag > config > default.
	baseDir := *outputDir
	if baseDir == "" {
		cfg, err := loadConfig(cfgPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "inspect: %v\n", err)
			return 1
		}
		baseDir = cfg.SelfBuild.BaseDir
	}
	if baseDir == "" {
		baseDir = "./proposed_changes"
	}
	// WriteProposedChange appends proposed_changes/ under baseDir; when the
	// operator passes ./proposed_changes as --output-dir they mean the root,
	// not a parent. Strip the trailing segment if the caller already included it
	// so we don't double-nest.
	if filepath.Base(baseDir) == "proposed_changes" {
		baseDir = filepath.Dir(baseDir)
	}

	fmt.Fprintf(os.Stderr, "inspect: agent=%s mcp-url=%s output-base=%s\n", *agentName, *mcpURL, baseDir)

	d := selfbuild.NewMCPDiscovery(selfbuild.MCPConfig{BaseDir: baseDir})
	path, err := d.Inspect(context.Background(), *agentName, *mcpURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "inspect: %v\n", err)
		return 1
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		// path is already usable; fall back to relative
		abs = path
	}
	fmt.Println(abs)
	return 0
}

func main() {
	// Subcommand dispatch: inspect runs standalone, before service flag parsing.
	if len(os.Args) > 1 && os.Args[1] == "inspect" {
		os.Exit(runInspect(os.Args[2:]))
	}

	var (
		registrySource     = flag.String("registry-source", "", "Backend type: file|git (required)")
		registryDir        = flag.String("registry-dir", "", "Directory containing agent YAML files (when source=file)")
		vocabularyExt      = flag.String("vocabulary-extensions", "", "Path to a YAML file of additional vocabulary values to merge into the base enums (optional)")
		registryGitURL     = flag.String("registry-git-url", "", "Git repo URL (when source=git)")
		registryGitRef     = flag.String("registry-git-ref", "main", "Git ref to track (when source=git)")
		registryGitPoll    = flag.Duration("registry-git-poll", 60*time.Second, "Poll interval (when source=git)")
		registryGitSubpath = flag.String("registry-git-subpath", "", "Subdirectory within git repo (when source=git)")
		registryCacheDir   = flag.String("registry-cache-dir", "/var/cache/clagentic-directory/registry", "Cache dir for git source")
		registrySecretKey  = flag.String("registry-secret-keyfile", "", "SSH deploy key or HTTPS token file (when source=git)")
		listen             = flag.String("listen", ":8444", "Listen address")
		logLevel           = flag.String("log-level", "info", "Log level: debug|info|warn|error")

		// Self-build: proposed_changes base dir (shared by all mechanisms).
		selfBuildBaseDir = flag.String("self-build-base-dir", "", "Base directory for proposed_changes/ output (required when any self-build flag is set)")

		// Mechanism 1: MCP discovery (default off).
		selfBuildMCPDiscovery = flag.Bool("self-build-mcp-discovery", false, "Enable MCP discovery self-build mechanism (default off)")

		// Mechanism 2: Engram watch (default off; empty string = disabled).
		selfBuildEngramWatchURL      = flag.String("self-build-engram-watch-url", "", "LORE API URL for engram-watch mechanism (default off)")
		selfBuildEngramWatchInterval = flag.Duration("self-build-engram-watch-interval", 60*time.Second, "Poll interval for engram-watch")
		selfBuildEngramWatchWindow   = flag.Duration("self-build-engram-watch-window", 5*time.Minute, "Rate-limit dedup window for engram-watch")

		// Mechanism 3: Usage-driven inference (default off; empty string = disabled).
		selfBuildUsageRelayURL = flag.String("self-build-usage-relay-url", "", "clagentic-relay event store URL for usage inference (default off)")
		selfBuildUsageWindow   = flag.Duration("self-build-usage-window", time.Hour, "Rolling window for usage-driven inference")
	)
	flag.Parse()

	level := slog.LevelInfo
	switch *logLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	// Log build revision so operators can confirm which binary is running.
	if buildRevision != "" {
		slog.Info("clagentic-directory starting", "revision", buildRevision)
	} else {
		slog.Info("clagentic-directory starting", "revision", "embedded-vcs-or-unknown")
	}

	// Load optional vocabulary extensions before constructing the store.
	ext, err := store.LoadVocabularyExtensions(*vocabularyExt)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if *vocabularyExt != "" {
		slog.Info("vocabulary extensions loaded", "path", *vocabularyExt,
			"intents", len(ext.Intents),
			"conversation_kinds", len(ext.ConversationKinds),
			"trust_labels", len(ext.TrustLabels),
			"formats", len(ext.Formats),
		)
	}

	var s store.Store
	switch *registrySource {
	case "file":
		if *registryDir == "" {
			fmt.Fprintln(os.Stderr, "error: --registry-dir is required when --registry-source=file")
			os.Exit(1)
		}
		fs, err := store.NewFileStore(*registryDir, ext)
		if err != nil {
			slog.Error("failed to open file store", "err", err)
			os.Exit(1)
		}
		slog.Info("file store loaded", "dir", *registryDir, "agents", len(fs.ListAgents()))
		s = fs
	case "git":
		if *registryGitURL == "" {
			fmt.Fprintln(os.Stderr, "error: --registry-git-url is required when --registry-source=git")
			os.Exit(1)
		}
		gs, err := store.NewGitStore(store.GitStoreConfig{
			URL:      *registryGitURL,
			Ref:      *registryGitRef,
			CacheDir: *registryCacheDir,
			Subpath:  *registryGitSubpath,
			KeyFile:  *registrySecretKey,
			Poll:     *registryGitPoll,
			Ext:      ext,
		})
		if err != nil {
			slog.Error("failed to open git store", "err", err)
			os.Exit(1)
		}
		slog.Info("git store loaded", "url", *registryGitURL, "ref", *registryGitRef, "agents", len(gs.ListAgents()))
		s = gs
	case "":
		fmt.Fprintln(os.Stderr, "error: --registry-source is required (file|git)")
		os.Exit(1)
	default:
		fmt.Fprintf(os.Stderr, "error: unknown --registry-source %q (must be file|git)\n", *registrySource)
		os.Exit(1)
	}

	ctx := context.Background()

	// Start opt-in self-build mechanisms.
	selfBuildActive := *selfBuildMCPDiscovery || *selfBuildEngramWatchURL != "" || *selfBuildUsageRelayURL != ""
	if selfBuildActive && *selfBuildBaseDir == "" {
		fmt.Fprintln(os.Stderr, "error: --self-build-base-dir is required when any self-build mechanism is enabled")
		os.Exit(1)
	}

	if *selfBuildMCPDiscovery {
		slog.Info("self-build: MCP discovery enabled (CLI: use 'inspect' subcommand)")
		// MCP discovery is ad-hoc (invoked via CLI subcommand), not a background loop.
		// The flag enables the inspect subcommand; the mechanism itself is invoked on demand.
		_ = selfbuild.NewMCPDiscovery(selfbuild.MCPConfig{BaseDir: *selfBuildBaseDir})
	}

	if *selfBuildEngramWatchURL != "" {
		slog.Info("self-build: engram-watch enabled", "url", *selfBuildEngramWatchURL)
		w := selfbuild.NewEngramWatcher(selfbuild.EngramWatchConfig{
			LOREURL:      *selfBuildEngramWatchURL,
			BaseDir:      *selfBuildBaseDir,
			PollInterval: *selfBuildEngramWatchInterval,
			RateWindow:   *selfBuildEngramWatchWindow,
		})
		go w.Run(ctx)
	}

	if *selfBuildUsageRelayURL != "" {
		slog.Info("self-build: usage-inference enabled", "relay", *selfBuildUsageRelayURL, "window", *selfBuildUsageWindow)
		adapter := &storeSequencingAdapter{s: s}
		u := selfbuild.NewUsageInference(selfbuild.UsageConfig{
			RelayURL:    *selfBuildUsageRelayURL,
			BaseDir:     *selfBuildBaseDir,
			Window:      *selfBuildUsageWindow,
			RunInterval: *selfBuildUsageWindow,
		}, adapter)
		go u.Run(ctx)
	}

	h := api.New(s)
	mux := http.NewServeMux()
	h.Register(mux)

	slog.Info("clagentic-directory listening", "addr", *listen)
	if err := http.ListenAndServe(*listen, mux); err != nil {
		slog.Error("server error", "err", err)
		os.Exit(1)
	}
}

// storeSequencingAdapter adapts store.Store to selfbuild.StoreReader.
type storeSequencingAdapter struct {
	s store.Store
}

func (a *storeSequencingAdapter) FindBySequencing(afterAgent string) []selfbuild.AgentRef {
	agents := a.s.FindBySequencing(afterAgent)
	refs := make([]selfbuild.AgentRef, len(agents))
	for i, ag := range agents {
		refs[i] = selfbuild.AgentRef{Name: ag.Name}
	}
	return refs
}
