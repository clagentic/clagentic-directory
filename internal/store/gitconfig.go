package store

import (
	"fmt"
	"strings"
)

// The daemon never runs git inside a directory it did not create in the same
// sync, and every git subprocess runs with the system and global config
// disabled and only the configured URL's transport allowed.

// gitConfigIsolationEnv disables system and global git config and stops
// repository discovery from walking above ceiling. Appended last so it wins
// over any inherited value.
func gitConfigIsolationEnv(ceiling string) []string {
	return []string{
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CEILING_DIRECTORIES=" + ceiling,
	}
}

// gitURLScheme classifies a configured remote URL as one of the transports the
// daemon permits: http, https, ssh, file. Local paths are file; the scp-like
// user@host:path form is ssh. A URL starting with "-" is refused so it cannot
// be read as a git option.
func gitURLScheme(url string) (string, error) {
	if strings.HasPrefix(url, "-") {
		return "", fmt.Errorf("git URL must not start with '-'")
	}
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
