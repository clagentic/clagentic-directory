package store

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// v2ValidYAML is a minimal valid schema_version: 2 agent entry.
const v2ValidYAML = `schema_version: 2
identity:
  name: test-agent
  version: 1.0.0
  description: A test agent for schema v2 validation.
capabilities:
  - id: do-work
    name: Do Work
    description: Does test work.
    triggers:
      intents:
        - code_work_requested
      conversation_kinds:
        - build
      after_roles: []
      after_agents: []
    returns:
      verdict_field: test_result
      format: json
trust_labels:
  - write-pr
`

// TestV2ValidEntryLoads verifies that a fully-valid schema_version: 2 entry loads cleanly.
func TestV2ValidEntryLoads(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "test-agent.yaml"), []byte(v2ValidYAML), 0644); err != nil {
		t.Fatal(err)
	}
	fs, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("expected clean load, got error: %v", err)
	}
	defer fs.Close()

	agents := fs.ListAgents()
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}
	if agents[0].SchemaVersion != 2 {
		t.Errorf("expected SchemaVersion 2, got %d", agents[0].SchemaVersion)
	}
}

// TestV2InvalidIntentFails verifies that an unknown intent causes a load error with a clear message.
func TestV2InvalidIntentFails(t *testing.T) {
	dir := t.TempDir()
	badYAML := strings.ReplaceAll(v2ValidYAML, "code_work_requested", "definitely_not_a_real_intent")
	if err := os.WriteFile(filepath.Join(dir, "bad-agent.yaml"), []byte(badYAML), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := NewFileStore(dir)
	if err == nil {
		t.Fatal("expected error for unknown intent, got nil")
	}
	if !strings.Contains(err.Error(), "definitely_not_a_real_intent") {
		t.Errorf("error should name the offending value; got: %v", err)
	}
	if !strings.Contains(err.Error(), "triggers.intents") {
		t.Errorf("error should name the offending field; got: %v", err)
	}
}

// TestV2InvalidConversationKindFails verifies that an unknown conversation_kind fails with a clear error.
func TestV2InvalidConversationKindFails(t *testing.T) {
	dir := t.TempDir()
	badYAML := strings.ReplaceAll(v2ValidYAML, "- build", "- not_a_valid_kind")
	if err := os.WriteFile(filepath.Join(dir, "bad-kind.yaml"), []byte(badYAML), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := NewFileStore(dir)
	if err == nil {
		t.Fatal("expected error for unknown conversation_kind, got nil")
	}
	if !strings.Contains(err.Error(), "not_a_valid_kind") {
		t.Errorf("error should name the offending value; got: %v", err)
	}
	if !strings.Contains(err.Error(), "triggers.conversation_kinds") {
		t.Errorf("error should name the offending field; got: %v", err)
	}
}

// TestV2InvalidTrustLabelFails verifies that an unknown trust_label fails with a clear error.
func TestV2InvalidTrustLabelFails(t *testing.T) {
	dir := t.TempDir()
	// Use a sentinel that is not in the v2 enum and never will be.
	// (Do not use "trusted" here — that was added to v2ValidTrustLabels in lr-e391.)
	badYAML := strings.ReplaceAll(v2ValidYAML, "- write-pr", "- definitely-not-a-valid-trust-label")
	if err := os.WriteFile(filepath.Join(dir, "bad-trust.yaml"), []byte(badYAML), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := NewFileStore(dir)
	if err == nil {
		t.Fatal("expected error for unknown trust_label, got nil")
	}
	if !strings.Contains(err.Error(), "definitely-not-a-valid-trust-label") {
		t.Errorf("error should name the offending value; got: %v", err)
	}
	if !strings.Contains(err.Error(), "trust_labels") {
		t.Errorf("error should name the offending field; got: %v", err)
	}
}

// TestV2MissingReturnsVerdictFieldFails verifies that a missing returns.verdict_field fails.
func TestV2MissingReturnsVerdictFieldFails(t *testing.T) {
	dir := t.TempDir()
	badYAML := strings.ReplaceAll(v2ValidYAML, "verdict_field: test_result", "verdict_field: \"\"")
	if err := os.WriteFile(filepath.Join(dir, "no-verdict.yaml"), []byte(badYAML), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := NewFileStore(dir)
	if err == nil {
		t.Fatal("expected error for missing returns.verdict_field, got nil")
	}
	if !strings.Contains(err.Error(), "returns.verdict_field") {
		t.Errorf("error should mention returns.verdict_field; got: %v", err)
	}
}

// TestV1EntryLoadsWithDeprecationWarning verifies that a schema_version: 1 entry loads cleanly
// but emits a deprecation warning via slog.
func TestV1EntryLoadsWithDeprecationWarning(t *testing.T) {
	dir := t.TempDir()
	v1YAML := `schema_version: 1
identity:
  name: legacy-agent
  version: 1.0.0
  description: A legacy v1 agent.
capabilities:
  - id: old-capability
    name: Old Capability
    description: Does old things.
    triggers:
      intents: [any_old_intent]
      conversation_kinds: [any_old_kind]
      after_roles: []
      after_agents: []
    returns:
      verdict_field: old_result
      format: json
trust_labels:
  - trusted
`
	if err := os.WriteFile(filepath.Join(dir, "legacy-agent.yaml"), []byte(v1YAML), 0644); err != nil {
		t.Fatal(err)
	}

	// Capture slog output.
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
	orig := slog.Default()
	slog.SetDefault(slog.New(handler))
	defer slog.SetDefault(orig)

	fs, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("v1 entry must load without error; got: %v", err)
	}
	defer fs.Close()

	agents := fs.ListAgents()
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}
	if agents[0].Name != "legacy-agent" {
		t.Errorf("expected name legacy-agent, got %s", agents[0].Name)
	}
	if agents[0].SchemaVersion != 1 {
		t.Errorf("expected SchemaVersion 1, got %d", agents[0].SchemaVersion)
	}

	// The deprecation warning must appear in slog output.
	logged := buf.String()
	if !strings.Contains(logged, "schema_version 1") {
		t.Errorf("expected deprecation warning mentioning schema_version 1; slog output was: %s", logged)
	}
	if !strings.Contains(logged, "lr-1745") {
		t.Errorf("expected deprecation warning referencing lr-1745; slog output was: %s", logged)
	}
}

// TestV1AndV2BackwardCompatShape verifies that v1 and v2 entries expose the same shape
// to callers (same Agent struct fields populated).
func TestV1AndV2BackwardCompatShape(t *testing.T) {
	dir := t.TempDir()

	v1YAML := `schema_version: 1
identity:
  name: v1-agent
  version: 1.0.0
  description: Legacy agent.
capabilities:
  - id: do-thing
    name: Do Thing
    description: Does a thing.
    triggers:
      intents: [research]
      conversation_kinds: [consult]
      after_roles: []
      after_agents: []
    returns:
      verdict_field: v1_result
      format: json
trust_labels:
  - read-only
`
	v2YAML := `schema_version: 2
identity:
  name: v2-agent
  version: 1.0.0
  description: Modern agent.
capabilities:
  - id: do-thing
    name: Do Thing
    description: Does a thing.
    triggers:
      intents:
        - research
      conversation_kinds:
        - consult
      after_roles: []
      after_agents: []
    returns:
      verdict_field: v2_result
      format: json
trust_labels:
  - read-only
`
	if err := os.WriteFile(filepath.Join(dir, "v1-agent.yaml"), []byte(v1YAML), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "v2-agent.yaml"), []byte(v2YAML), 0644); err != nil {
		t.Fatal(err)
	}

	// Suppress the v1 deprecation warning.
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError})
	orig := slog.Default()
	slog.SetDefault(slog.New(handler))
	defer slog.SetDefault(orig)

	fs, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("mixed v1+v2 store must load without error; got: %v", err)
	}
	defer fs.Close()

	// Both agents must be findable by the same capability query.
	found := fs.FindByCapability("research")
	if len(found) != 2 {
		t.Errorf("FindByCapability(research): expected 2 agents (v1+v2), got %d", len(found))
	}

	found = fs.FindByConversationKind("consult")
	if len(found) != 2 {
		t.Errorf("FindByConversationKind(consult): expected 2 agents (v1+v2), got %d", len(found))
	}

	// Both must have the same non-empty field shape.
	for _, a := range fs.ListAgents() {
		if len(a.Capabilities) != 1 {
			t.Errorf("agent %s: expected 1 capability, got %d", a.Name, len(a.Capabilities))
		}
		if len(a.TrustLabels) != 1 {
			t.Errorf("agent %s: expected 1 trust_label, got %d", a.Name, len(a.TrustLabels))
		}
		cap0 := a.Capabilities[0]
		if cap0.Returns.VerdictField == "" {
			t.Errorf("agent %s: returns.verdict_field must be non-empty", a.Name)
		}
		if cap0.Returns.Format == "" {
			t.Errorf("agent %s: returns.format must be non-empty", a.Name)
		}
	}
}

