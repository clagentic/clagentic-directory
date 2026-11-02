package update

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// sourceRemote is the remote of source_dir that reviewed code is fetched from.
const sourceRemote = "origin"

// ErrNotCheckout means source_dir is not a git checkout with an origin remote.
var ErrNotCheckout = errors.New("not a git checkout with an origin remote")

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

// GitSource exports a commit through the git binary.
//
// It fetches into a private repository, not into source_dir: source_dir is
// usually owned by another account, and a root-run fetch there would leave
// root-owned objects in a builder's repository. Only source_dir's remote URL
// is read from it.
type GitSource struct{ Run CommandRunner }

func (g GitSource) Export(srcDir, ref string) (*Export, error) {
	// safe.directory=* on the command line only: root reads a repository
	// another account owns, and git refuses that by default.
	const trust = "safe.directory=*"
	url, err := g.Run("git", "-c", trust, "-C", srcDir, "config", "--get", "remote."+sourceRemote+".url")
	if err != nil {
		if notCheckout(err, url) {
			return nil, fmt.Errorf("%w: source_dir %s", ErrNotCheckout, srcDir)
		}
		// On failure url holds git's stderr, never the remote URL.
		return nil, fmt.Errorf("read origin of source_dir %s: %v: %s", srcDir, err, url)
	}
	if url == "" {
		return nil, fmt.Errorf("%w: source_dir %s", ErrNotCheckout, srcDir)
	}
	if ref == "" {
		ref = "HEAD"
	}

	tmp, err := os.MkdirTemp("", "clagentic-directory-update-")
	if err != nil {
		return nil, fmt.Errorf("private export dir: %v", err)
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }
	repo, tree := filepath.Join(tmp, "repo"), filepath.Join(tmp, "tree")
	if err := os.Mkdir(tree, 0o700); err != nil {
		cleanup()
		return nil, fmt.Errorf("private export dir: %v", err)
	}

	steps := [][]string{
		{"-c", trust, "init", "--quiet", repo},
		{"-c", trust, "-C", repo, "fetch", "--quiet", "--no-tags", "--no-recurse-submodules", "--", url, ref},
	}
	for _, args := range steps {
		if out, err := g.Run("git", args...); err != nil {
			cleanup()
			return nil, fmt.Errorf("git %s: %v: %s", gitVerb(args), err, out)
		}
	}
	sha, err := g.Run("git", "-C", repo, "rev-parse", "--verify", "--quiet", "FETCH_HEAD^{commit}")
	if err != nil || !commitPattern.MatchString(sha) {
		cleanup()
		return nil, fmt.Errorf("ref %q did not resolve to a commit: %v: %s", ref, err, sha)
	}
	// checkout with --work-tree writes the files and leaves HEAD alone;
	// unlike git archive it ignores export-ignore and export-subst rules.
	if out, err := g.Run("git", "-C", repo, "--work-tree", tree, "checkout", "--quiet", "--force", sha, "--", "."); err != nil {
		cleanup()
		return nil, fmt.Errorf("git checkout %s: %v: %s", sha, err, out)
	}
	return &Export{Dir: tree, Commit: sha, Cleanup: cleanup}, nil
}

// notCheckout reports whether a failed `git config --get` answers that
// source_dir has no origin: exit 1 means the key is absent, and git's own
// "not a git repository" message confirms a plain directory. A missing git
// binary, a permission problem or any other failure says nothing about
// source_dir.
func notCheckout(err error, out string) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	return exitErr.ExitCode() == 1 || strings.Contains(out, "not a git repository")
}

// gitLocationVars name the variables that redirect git to a repository other
// than the one -C selects. A caller running inside a git hook has them set.
var gitLocationVars = map[string]bool{
	"GIT_DIR": true, "GIT_WORK_TREE": true, "GIT_INDEX_FILE": true,
	"GIT_OBJECT_DIRECTORY": true, "GIT_ALTERNATE_OBJECT_DIRECTORIES": true,
	"GIT_COMMON_DIR": true, "GIT_PREFIX": true, "GIT_NAMESPACE": true,
}

// runGit runs name with args (no shell), without the repository-location
// variables, and returns combined output.
func runGit(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	for _, kv := range os.Environ() {
		if key, _, _ := strings.Cut(kv, "="); !gitLocationVars[key] {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// gitVerb returns the subcommand of a git argument list, for error text.
func gitVerb(args []string) string {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-c", "-C":
			i++
		default:
			return args[i]
		}
	}
	return ""
}
