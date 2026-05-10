package selfbuild

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// UsageConfig configures the usage-driven inference mechanism.
type UsageConfig struct {
	// RelayURL is the clagentic-relay event store base URL.
	RelayURL string
	// BaseDir is the root directory where proposed_changes/ will be written.
	BaseDir string
	// Window is the rolling time window for event aggregation.
	Window time.Duration
	// RunInterval controls how often usage is re-analyzed. Default = Window.
	RunInterval time.Duration
	// HTTPTimeout for relay API calls.
	HTTPTimeout time.Duration
}

// RelayEvent is a single event from the clagentic-relay event store.
type RelayEvent struct {
	Actor            string    `json:"actor"`
	NextActor        string    `json:"next_actor"`
	ConversationKind string    `json:"conversation_kind"`
	Timestamp        time.Time `json:"timestamp"`
}

// UsageInference pulls events from the relay event store, compares empirical
// actor-sequencing against the registered after_agents, and emits drift reports
// to proposed_changes/ when discrepancies are found.
//
// It never writes to the live registry.
type UsageInference struct {
	cfg    UsageConfig
	client *http.Client
	store  StoreReader
}

// StoreReader is the subset of store.Store needed by UsageInference.
type StoreReader interface {
	FindBySequencing(afterAgent string) []AgentRef
}

// AgentRef is a minimal agent reference (avoid import cycle with store package).
type AgentRef struct {
	Name string
}

// NewUsageInference creates a UsageInference with the given config.
// storeReader may be nil; in that case sequencing lookups always return empty.
func NewUsageInference(cfg UsageConfig, storeReader StoreReader) *UsageInference {
	if cfg.RunInterval == 0 {
		cfg.RunInterval = cfg.Window
	}
	timeout := cfg.HTTPTimeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	return &UsageInference{
		cfg:    cfg,
		client: &http.Client{Timeout: timeout},
		store:  storeReader,
	}
}

// Run polls the relay event store on the configured interval until ctx is cancelled.
func (u *UsageInference) Run(ctx context.Context) {
	slog.Info("usage-inference: starting", "relay", u.cfg.RelayURL, "window", u.cfg.Window)
	ticker := time.NewTicker(u.cfg.RunInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("usage-inference: stopped")
			return
		case <-ticker.C:
			if err := u.analyze(ctx); err != nil {
				slog.Warn("usage-inference: analysis error", "err", err)
			}
		}
	}
}

// Analyze fetches events and writes drift reports (exposed for testing).
func (u *UsageInference) Analyze(ctx context.Context, events []RelayEvent) ([]string, error) {
	tuples := aggregate(events)
	return u.emitDrift(tuples)
}

func (u *UsageInference) analyze(ctx context.Context) error {
	events, err := u.fetchEvents(ctx)
	if err != nil {
		return err
	}
	slog.Debug("usage-inference: fetched events", "count", len(events))
	_, err = u.Analyze(ctx, events)
	return err
}

func (u *UsageInference) fetchEvents(ctx context.Context) ([]RelayEvent, error) {
	since := time.Now().Add(-u.cfg.Window).UTC().Format(time.RFC3339)
	url := fmt.Sprintf("%s/v1/events?since=%s", u.cfg.RelayURL, since)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := u.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("relay API returned status %d", resp.StatusCode)
	}

	var events []RelayEvent
	if err := json.NewDecoder(resp.Body).Decode(&events); err != nil {
		return nil, fmt.Errorf("decode relay events: %w", err)
	}
	return events, nil
}

// sequenceTuple is the aggregation key.
type sequenceTuple struct {
	Actor            string
	NextActor        string
	ConversationKind string
}

// aggregate counts (actor, next_actor, conversation_kind) tuples.
func aggregate(events []RelayEvent) map[sequenceTuple]int {
	counts := make(map[sequenceTuple]int)
	for _, ev := range events {
		if ev.Actor == "" || ev.NextActor == "" {
			continue
		}
		k := sequenceTuple{
			Actor:            ev.Actor,
			NextActor:        ev.NextActor,
			ConversationKind: ev.ConversationKind,
		}
		counts[k]++
	}
	return counts
}

func (u *UsageInference) emitDrift(tuples map[sequenceTuple]int) ([]string, error) {
	// Group drift reports by actor so we write one file per actor-centric diff.
	byActor := make(map[string][]DriftReport)

	for tuple, count := range tuples {
		registeredSeq := u.isRegistered(tuple.Actor, tuple.NextActor)

		// Only emit drift when the empirical sequence is not registered.
		if registeredSeq {
			continue
		}

		byActor[tuple.Actor] = append(byActor[tuple.Actor], DriftReport{
			Actor:              tuple.Actor,
			NextActor:          tuple.NextActor,
			ConversationKind:   tuple.ConversationKind,
			ObservedCount:      count,
			RegisteredAfterSeq: false,
		})
	}

	var written []string
	for actor, reports := range byActor {
		windowLabel := u.cfg.Window.String()
		pc := ProposedChange{
			SchemaVersion: 1,
			GeneratedAt:   time.Now().UTC(),
			Source:        "usage-inference",
			AgentName:     actor,
			DriftReports:  reports,
			Notes: []string{
				fmt.Sprintf("Drift detected over rolling window: %s", windowLabel),
				fmt.Sprintf("Observed %d unregistered sequencing pattern(s) for actor %q", len(reports), actor),
			},
		}
		path, err := WriteProposedChange(u.cfg.BaseDir, pc)
		if err != nil {
			slog.Warn("usage-inference: write error", "actor", actor, "err", err)
			continue
		}
		slog.Info("usage-inference: drift report written", "actor", actor, "path", path)
		written = append(written, path)
	}
	return written, nil
}

// isRegistered checks whether nextActor appears as an after_agent for actor in the store.
func (u *UsageInference) isRegistered(actor, nextActor string) bool {
	if u.store == nil {
		return false
	}
	for _, ref := range u.store.FindBySequencing(actor) {
		if ref.Name == nextActor {
			return true
		}
	}
	return false
}
