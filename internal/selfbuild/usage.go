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
	// ActorRole is the registry role of the actor ("lead", "director",
	// "operator", "crew"). Populated when the relay event API exposes it.
	// Empty string when the relay version predates this field. lr-d482.
	ActorRole string `json:"actor_role,omitempty"`
	// LastLoreSearchAt is the RFC3339 timestamp of the most recent lore
	// search recorded in the conversation's agent_state ledger at the time
	// this event was emitted. Nil/empty when no search was recorded.
	// Populated when the relay event API exposes it. lr-d482.
	LastLoreSearchAt string `json:"last_lore_search_at,omitempty"`
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
	result := aggregate(events)
	return u.emitDrift(result)
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

// actorResearchInfo tracks research-first signals for an actor across events.
type actorResearchInfo struct {
	// role is the most recent registry role observed for this actor.
	role string
	// hasLoreSearch is true when at least one event in the window carried
	// a non-empty LastLoreSearchAt for this actor.
	hasLoreSearch bool
}

// aggregateResult is the combined output of aggregate.
type aggregateResult struct {
	// counts maps (actor, next_actor, conversation_kind) to observed count.
	counts map[sequenceTuple]int
	// researchInfo maps actor name to their research-first signals.
	researchInfo map[string]*actorResearchInfo
}

// aggregate counts (actor, next_actor, conversation_kind) tuples and collects
// research-first signals (actor role + whether any lore search was recorded).
// lr-d482: research-first drift detection.
func aggregate(events []RelayEvent) aggregateResult {
	counts := make(map[sequenceTuple]int)
	researchInfo := make(map[string]*actorResearchInfo)

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

		// Track per-actor research-first signals.
		info, ok := researchInfo[ev.Actor]
		if !ok {
			info = &actorResearchInfo{}
			researchInfo[ev.Actor] = info
		}
		if ev.ActorRole != "" {
			info.role = ev.ActorRole
		}
		if ev.LastLoreSearchAt != "" {
			info.hasLoreSearch = true
		}
	}

	return aggregateResult{counts: counts, researchInfo: researchInfo}
}

// isLeadOrDirector returns true when the role is "lead" or "director".
// Used by emitDrift to decide whether the research-first flag applies. lr-d482.
func isLeadOrDirector(role string) bool {
	return role == "lead" || role == "director"
}

func (u *UsageInference) emitDrift(result aggregateResult) ([]string, error) {
	// Group drift reports by actor so we write one file per actor-centric diff.
	byActor := make(map[string][]DriftReport)

	for tuple, count := range result.counts {
		registeredSeq := u.isRegistered(tuple.Actor, tuple.NextActor)

		// Only emit drift when the empirical sequence is not registered.
		if registeredSeq {
			continue
		}

		// Research-first flag: set when the actor is a lead/director who
		// posted in this window without a recorded lore search. lr-d482.
		researchFirstFlag := false
		if info, ok := result.researchInfo[tuple.Actor]; ok {
			if isLeadOrDirector(info.role) && !info.hasLoreSearch {
				researchFirstFlag = true
			}
		}

		byActor[tuple.Actor] = append(byActor[tuple.Actor], DriftReport{
			Actor:              tuple.Actor,
			NextActor:          tuple.NextActor,
			ConversationKind:   tuple.ConversationKind,
			ObservedCount:      count,
			RegisteredAfterSeq: false,
			ResearchFirstFlag:  researchFirstFlag,
		})
	}

	var written []string
	for actor, reports := range byActor {
		windowLabel := u.cfg.Window.String()
		notes := []string{
			fmt.Sprintf("Drift detected over rolling window: %s", windowLabel),
			fmt.Sprintf("Observed %d unregistered sequencing pattern(s) for actor %q", len(reports), actor),
		}

		// Append research-first note when any report in this batch has the flag set.
		// lr-d482: signals lead/director posted without prior lore search in this window.
		for _, r := range reports {
			if r.ResearchFirstFlag {
				notes = append(notes,
					fmt.Sprintf("RESEARCH-FIRST: actor %q (lead/director) had no recorded prior-context search in the event window. Existing context should be consulted before proposing fixes.", actor),
				)
				break // one note per actor batch is sufficient
			}
		}

		pc := ProposedChange{
			SchemaVersion: 1,
			GeneratedAt:   time.Now().UTC(),
			Source:        "usage-inference",
			AgentName:     actor,
			DriftReports:  reports,
			Notes:         notes,
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
