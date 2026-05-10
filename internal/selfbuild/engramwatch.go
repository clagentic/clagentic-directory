package selfbuild

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// EngramWatchConfig configures the engram-watch mechanism.
type EngramWatchConfig struct {
	// LORE API base URL, e.g. "http://localhost:9100".
	LOREURL string
	// BaseDir is the root directory where proposed_changes/ will be written.
	BaseDir string
	// PollInterval controls how often the engram stream is checked. Default 60s.
	PollInterval time.Duration
	// RateWindow is the dedup window per agent. Proposed changes for the same
	// agent within this window are coalesced. Default 5m.
	RateWindow time.Duration
	// HTTPTimeout for LORE API calls.
	HTTPTimeout time.Duration
}

// EngramWatcher polls the LORE engram/codex stream for agent-definition-file
// diffs (SKILL.md, AGENT.md changes) and writes proposed_changes/ entries when
// a diff is detected vs the current registry entry.
//
// It never writes to the live registry.
type EngramWatcher struct {
	cfg    EngramWatchConfig
	client *http.Client

	mu       sync.Mutex
	lastSeen map[string]time.Time // agent name -> last proposed change time
}

// EngramEvent is a single event from the LORE engram stream.
type EngramEvent struct {
	ID        string    `json:"id"`
	Timestamp time.Time `json:"timestamp"`
	Kind      string    `json:"kind"`   // e.g. "file-diff"
	FilePath  string    `json:"file_path"`
	Agent     string    `json:"agent"`  // agent name, if resolvable
	Diff      string    `json:"diff"`
}

// NewEngramWatcher creates an EngramWatcher with the given config.
func NewEngramWatcher(cfg EngramWatchConfig) *EngramWatcher {
	if cfg.PollInterval == 0 {
		cfg.PollInterval = 60 * time.Second
	}
	if cfg.RateWindow == 0 {
		cfg.RateWindow = 5 * time.Minute
	}
	timeout := cfg.HTTPTimeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	return &EngramWatcher{
		cfg:      cfg,
		client:   &http.Client{Timeout: timeout},
		lastSeen: make(map[string]time.Time),
	}
}

// Run polls the LORE engram stream until ctx is cancelled.
func (w *EngramWatcher) Run(ctx context.Context) {
	slog.Info("engram-watch: starting", "url", w.cfg.LOREURL, "interval", w.cfg.PollInterval)
	ticker := time.NewTicker(w.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("engram-watch: stopped")
			return
		case <-ticker.C:
			if err := w.poll(ctx); err != nil {
				slog.Warn("engram-watch: poll error", "err", err)
			}
		}
	}
}

// ProcessEvents processes a slice of engram events (exposed for testing).
func (w *EngramWatcher) ProcessEvents(events []EngramEvent) ([]string, error) {
	var written []string
	for _, ev := range events {
		if !isAgentDefFile(ev.FilePath) {
			continue
		}
		agentName := ev.Agent
		if agentName == "" {
			agentName = extractAgentFromPath(ev.FilePath)
		}
		if agentName == "" {
			continue
		}
		if w.isDupe(agentName) {
			slog.Debug("engram-watch: skipping duplicate", "agent", agentName)
			continue
		}

		caps := extractCapabilitiesFromDiff(ev.Diff)
		pc := ProposedChange{
			SchemaVersion: 1,
			GeneratedAt:   time.Now().UTC(),
			Source:        "engram-watch",
			AgentName:     agentName,
			Capabilities:  caps,
			Notes: []string{
				fmt.Sprintf("Derived from agent-definition-file diff: %s", ev.FilePath),
				fmt.Sprintf("Engram event ID: %s", ev.ID),
			},
		}

		path, err := WriteProposedChange(w.cfg.BaseDir, pc)
		if err != nil {
			slog.Warn("engram-watch: write error", "agent", agentName, "err", err)
			continue
		}
		w.markSeen(agentName)
		slog.Info("engram-watch: proposed change written", "agent", agentName, "path", path)
		written = append(written, path)
	}
	return written, nil
}

func (w *EngramWatcher) poll(ctx context.Context) error {
	url := fmt.Sprintf("%s/v1/engram/events?kinds=file-diff", w.cfg.LOREURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	resp, err := w.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("LORE API returned status %d", resp.StatusCode)
	}

	var events []EngramEvent
	if err := json.NewDecoder(resp.Body).Decode(&events); err != nil {
		return fmt.Errorf("decode engram events: %w", err)
	}

	_, err = w.ProcessEvents(events)
	return err
}

func (w *EngramWatcher) isDupe(agentName string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	last, ok := w.lastSeen[agentName]
	return ok && time.Since(last) < w.cfg.RateWindow
}

func (w *EngramWatcher) markSeen(agentName string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lastSeen[agentName] = time.Now()
}

// isAgentDefFile returns true if the file path looks like a SKILL.md or AGENT.md.
func isAgentDefFile(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, "skill.md") || strings.HasSuffix(lower, "agent.md")
}

// extractAgentFromPath guesses an agent name from the file path.
// e.g. "/workspace/crew-manifest/amos/SKILL.md" -> "amos"
func extractAgentFromPath(path string) string {
	parts := strings.Split(path, "/")
	// Walk backwards: skip the filename, return the first meaningful segment.
	for i := len(parts) - 2; i >= 0; i-- {
		seg := parts[i]
		if seg != "" && seg != "." && seg != "skills" && seg != ".claude" {
			return seg
		}
	}
	return ""
}

// extractCapabilitiesFromDiff does a best-effort parse of a unified diff for
// trigger/capability lines added (lines starting with "+").
func extractCapabilitiesFromDiff(diff string) []ProposedCapability {
	var caps []ProposedCapability
	var currentIntent string

	for _, line := range strings.Split(diff, "\n") {
		if !strings.HasPrefix(line, "+") {
			continue
		}
		content := strings.TrimPrefix(line, "+")
		content = strings.TrimSpace(content)

		// Look for intent-like keywords in added lines.
		lower := strings.ToLower(content)
		if strings.Contains(lower, "trigger") || strings.Contains(lower, "intent") ||
			strings.Contains(lower, "when:") || strings.Contains(lower, "use when") {
			currentIntent = content
		}

		if strings.Contains(lower, "capability") || strings.Contains(lower, "skill") ||
			strings.Contains(lower, "returns") {
			if currentIntent == "" {
				currentIntent = content
			}
			cap := ProposedCapability{
				ID:          AnnotatedString{Value: slugify(content), Confidence: ConfidenceInferred},
				Name:        AnnotatedString{Value: content, Confidence: ConfidenceInferred},
				Description: AnnotatedString{Value: content, Confidence: ConfidenceInferred},
				Intents:     AnnotatedStrings{Values: []string{slugify(currentIntent)}, Confidence: ConfidenceInferred},
				Format:      AnnotatedString{Value: inferFormat(content), Confidence: ConfidenceInferred},
			}
			caps = append(caps, cap)
		}
	}
	return caps
}

func slugify(s string) string {
	s = strings.ToLower(s)
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, s)
}
