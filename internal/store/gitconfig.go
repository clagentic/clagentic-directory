package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The daemon does not trust any git config it did not write. Before each git
// invocation in a sync, GitStore overwrites <repo>/.git/config with the
// canonical config below, and every git subprocess runs with the system and
// global config disabled. Config keys that run commands (core.fsmonitor,
// filter.<x>.smudge, credential.helper, url.<x>.insteadOf with an ext::
// transport, remote.origin.uploadpack, ...) therefore cannot be planted.

// gitConfigIsolationEnv disables system and global git config. Appended last so
// it wins over any inherited value.
func gitConfigIsolationEnv() []string {
	return []string{"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
}

// gitURLScheme classifies a configured remote URL as one of the transports the
// daemon permits: http, https, ssh, file. Local paths are file; the scp-like
// user@host:path form is ssh.
func gitURLScheme(url string) (string, error) {
	if i := strings.Index(url, "://"); i > 0 {
		switch s := strings.ToLower(url[:i]); s {
		case "http", "https", "ssh", "file":
			return s, nil
		default:
			return "", fmt.Errorf("git URL scheme %q is not permitted (want http, https, ssh or file)", s)
		}
	}
	if strings.HasPrefix(url, "/") || strings.HasPrefix(url, ".") {
		return "file", nil
	}
	if strings.Contains(url, "@") && strings.Contains(url, ":") {
		return "ssh", nil
	}
	return "", fmt.Errorf("cannot determine git transport for configured URL")
}

// protocolAllowlist returns -c options that deny every transport except the
// scheme of the configured URL, so a rewritten URL cannot select ext:: or any
// other helper.
func protocolAllowlist(url string) ([]string, error) {
	scheme, err := gitURLScheme(url)
	if err != nil {
		return nil, err
	}
	return []string{
		"-c", "protocol.allow=never",
		"-c", "protocol." + scheme + ".allow=always",
	}, nil
}

// gitConfigQuote quotes a value for a git config file.
func gitConfigQuote(v string) (string, error) {
	if strings.ContainsAny(v, "\n\r\x00") {
		return "", fmt.Errorf("config value contains a control character")
	}
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(v) + `"`, nil
}

// canonicalGitConfig renders the only config the daemon lets git read.
func canonicalGitConfig(url, ref string) (string, error) {
	qURL, err := gitConfigQuote(url)
	if err != nil {
		return "", fmt.Errorf("remote url: %w", err)
	}
	refspec := "+refs/heads/" + ref + ":refs/remotes/origin/" + ref
	qSpec, err := gitConfigQuote(refspec)
	if err != nil {
		return "", fmt.Errorf("ref: %w", err)
	}
	return "[core]\n" +
		"\trepositoryformatversion = 0\n" +
		"\tbare = false\n" +
		"\thooksPath = /dev/null\n" +
		"\tfsmonitor = false\n" +
		"[remote \"origin\"]\n" +
		"\turl = " + qURL + "\n" +
		"\tfetch = " + qSpec + "\n", nil
}

// writeCanonicalConfig atomically replaces repoDir/.git/config (temp file in
// .git, then rename). .git must be a real directory: a gitdir pointer file or
// symlink would redirect git to config the daemon does not control.
func writeCanonicalConfig(repoDir, url, ref string) error {
	gitDir := filepath.Join(repoDir, ".git")
	fi, err := os.Lstat(gitDir)
	if err != nil {
		return fmt.Errorf("stat .git: %w", err)
	}
	if !fi.IsDir() {
		return fmt.Errorf(".git is not a plain directory: possible cache tampering")
	}
	content, err := canonicalGitConfig(url, ref)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(gitDir, "config.daemon-*")
	if err != nil {
		return fmt.Errorf("writing canonical git config: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("writing canonical git config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("writing canonical git config: %w", err)
	}
	if err := os.Rename(tmpName, filepath.Join(gitDir, "config")); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("installing canonical git config: %w", err)
	}
	return nil
}