// TestAllFleetEntriesValidateAsV2 checks that every YAML in examples/registry/ is
// a valid schema_version: 2 entry. This test fails if any fleet entry has been left
// at v1 or uses vocabulary not in the v2 enum.
func TestAllFleetEntriesValidateAsV2(t *testing.T) {
	// Walk up from this test file to find examples/registry.
	// This test assumes it runs from the module root (go test ./...).
	dir := "../../examples/registry"
	entries, err := os.ReadDir(dir)
	if err != nil {
		// If the directory doesn't exist (e.g. running from a different cwd), skip gracefully.
		t.Skipf("examples/registry not found at %s: %v", dir, err)
	}

	// Suppress v1 deprecation warnings; we will fail on v1 entries explicitly below.
	var buf bytes.Buffer
	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelError})
	orig := slog.Default()
	slog.SetDefault(slog.New(handler))
	defer slog.SetDefault(orig)

	for _, e := range entries {
		if e.IsDir() || (!strings.HasSuffix(e.Name(), ".yaml") && !strings.HasSuffix(e.Name(), ".yml")) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("reading %s: %v", path, err)
			continue
		}
		agent, err := parseEntry(path, data)
		if err != nil {
			t.Errorf("fleet entry %s failed to parse: %v", e.Name(), err)
			continue
		}
		if agent.SchemaVersion != 2 {
			t.Errorf("fleet entry %s is schema_version: %d; all entries must be schema_version: 2 (see lr-1745)",
				e.Name(), agent.SchemaVersion)
		}
	}
}
