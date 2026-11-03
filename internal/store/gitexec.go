package store

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Directory name prefixes the daemon creates under the cache dir. Only these
// (and the legacy per-URL clone name) are ever removed.
const (
	cloneDirPrefix = "clone-"
	probeDirPrefix = "probe-"
)

// safeGitConfig is defense in depth on top of the fresh-directory model and the
// GIT_CONFIG_* isolation: it pins the keys that run commands in clone and
// checkout. Neither `clone` nor `ls-remote` accepts a --no-hooks flag.
func safeGitConfig() []string {
	return []string{
		"-c", "core.hooksPath=/dev/null",
		"-c", "core.fsmonitor=false",
	}
}

// runGit runs git with cwd set to dir under the store's timeout and config
// isolation: system and global config are disabled, command-running keys are
// overridden on the command line, only the configured URL's transport is
// allowed, and discovery is capped at the cache dir. dir must be a directory
// the daemon created itself; never pass a pre-existing one.
func (g *GitStore) runGit(dir string, args ...string) (string, error) {
	proto, err := protocolAllowlist(g.url)
	if err != nil {
		return "", err
	}
	full := append(append(safeGitConfig(), proto...), args...)
	ctx, cancel := context.WithTimeout(g.ctx, g.gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", full...)
	// Bound the wait for output pipes if a killed git left a child holding them.
	cmd.WaitDelay = time.Second
	cmd.Dir = dir
	iso := gitConfigIsolationEnv(g.cacheDir)
	if g.keyFile != "" && strings.HasPrefix(g.url, "git@") {
		// Use a whitelisted env rather than inheriting os.Environ() to prevent
		// GIT_SSH_COMMAND or GIT_ASKPASS overrides from the parent process environment.
		// StrictHostKeyChecking=accept-new accepts new hosts on first connect but
		// rejects unexpected key changes, preventing silent MITM.
		cmd.Env = append([]string{
			"PATH=" + os.Getenv("PATH"),
			"HOME=" + os.Getenv("HOME"),
			"GIT_SSH_COMMAND=ssh -i " + g.keyFile + " -o StrictHostKeyChecking=accept-new -o BatchMode=yes",
		}, iso...)
	} else {
		cmd.Env = append(os.Environ(), iso...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("git %s: timed out after %s", gitSubcommand(args), g.gitTimeout)
		}
		return "", fmt.Errorf("git %s: %w\n%s%s", gitSubcommand(args), err, stdout.String(), stderr.String())
	}
	return stdout.String(), nil
}

// gitSubcommand returns the git subcommand name, skipping leading `-c k=v`
// pairs, for error messages (args may carry credentials, so they are not echoed).
func gitSubcommand(args []string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == "-c" {
			i++
			continue
		}
		return args[i]
	}
	return ""
}

// remoteSHA reads the origin SHA of the configured ref with `git ls-remote`.
// It runs in a scratch directory the daemon creates for the call, so no
// pre-existing repo is ever discovered.
func (g *GitStore) remoteSHA() (string, error) {
	scratch, err := os.MkdirTemp(g.cacheDir, probeDirPrefix)
	if err != nil {
		return "", fmt.Errorf("creating scratch dir: %w", err)
	}
	defer g.removeDir(scratch)

	refName := "refs/heads/" + g.ref
	out, err := g.runGit(scratch, "ls-remote", "--", g.url, refName)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == refName {
			if !isHexSHA(fields[0]) {
				return "", fmt.Errorf("git ls-remote returned a malformed SHA for %s", refName)
			}
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("git ls-remote: ref %s not found on origin", refName)
}

// isHexSHA reports whether s is a 40-character lowercase hex SHA-1.
func isHexSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// cloneFresh shallow-clones the configured ref into a new directory created
// under the cache dir and verifies HEAD equals want. On any failure the new
// directory is removed.
func (g *GitStore) cloneFresh(want string) (string, error) {
	dir, err := os.MkdirTemp(g.cacheDir, cloneDirPrefix)
	if err != nil {
		return "", fmt.Errorf("creating clone dir: %w", err)
	}
	// --depth=1 is a security invariant: shallow clones prevent an attacker from
	// crafting malformed git objects in history that could exploit git parsing bugs.
	// Do not remove --depth=1 without a security review.
	if _, err := g.runGit(dir, "clone", "--depth=1", "--branch", g.ref, "--", g.url, "."); err != nil {
		g.removeDir(dir)
		return "", err
	}
	head, err := g.runGit(dir, "rev-parse", "HEAD")
	if err != nil {
		g.removeDir(dir)
		return "", err
	}
	if got := strings.TrimSpace(head); got != want {
		g.removeDir(dir)
		return "", fmt.Errorf("clone HEAD %s does not match ls-remote %s (origin moved mid-sync)", got, want)
	}
	return dir, nil
}

// removeDir best-effort deletes a clone or probe directory the daemon made.
// It refuses anything that is not a direct child of the cache dir with one of
// the daemon's prefixes.
func (g *GitStore) removeDir(dir string) {
	base := filepath.Base(dir)
	if filepath.Dir(dir) != g.cacheDir ||
		(!strings.HasPrefix(base, cloneDirPrefix) && !strings.HasPrefix(base, probeDirPrefix)) {
		slog.Error("git store: refusing to remove unexpected directory", "dir", dir)
		return
	}
	if err := os.RemoveAll(dir); err != nil {
		slog.Warn("git store: removing directory failed", "dir", dir, "err", err)
	}
}

// removeStaleDirs deletes directories an earlier process left under the cache
// dir: clone and probe directories, and the pre-fresh-clone persistent clone
// named after the URL. Nothing in them is loaded or trusted. Best effort.
func (g *GitStore) removeStaleDirs() {
	entries, err := os.ReadDir(g.cacheDir)
	if err != nil {
		slog.Warn("git store: listing cache dir failed", "err", err)
		return
	}
	legacy := filepath.Base(strings.TrimSuffix(g.url, ".git"))
	for _, e := range entries {
		name := e.Name()
		switch {
		case strings.HasPrefix(name, cloneDirPrefix), strings.HasPrefix(name, probeDirPrefix):
		case name == legacy && name != "." && name != ".." && name != string(filepath.Separator) && e.IsDir():
			if _, err := os.Lstat(filepath.Join(g.cacheDir, name, ".git")); err != nil {
				continue
			}
		default:
			continue
		}
		if err := os.RemoveAll(filepath.Join(g.cacheDir, name)); err != nil {
			slog.Warn("git store: removing stale cache entry failed", "name", name, "err", err)
		}
	}
}
