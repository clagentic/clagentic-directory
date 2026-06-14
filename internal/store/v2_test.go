package store

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// testVocabYAML is a minimal vocabulary.v1.yaml that covers the values used in
// v2ValidYAML and all examples/registry/ entries. Tests that exercise strict
// validation must call writeTestVocab(t) to obtain a vocab file path.
const testVocabYAML = `schema_version: 1
intents:
  code_work_requested: "A code change task has been dispatched."
  code-generation: "Code generation from a spec."
  implement-task: "Implement a described task end-to-end."
  pr_opened: "A pull request has been opened."
  code_ready_for_review: "Code is ready for structured review."
  code-review: "A general code review."
  review-pr: "Review of a specific pull request."
  review-commit: "Review of a specific commit."
  merge_requested: "A merge has been requested."
  merge-pr: "Merge a specific pull request."
  release: "A release action has been requested."
  tag-release: "Tag-and-release."
  diagnostic_requested: "A diagnostic investigation."
  root_cause_unknown: "Root cause is unknown."
  escalation_diagnosis: "Diagnostic escalation."
  deploy_requested: "A deployment has been requested."
  runbook_run: "Execute a named runbook."
  ops_check: "An operational health check."
  research_requested: "A research task."
  survey_requested: "A survey task."
  research: "General research."
  investigate: "Investigation of a topic."
  find-information: "Find specific information."
  scaffold_requested: "A scaffolding task."
  new_project_setup: "New project setup."
  escalation: "Escalation for routing."
  portfolio_question: "Question about the agent portfolio."
  dispatch_routing: "Routing decision needed."
  web-research: "Fetch and synthesize from the web."
  web-search: "Run a web search."
  url-fetch: "Retrieve and summarize a URL."
  fact-lookup: "Look up a specific fact."
  doc-lookup: "Check official documentation."
  large-context-analysis: "Analyze large-context content."
  codebase-survey: "Read and summarize a large codebase."
  community-sentiment: "Research community opinion."
  reddit-research: "Search Reddit."
  user-opinion-research: "Gather user perspectives."
  deep-analysis: "Apply extended reasoning."
  architecture-review: "Evaluate a system design."
  security-review: "Analyze for security vulnerabilities."
  tradeoff-evaluation: "Weigh competing options."
  second-opinion: "Independent evaluation."
  delegate-to-codex: "Route to OpenAI Codex CLI."
  codex-review: "Adversarial review from Codex."
  gpt-reasoning: "Apply GPT-5.x reasoning."
  local-inference: "Run inference on local models."
  cheap-inference: "Use low-cost local models."
  offline-inference: "Run inference without external APIs."
  embeddings: "Generate vector embeddings."
  inspect-repo: "Inspect a repository advisory."
  harvest-intelligence: "Extract high-signal findings."
  ingest-candidate: "Run a user-approved ingest."
  probe: "Send a probe to verify wiring."
  wiring-test: "Test named-agent routing."
conversation_kinds:
  build: "A code-change workflow."
  consult: "A consulting or advisory session."
  smoke: "A lightweight smoke-test."
  gate: "A gate check."
  research: "A structured research session."
  review: "A code review session."
  deploy: "A deployment or release conversation."
  planning: "A planning or design session."
  directive: "An operator directive."
  escalation: "An escalation session."
  coordination: "A cross-agent coordination conversation."
  advisory: "An advisory session."
  code-generation: "A session whose primary output is generated code."
  classification: "A classification session."
  summarization: "A summarization session."
  design: "A design-level session."
  test: "A test or wiring-verification session."
trust_labels:
  read-only: "May read only."
  write-pr: "May open pull requests."
  write-ops: "May execute runbooks."
  merge-authority: "May merge pull requests."
  merge-gate: "Legacy alias of merge-authority."
  publish: "May publish artifacts."
  observe: "Observes events only."
  escalation-surface: "Escalation surface."
  dispatch-authority: "May dispatch to other agents."
  trusted: "Within established role boundaries."
  autonomous: "May act without per-action confirmation."
  high-stakes: "High-stakes decisions."
  external-model: "Delegates to a non-Claude model."
  external-source: "Fetches from external web sources."
  local-model: "Uses locally-hosted models."
  test-only: "For testing purposes only."
formats:
  json: "Structured JSON object."
  structured: "Structured object (YAML, JSON, or typed SDK envelope)."
  structured-markdown: "Markdown document with structured sections."
  url: "A URL string."
  text: "Unstructured plain text."
  agent-result-json: "JSON matching an agent_result envelope schema."
  verbatim-model-output: "Raw output of a delegated model."
  plaintext: "Simple unstructured text."
`

