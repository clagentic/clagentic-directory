package selfbuild

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// WriteProposedChange writes a ProposedChange to the proposed_changes/ subdirectory
// under baseDir. It never touches the live registry directory.
//
// File name: proposed_changes/<agent>.<unix-timestamp>.yaml
func WriteProposedChange(baseDir string, pc ProposedChange) (string, error) {
	dir := filepath.Join(baseDir, "proposed_changes")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("selfbuild: mkdir proposed_changes: %w", err)
	}

	ts := time.Now().UTC().Unix()
	name := fmt.Sprintf("%s.%d.yaml", sanitizeAgentName(pc.AgentName), ts)
	path := filepath.Join(dir, name)

	data, err := yaml.Marshal(pc)
	if err != nil {
		return "", fmt.Errorf("selfbuild: marshal proposed change: %w", err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("selfbuild: write proposed change: %w", err)
	}
	return path, nil
}

func sanitizeAgentName(name string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		return '_'
	}, name)
}
