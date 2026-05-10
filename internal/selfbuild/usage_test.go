package selfbuild_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"forgejo.akuehner.com/clagentic/clagentic-directory/internal/selfbuild"
	"gopkg.in/yaml.v3"
)

// mockStoreReader implements selfbuild.StoreReader with a fixed sequencing map.
type mockStoreReader struct {
	// afterAgent -> list of registered next agents
	registered map[string][]string
}

func (m *mockStoreReader) FindBySequencing(afterAgent string) []selfbuild.AgentRef {
	var refs []selfbuild.AgentRef
	for _, name := range m.registered[afterAgent] {
		refs = append(refs, selfbuild.AgentRef{Name: name})
	}
	return refs
}

// Fixture events: amos always precedes peaches, but peaches->naomi is NOT registered.
var usageFixtureEvents = []selfbuild.RelayEvent{
	{Actor: "amos", NextActor: "peaches", ConversationKind: "code-review", Timestamp: time.Now()},
	{Actor: "amos", NextActor: "peaches", ConversationKind: "code-review", Timestamp: time.Now()},
	{Actor: "peaches", NextActor: "naomi", ConversationKind: "code-review", Timestamp: time.Now()},
	{Actor: "peaches", NextActor: "naomi", ConversationKind: "code-review", Timestamp: time.Now()},
	{Actor: "peaches", NextActor: "naomi", ConversationKind: "code-review", Timestamp: time.Now()},
	// This one IS registered — should not produce drift.
	{Actor: "amos", NextActor: "miller", ConversationKind: "troubleshoot", Timestamp: time.Now()},
}

func newFixtureStore() *mockStoreReader {
	return &mockStoreReader{
		registered: map[string][]string{
			// amos->miller is registered; amos->peaches is NOT.
			"amos": {"miller"},
			// peaches has no registered after_agents.
		},
	}
}

func TestUsageInference_Analyze(t *testing.T) {
	baseDir := t.TempDir()
	cfg := selfbuild.UsageConfig{
		RelayURL:    "http://localhost:8445",
		BaseDir:     baseDir,
		Window:      time.Hour,
		RunInterval: time.Hour,
	}
	u := selfbuild.NewUsageInference(cfg, newFixtureStore())

	written, err := u.Analyze(context.Background(), usageFixtureEvents)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	// Expect drift for: amos->peaches (unregistered) and peaches->naomi (unregistered).
	// They are in different actor groups, so 2 files.
	if len(written) != 2 {
		t.Fatalf("len(written) = %d, want 2; paths: %v", len(written), written)
	}

	for _, path := range written {
		if filepath.Dir(path) != filepath.Join(baseDir, "proposed_changes") {
			t.Errorf("file outside proposed_changes/: %s", path)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		var pc selfbuild.ProposedChange
		if err := yaml.Unmarshal(data, &pc); err != nil {
			t.Fatalf("unmarshal %s: %v", path, err)
		}
		if pc.Source != "usage-inference" {
			t.Errorf("Source = %q, want usage-inference", pc.Source)
		}
		if len(pc.DriftReports) == 0 {
			t.Errorf("no drift reports in %s", path)
		}
		for _, dr := range pc.DriftReports {
			if dr.RegisteredAfterSeq {
				t.Errorf("registered pair included in drift report: %+v", dr)
			}
		}
	}
}

func TestUsageInference_RegisteredPairSuppressed(t *testing.T) {
	baseDir := t.TempDir()
	cfg := selfbuild.UsageConfig{
		BaseDir:     baseDir,
		Window:      time.Hour,
		RunInterval: time.Hour,
	}
	// amos->miller is registered; provide only that event.
	events := []selfbuild.RelayEvent{
		{Actor: "amos", NextActor: "miller", ConversationKind: "troubleshoot", Timestamp: time.Now()},
	}
	u := selfbuild.NewUsageInference(cfg, newFixtureStore())

	written, err := u.Analyze(context.Background(), events)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(written) != 0 {
		t.Errorf("expected no drift for registered pair, got %d files", len(written))
	}
}

func TestUsageInference_EmptyActorSkipped(t *testing.T) {
	baseDir := t.TempDir()
	cfg := selfbuild.UsageConfig{
		BaseDir:     baseDir,
		Window:      time.Hour,
		RunInterval: time.Hour,
	}
	events := []selfbuild.RelayEvent{
		{Actor: "", NextActor: "naomi", ConversationKind: "merge"},
		{Actor: "amos", NextActor: "", ConversationKind: "merge"},
	}
	u := selfbuild.NewUsageInference(cfg, newFixtureStore())

	written, err := u.Analyze(context.Background(), events)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(written) != 0 {
		t.Errorf("expected no output for empty-actor events, got %d", len(written))
	}
}

func TestUsageInference_NoDirectRegistryWrite(t *testing.T) {
	baseDir := t.TempDir()
	cfg := selfbuild.UsageConfig{
		BaseDir:     baseDir,
		Window:      time.Hour,
		RunInterval: time.Hour,
	}
	events := []selfbuild.RelayEvent{
		{Actor: "x", NextActor: "y", ConversationKind: "test", Timestamp: time.Now()},
	}
	u := selfbuild.NewUsageInference(cfg, &mockStoreReader{})

	_, err := u.Analyze(context.Background(), events)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	entries, err := os.ReadDir(baseDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "proposed_changes" {
			t.Errorf("unexpected file in baseDir: %s", e.Name())
		}
	}
}