// writeTestVocab writes testVocabYAML to a temp file and returns its path.
// Tests that exercise strict vocabulary validation must call this.
func writeTestVocab(t *testing.T) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "vocabulary.v1.*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(testVocabYAML); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return f.Name()
}

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
	vocabPath := writeTestVocab(t)
	fs, err := NewFileStore(dir, vocabPath, VocabularyExtensions{})
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

// TestV2ValidateOpenAcceptsAnyValue verifies that without a vocab file (ValidateOpen),
// schema_version: 2 entries with unknown vocabulary values still load.
func TestV2ValidateOpenAcceptsAnyValue(t *testing.T) {
	dir := t.TempDir()
	openYAML := strings.ReplaceAll(v2ValidYAML, "code_work_requested", "not_a_real_intent_but_open_mode")
	if err := os.WriteFile(filepath.Join(dir, "test-agent.yaml"), []byte(openYAML), 0644); err != nil {
		t.Fatal(err)
	}
	// No vocab file: ValidateOpen mode — all values accepted.
	fs, err := NewFileStore(dir, "", VocabularyExtensions{})
	if err != nil {
		t.Fatalf("ValidateOpen mode should accept any value; got error: %v", err)
	}
	defer fs.Close()
	if len(fs.ListAgents()) != 1 {
		t.Errorf("expected 1 agent in ValidateOpen mode, got %d", len(fs.ListAgents()))
	}
}

// TestV2InvalidIntentFails verifies that an unknown intent causes a load error with a clear message.
func TestV2InvalidIntentFails(t *testing.T) {
	dir := t.TempDir()
	badYAML := strings.ReplaceAll(v2ValidYAML, "code_work_requested", "definitely_not_a_real_intent")
	if err := os.WriteFile(filepath.Join(dir, "bad-agent.yaml"), []byte(badYAML), 0644); err != nil {
		t.Fatal(err)
	}
	vocabPath := writeTestVocab(t)
	_, err := NewFileStore(dir, vocabPath, VocabularyExtensions{})
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
	vocabPath := writeTestVocab(t)
	_, err := NewFileStore(dir, vocabPath, VocabularyExtensions{})
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
	badYAML := strings.ReplaceAll(v2ValidYAML, "- write-pr", "- definitely-not-a-valid-trust-label")
	if err := os.WriteFile(filepath.Join(dir, "bad-trust.yaml"), []byte(badYAML), 0644); err != nil {
		t.Fatal(err)
	}
	vocabPath := writeTestVocab(t)
	_, err := NewFileStore(dir, vocabPath, VocabularyExtensions{})
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
	// Required-field checks run regardless of vocabulary mode.
	_, err := NewFileStore(dir, "", VocabularyExtensions{})
	if err == nil {
		t.Fatal("expected error for missing returns.verdict_field, got nil")
	}
	if !strings.Contains(err.Error(), "returns.verdict_field") {
		t.Errorf("error should mention returns.verdict_field; got: %v", err)
	}
}

