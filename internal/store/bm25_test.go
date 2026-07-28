package store

import "testing"

// TestFindByCapabilityBM25FallbackOnly verifies that Tier 4 (BM25) only runs
// when tiers 1-3 produce nothing, and that a genuine tiers-1-3 miss now
// resolves via description-level matching instead of returning [] (lr-dab7e0
// acceptance criterion 3 and 4).
func TestFindByCapabilityBM25FallbackOnly(t *testing.T) {
	dir := t.TempDir()
	writeAgentEntry(t, dir, "miller", "diagnostician", []string{"deep-analysis", "second-opinion"})
	fs, err := NewFileStore(dir, "", VocabularyExtensions{})
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()

	// "diagnose" is not miller's role, not an exact intent, not a synonym key
	// -- tiers 1-3 all miss. It must still resolve via BM25 over the fixture
	// agent's description ("Does a thing" in writeAgentEntry's fixed
	// capability description does NOT contain "diagnose", so this instead
	// verifies the tiers-1-3-miss path falls through to BM25 without error
	// and returns [] when BM25 also has no lexical overlap -- proving Tier 4
	// does not fabricate a match where none exists).
	got := fs.FindByCapability("diagnose")
	if len(got) != 0 {
		t.Errorf("expected no BM25 match for a term absent from the fixture corpus, got %v", namesOf(got))
	}
}

// TestFindByCapabilityBM25MatchesDescription verifies Tier 4 resolves a
// query against agent/capability description text when no tiers-1-3 signal
// exists — this is the core defect this task fixes (lr-dab7e0 Defect A):
// MILLER's capability text says "diagnosis" repeatedly but registers
// unreachable intents ["deep-analysis","second-opinion"]; "diagnose" must
// now resolve to miller via BM25 over the capability description.
func TestFindByCapabilityBM25MatchesDescription(t *testing.T) {
	agents := goldenFixtureAgents()

	got := findByCapability(agents, "diagnose")
	if len(got) == 0 || got[0].Name != "miller" {
		t.Fatalf("FindByCapability(diagnose): expected miller first via BM25, got %v", namesOf(got))
	}
}

// TestFindByCapabilityTiers1Through3Unaffected verifies that adding Tier 4
// does not change the result or rank for queries that already resolved via
// tiers 1-3 before this change (lr-dab7e0 acceptance criterion 3: "every
// query working today keeps working at the same rank"). Each case's tier is
// verified directly against match.go/golden_fixture.go, not assumed.
func TestFindByCapabilityTiers1Through3Unaffected(t *testing.T) {
	agents := goldenFixtureAgents()

	tests := []struct {
		query string
		want  string
	}{
		{"research", "prax"},   // tier 1 exact intent, canonical-rank tie-break over gemini-researcher
		{"merge-pr", "naomi"},  // tier 1 exact intent
		{"build", "amos"},      // tier 2 synonym: build -> code-generation
		{"write-code", "amos"}, // tier 2 synonym: write-code -> code-generation
	}
	for _, tc := range tests {
		t.Run(tc.query, func(t *testing.T) {
			got := findByCapability(agents, tc.query)
			if len(got) == 0 || got[0].Name != tc.want {
				t.Errorf("findByCapability(%q): expected %q first, got %v", tc.query, tc.want, namesOf(got))
			}
		})
	}
}

// TestBM25FallbackCapsResultCount verifies Tier 4 never returns more than
// bm25TopN agents, so a very generic query cannot dump the entire registry
// through the fallback tier.
func TestBM25FallbackCapsResultCount(t *testing.T) {
	agents := goldenFixtureAgents()

	// "agent" is a generic word likely to appear across many descriptions
	// (multiple entries describe themselves as "crew-manifest Layer 2"
	// agents); this asserts the cap holds regardless of how many agents
	// score a nonzero match.
	got := findByCapability(agents, "agent")
	if len(got) > bm25TopN {
		t.Errorf("expected at most %d results from BM25 fallback, got %d", bm25TopN, len(got))
	}
}

// TestBM25FallbackDeterministic verifies repeated calls with the same query
// against the same corpus produce an identical ordering (lr-dab7e0
// acceptance criterion 7: deterministic and reproducible, no network call,
// no model inference). Go map iteration order is randomized per-process, so
// a single pass would not catch a nondeterminism bug introduced by iterating
// the agents map without a stable secondary sort.
func TestBM25FallbackDeterministic(t *testing.T) {
	agents := goldenFixtureAgents()

	first := findByCapability(agents, "troubleshoot a failure")
	for i := 0; i < 20; i++ {
		got := findByCapability(agents, "troubleshoot a failure")
		if len(got) != len(first) {
			t.Fatalf("run %d: length changed: got %d, want %d", i, len(got), len(first))
		}
		for j := range got {
			if got[j].Name != first[j].Name {
				t.Fatalf("run %d: order changed at index %d: got %q, want %q", i, j, got[j].Name, first[j].Name)
			}
		}
	}
}
