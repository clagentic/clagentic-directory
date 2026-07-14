package store

import (
	"os"
	"path/filepath"
	"testing"
)

// writeAgentEntry writes a minimal schema_version: 2 agent YAML with the given
// name, role, and declared intents to dir, for use by findByCapability tests.
func writeAgentEntry(t *testing.T, dir, name, role string, intents []string) {
	t.Helper()
	intentYAML := ""
	for _, i := range intents {
		intentYAML += "        - " + i + "\n"
	}
	roleLine := ""
	if role != "" {
		roleLine = "  role: " + role + "\n"
	}
	y := "schema_version: 2\nidentity:\n  name: " + name + "\n  version: 1.0.0\n" + roleLine +
		"capabilities:\n  - id: do-thing\n    name: Do Thing\n    description: Does a thing.\n    triggers:\n      intents:\n" +
		intentYAML + "      conversation_kinds: []\n      after_roles: []\n      after_agents: []\n    returns:\n      verdict_field: result\n      format: text\n"
	if err := os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(y), 0644); err != nil {
		t.Fatal(err)
	}
}

// TestFindByCapabilityFallback is table-driven per the natural-language
// dispatcher scenario (lr resume task): natural queries like "build" or
// "review" must resolve agents via the synonym table or role fallback when
// no agent declares an exact intent match, without diluting genuine exact
// matches or genuine misses.
func TestFindByCapabilityFallback(t *testing.T) {
	dir := t.TempDir()
	// amos: builder role, declares the canonical code-generation intent.
	writeAgentEntry(t, dir, "amos", "builder", []string{"code_work_requested", "code-generation"})
	// peaches: reviewer role, declares the canonical code-review intent.
	writeAgentEntry(t, dir, "peaches", "reviewer", []string{"code-review", "review-pr"})
	// prax: researcher role, no exact or synonym-reachable intent declared,
	// used only for the role-fallback-by-name case.
	writeAgentEntry(t, dir, "prax", "researcher", []string{"survey_requested"})

	fs, err := NewFileStore(dir, "", VocabularyExtensions{})
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()

	tests := []struct {
		name    string
		query   string
		want    []string // expected agent names, order-independent
		wantLen int
	}{
		{
			name:  "exact hit still works",
			query: "code-generation",
			want:  []string{"amos"},
		},
		{
			name:  "build synonym resolves amos",
			query: "build",
			want:  []string{"amos"},
		},
		{
			name:  "review synonym resolves peaches",
			query: "review",
			want:  []string{"peaches"},
		},
		{
			name:  "role builder resolves amos",
			query: "builder",
			want:  []string{"amos"},
		},
		{
			name:  "role reviewer resolves peaches",
			query: "reviewer",
			want:  []string{"peaches"},
		},
		{
			name:  "role researcher resolves prax by name",
			query: "researcher",
			want:  []string{"prax"},
		},
		{
			name:    "genuine miss returns empty",
			query:   "no-such-intent-or-role",
			wantLen: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := fs.FindByCapability(tc.query)
			if len(tc.want) > 0 {
				if len(got) != len(tc.want) {
					t.Fatalf("FindByCapability(%q): expected %d agent(s) %v, got %d: %v",
						tc.query, len(tc.want), tc.want, len(got), namesOf(got))
				}
				for _, wantName := range tc.want {
					found := false
					for _, a := range got {
						if a.Name == wantName {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("FindByCapability(%q): expected agent %q in result, got %v",
							tc.query, wantName, namesOf(got))
					}
				}
			} else if len(got) != tc.wantLen {
				t.Errorf("FindByCapability(%q): expected %d agents, got %d: %v",
					tc.query, tc.wantLen, len(got), namesOf(got))
			}
		})
	}
}

// TestFindByCapabilityExactNeverDiluted verifies that when an exact intent
// match exists, the synonym and role fallback tiers never run (fallback
// results must not be merged into an exact-tier result set).
func TestFindByCapabilityExactNeverDiluted(t *testing.T) {
	dir := t.TempDir()
	// "build" is both a literal declared intent for one agent AND a synonym
	// key. The exact-tier match must win alone.
	writeAgentEntry(t, dir, "literal-build-agent", "", []string{"build"})
	writeAgentEntry(t, dir, "amos", "builder", []string{"code-generation"})

	fs, err := NewFileStore(dir, "", VocabularyExtensions{})
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()

	got := fs.FindByCapability("build")
	if len(got) != 1 || got[0].Name != "literal-build-agent" {
		t.Errorf("expected exact match to return only literal-build-agent, got %v", namesOf(got))
	}
}

func namesOf(agents []Agent) []string {
	out := make([]string, len(agents))
	for i, a := range agents {
		out[i] = a.Name
	}
	return out
}
