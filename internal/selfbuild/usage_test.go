package selfbuild_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/clagentic/clagentic-directory/internal/selfbuild"
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

// Fixture events: builder always precedes reviewer, but reviewer->merge-gate is NOT registered.
var usageFixtureEvents = []selfbuild.StoreEvent{
	{Actor: "builder", NextActor: "reviewer", ConversationKind: "code-review", Timestamp: time.Now()},
	{Actor: "builder", NextActor: "reviewer", ConversationKind: "code-review", Timestamp: time.Now()},
	{Actor: "reviewer", NextActor: "merge-gate", ConversationKind: "code-review", Timestamp: time.Now()},
	{Actor: "reviewer", NextActor: "merge-gate", ConversationKind: "code-review", Timestamp: time.Now()},
	{Actor: "reviewer", NextActor: "merge-gate", ConversationKind: "code-review", Timestamp: time.Now()},
	// This one IS registered — should not produce drift.
	{Actor: "builder", NextActor: "diagnostician", ConversationKind: "troubleshoot", Timestamp: time.Now()},
}

func newFixtureStore() *mockStoreReader {
	return &mockStoreReader{
		registered: map[string][]string{
			// builder->diagnostician is registered; builder->reviewer is NOT.
			"builder": {"diagnostician"},
			// reviewer has no registered after_agents.
		},
	}
}

func TestUsageInference_Analyze(t *testing.T) {
	baseDir := t.TempDir()
	cfg := selfbuild.UsageConfig{
		EventStoreURL: "http://localhost:8445",
		BaseDir:       baseDir,
		Window:        time.Hour,
		RunInterval:   time.Hour,
	}
	u := selfbuild.NewUsageInference(cfg, newFixtureStore())

	written, err := u.Analyze(context.Background(), usageFixtureEvents)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	// Expect drift for: builder->reviewer (unregistered) and reviewer->merge-gate (unregistered).
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
	// builder->diagnostician is registered; provide only that event.
	events := []selfbuild.StoreEvent{
		{Actor: "builder", NextActor: "diagnostician", ConversationKind: "troubleshoot", Timestamp: time.Now()},
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
	events := []selfbuild.StoreEvent{
		{Actor: "", NextActor: "merge-gate", ConversationKind: "merge"},
		{Actor: "builder", NextActor: "", ConversationKind: "merge"},
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
	events := []selfbuild.StoreEvent{
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

// TestUsageInference_ResearchFirstFlag_SetForLeadWithoutSearch verifies that
// ResearchFirstFlag is set in the drift report when the actor is a lead/director
// with no recorded prior-context search in the event window.
func TestUsageInference_ResearchFirstFlag_SetForLeadWithoutSearch(t *testing.T) {
	baseDir := t.TempDir()
	cfg := selfbuild.UsageConfig{
		BaseDir:     baseDir,
		Window:      time.Hour,
		RunInterval: time.Hour,
	}
	// project-lead is a lead (ActorRole="lead") with no LastContextSearchAt — flag should fire.
	events := []selfbuild.StoreEvent{
		{Actor: "project-lead", NextActor: "builder", ConversationKind: "build", Timestamp: time.Now(),
			ActorRole: "lead", LastContextSearchAt: ""},
		{Actor: "project-lead", NextActor: "builder", ConversationKind: "build", Timestamp: time.Now(),
			ActorRole: "lead", LastContextSearchAt: ""},
	}
	u := selfbuild.NewUsageInference(cfg, &mockStoreReader{})

	written, err := u.Analyze(context.Background(), events)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("expected 1 drift report, got %d", len(written))
	}

	data, err := os.ReadFile(written[0])
	if err != nil {
		t.Fatalf("read drift report: %v", err)
	}
	var pc selfbuild.ProposedChange
	if err := yaml.Unmarshal(data, &pc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(pc.DriftReports) == 0 {
		t.Fatal("expected at least one drift report")
	}
	if !pc.DriftReports[0].ResearchFirstFlag {
		t.Error("expected ResearchFirstFlag=true for lead with no prior-context search")
	}
	// Also verify the RESEARCH-FIRST note appears in Notes.
	hasNote := false
	for _, note := range pc.Notes {
		if len(note) > 10 && note[:10] == "RESEARCH-F" {
			hasNote = true
			break
		}
	}
	if !hasNote {
		t.Errorf("expected RESEARCH-FIRST note in drift report Notes; got: %v", pc.Notes)
	}
}

// TestUsageInference_ResearchFirstFlag_ClearForLeadWithSearch verifies that
// ResearchFirstFlag is NOT set when the actor has a recorded prior-context search.
func TestUsageInference_ResearchFirstFlag_ClearForLeadWithSearch(t *testing.T) {
	baseDir := t.TempDir()
	cfg := selfbuild.UsageConfig{
		BaseDir:     baseDir,
		Window:      time.Hour,
		RunInterval: time.Hour,
	}
	// project-lead with a prior-context search recorded — flag should NOT fire.
	events := []selfbuild.StoreEvent{
		{Actor: "project-lead", NextActor: "builder", ConversationKind: "build", Timestamp: time.Now(),
			ActorRole: "lead", LastContextSearchAt: "2026-05-17T12:00:00Z"},
	}
	u := selfbuild.NewUsageInference(cfg, &mockStoreReader{})

	written, err := u.Analyze(context.Background(), events)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(written) != 1 {
		t.Fatalf("expected 1 drift report (unregistered sequence), got %d", len(written))
	}

	data, err := os.ReadFile(written[0])
	if err != nil {
		t.Fatalf("read drift report: %v", err)
	}
	var pc selfbuild.ProposedChange
	if err := yaml.Unmarshal(data, &pc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(pc.DriftReports) == 0 {
		t.Fatal("expected at least one drift report")
	}
	if pc.DriftReports[0].ResearchFirstFlag {
		t.Error("expected ResearchFirstFlag=false for lead with a recorded prior-context search")
	}
}

// TestUsageInference_ResearchFirstFlag_ClearForCrew verifies that
// ResearchFirstFlag is NOT set for crew agents.
func TestUsageInference_ResearchFirstFlag_ClearForCrew(t *testing.T) {
	baseDir := t.TempDir()
	cfg := selfbuild.UsageConfig{
		BaseDir:     baseDir,
		Window:      time.Hour,
		RunInterval: time.Hour,
	}
	// builder is crew — no research-first flag regardless of prior-context search status.
	events := []selfbuild.StoreEvent{
		{Actor: "builder", NextActor: "merge-gate", ConversationKind: "build", Timestamp: time.Now(),
			ActorRole: "crew", LastContextSearchAt: ""},
	}
	u := selfbuild.NewUsageInference(cfg, &mockStoreReader{})

	written, err := u.Analyze(context.Background(), events)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if len(written) == 0 {
		// No drift report means no sequencing mismatch. In any case the flag should
		// not fire for crew agents.
		return
	}
	data, err := os.ReadFile(written[0])
	if err != nil {
		t.Fatalf("read drift report: %v", err)
	}
	var pc selfbuild.ProposedChange
	if err := yaml.Unmarshal(data, &pc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, dr := range pc.DriftReports {
		if dr.Actor == "builder" && dr.ResearchFirstFlag {
			t.Error("expected ResearchFirstFlag=false for crew agent")
		}
	}
}
