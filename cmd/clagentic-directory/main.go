package main

import (
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"forgejo.akuehner.com/clagentic/clagentic-directory/internal/api"
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

	h := api.New(s)
	mux := http.NewServeMux()
	h.Register(mux)

	slog.Info("clagentic-directory listening", "addr", *listen)
	if err := http.ListenAndServe(*listen, mux); err != nil {
		slog.Error("server error", "err", err)
		os.Exit(1)
	}
}
