package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"forgejo.akuehner.com/clagentic/clagentic-directory/internal/api"
	"forgejo.akuehner.com/clagentic/clagentic-directory/internal/selfbuild"
	"forgejo.akuehner.com/clagentic/clagentic-directory/internal/store"
)

func main() {
	var (
		registrySource     = flag.String("registry-source", "", "Backend type: file|git (required)")
		registryDir        = flag.String("registry-dir", "", "Directory containing agent YAML files (when source=file)")
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

	var s store.Store
	switch *registrySource {
	case "file":
		if *registryDir == "" {
			fmt.Fprintln(os.Stderr, "error: --registry-dir is required when --registry-source=file")
			os.Exit(1)
		}
		fs, err := store.NewFileStore(*registryDir)
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