// TestV2MultipleConflictsAggregated verifies that all vocabulary conflicts across
// an entry are reported together, not just the first one encountered.
func TestV2MultipleConflictsAggregated(t *testing.T) {
	dir := t.TempDir()
	// An entry with two bad values: one unknown intent, one unknown trust_label.
	badYAML := strings.ReplaceAll(v2ValidYAML, "code_work_requested", "bad_intent_one")
	badYAML = strings.ReplaceAll(badYAML, "- write-pr", "- bad-trust-label-one")
	if err := os.WriteFile(filepath.Join(dir, "multi-bad.yaml"), []byte(badYAML), 0644); err != nil {
		t.Fatal(err)
	}
	vocabPath := writeTestVocab(t)
	_, err := NewFileStore(dir, vocabPath, VocabularyExtensions{})
	if err == nil {
		t.Fatal("expected error for multiple conflicts, got nil")
	}
	ve, ok := err.(RegistryValidationErrors)
	if !ok {
		t.Fatalf("expected RegistryValidationErrors, got %T: %v", err, err)
	}
	if len(ve) < 2 {
		t.Errorf("expected at least 2 conflicts aggregated, got %d: %v", len(ve), ve)
	}
}

// TestLoadVocabularyKnownFields verifies that the vocabulary loader rejects
// unknown top-level keys (catches typos like "format" instead of "formats").
func TestLoadVocabularyKnownFields(t *testing.T) {
	badVocab := `schema_version: 1
intents:
  code_work_requested: "valid"
format:
  json: "This is a typo — should be 'formats'"
`
	f, err := os.CreateTemp(t.TempDir(), "vocab-bad-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(badVocab); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	_, err = loadVocabulary(f.Name())
	if err == nil {
		t.Fatal("expected error for unknown field 'format' (typo for 'formats'), got nil")
	}
	if !strings.Contains(err.Error(), "format") {
		t.Errorf("error should mention the unknown field; got: %v", err)
	}
}

// TestLoadVocabularyUnsupportedVersion verifies that a vocabulary file with an
// unsupported schema_version returns a clear error.
func TestLoadVocabularyUnsupportedVersion(t *testing.T) {
	badVocab := `schema_version: 99
intents:
  code_work_requested: "valid"
`
	f, err := os.CreateTemp(t.TempDir(), "vocab-badver-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(badVocab); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	_, err = loadVocabulary(f.Name())
	if err == nil {
		t.Fatal("expected error for unsupported schema_version 99, got nil")
	}
	if !strings.Contains(err.Error(), "schema_version") {
		t.Errorf("error should mention schema_version; got: %v", err)
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

	// v1 entries are not vocab-validated; pass empty vocabPath.
	fs, err := NewFileStore(dir, "", VocabularyExtensions{})
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

	vocabPath := writeTestVocab(t)
	fs, err := NewFileStore(dir, vocabPath, VocabularyExtensions{})
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

// TestExampleRegistryRoundTrip parses every YAML in examples/registry/ and verifies
// that all fields present in the YAML arrive non-nil/non-empty in the parsed Agent
// struct. This catches silent null-parse caused by nesting mismatches between YAML
// and Go structs (e.g. lr-a7fe: after_agents was silently left nil when nesting
// diverged between SCHEMA.md and rawTriggers).
func TestExampleRegistryRoundTrip(t *testing.T) {
	dir := "../../examples/registry"
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("examples/registry not found at %s: %v", dir, err)
	}

	for _, e := range entries {
		if e.IsDir() || (!strings.HasSuffix(e.Name(), ".yaml") && !strings.HasSuffix(e.Name(), ".yml")) {
			continue
		}
		e := e
		t.Run(e.Name(), func(t *testing.T) {
			path := filepath.Join(dir, e.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading %s: %v", path, err)
			}

			// Parse the raw YAML independently to know what fields are populated.
			var raw rawEntry
			if err := unmarshalYAML(data, &raw); err != nil {
				t.Fatalf("raw yaml.Unmarshal failed: %v", err)
			}

			// Parse via the full pipeline; nil vocab = ValidateOpen for the round-trip check.
			agent, err := parseEntry(path, data, nil)
			if err != nil {
				t.Fatalf("parseEntry failed: %v", err)
			}

			// Top-level identity fields.
			if raw.Identity.Name != "" && agent.Name == "" {
				t.Errorf("identity.name present in YAML but agent.Name is empty")
			}
			if raw.Identity.Version != "" && agent.Version == "" {
				t.Errorf("identity.version present in YAML but agent.Version is empty")
			}
			if raw.Identity.Description != "" && agent.Description == "" {
				t.Errorf("identity.description present in YAML but agent.Description is empty")
			}

			// Trust labels.
			if len(raw.TrustLabels) > 0 && len(agent.TrustLabels) == 0 {
				t.Errorf("trust_labels present in YAML (%v) but agent.TrustLabels is empty", raw.TrustLabels)
			}

			// Capabilities round-trip.
			if len(raw.Capabilities) != len(agent.Capabilities) {
				t.Errorf("capability count mismatch: YAML has %d, parsed has %d",
					len(raw.Capabilities), len(agent.Capabilities))
			}

			for i, rc := range raw.Capabilities {
				if i >= len(agent.Capabilities) {
					break
				}
				cap := agent.Capabilities[i]

				if rc.ID != "" && cap.ID == "" {
					t.Errorf("capability[%d]: id present in YAML but cap.ID is empty", i)
				}
				if rc.Description != "" && cap.Description == "" {
					t.Errorf("capability[%d] %q: description present in YAML but cap.Description is empty", i, rc.ID)
				}
				if rc.Returns.VerdictField != "" && cap.Returns.VerdictField == "" {
					t.Errorf("capability[%d] %q: returns.verdict_field present in YAML but cap.Returns.VerdictField is empty", i, rc.ID)
				}
				if rc.Returns.Format != "" && cap.Returns.Format == "" {
					t.Errorf("capability[%d] %q: returns.format present in YAML but cap.Returns.Format is empty", i, rc.ID)
				}

				// Trigger slices — the original bug: after_agents silently nil.
				if len(rc.Triggers.Intents) > 0 && len(cap.Triggers.Intents) == 0 {
					t.Errorf("capability[%d] %q: triggers.intents present in YAML (%v) but Triggers.Intents is empty",
						i, rc.ID, rc.Triggers.Intents)
				}
				if len(rc.Triggers.ConversationKinds) > 0 && len(cap.Triggers.ConversationKinds) == 0 {
					t.Errorf("capability[%d] %q: triggers.conversation_kinds present in YAML (%v) but Triggers.ConversationKinds is empty",
						i, rc.ID, rc.Triggers.ConversationKinds)
				}
				if len(rc.Triggers.AfterAgents) > 0 && len(cap.Triggers.AfterAgents) == 0 {
					t.Errorf("capability[%d] %q: triggers.after_agents present in YAML (%v) but Triggers.AfterAgents is nil — nesting mismatch (see lr-a7fe)",
						i, rc.ID, rc.Triggers.AfterAgents)
				}
				if len(rc.Triggers.AfterRoles) > 0 && len(cap.Triggers.AfterRoles) == 0 {
					t.Errorf("capability[%d] %q: triggers.after_roles present in YAML (%v) but Triggers.AfterRoles is nil",
						i, rc.ID, rc.Triggers.AfterRoles)
				}
			}
		})
	}
}

// unmarshalYAML parses raw YAML without the full validation pipeline,
// so tests can inspect what fields the YAML declares.
func unmarshalYAML(data []byte, v interface{}) error {
	return yaml.Unmarshal(data, v)
}

// TestAllFleetEntriesValidateAsV2 checks that every YAML in examples/registry/ is
// a valid schema_version: 2 entry. This test fails if any fleet entry has been left
// at v1 or uses vocabulary not in the test vocab.
func TestAllFleetEntriesValidateAsV2(t *testing.T) {
	// Walk up from this test file to find examples/registry.
	// This test assumes it runs from the module root (go test ./...).
	dir := "../../examples/registry"
	entries, err := os.ReadDir(dir)
	if err != nil {
		// If the directory doesn't exist (e.g. running from a different cwd), skip gracefully.
		t.Skipf("examples/registry not found at %s: %v", dir, err)
	}

	// Load the test vocabulary so we validate against the canonical value set.
	vocabPath := writeTestVocab(t)
	vocab, err := loadVocabulary(vocabPath)
	if err != nil {
		t.Fatalf("loading test vocabulary: %v", err)
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
		agent, err := parseEntry(path, data, vocab)
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
