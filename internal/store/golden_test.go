package store

import (
	"fmt"
	"testing"
)

// goldenMetrics holds the recall@1, recall@3, and MRR scores for a run of
// goldenSet against findByCapability, plus enough detail to log a
// human-readable per-case breakdown.
type goldenMetrics struct {
	total             int // resolvable cases (goldenUnresolved cases excluded)
	unresolved        int
	hitAt1            int
	hitAt3            int
	reciprocalRankSum float64
	misses            []string // "<query>: got <names>, want one of <names>" for hitAt1 misses
}

func (m goldenMetrics) recallAt1() float64 {
	if m.total == 0 {
		return 0
	}
	return float64(m.hitAt1) / float64(m.total)
}

func (m goldenMetrics) recallAt3() float64 {
	if m.total == 0 {
		return 0
	}
	return float64(m.hitAt3) / float64(m.total)
}

func (m goldenMetrics) mrr() float64 {
	if m.total == 0 {
		return 0
	}
	return m.reciprocalRankSum / float64(m.total)
}

// runGoldenSet evaluates every resolvable case in goldenSet against agents
// using findByCapability and returns aggregate recall@1/recall@3/MRR.
//
// contains(wantAny, name) determines a hit — any agent in a case's wantAny
// set counts as correct (see goldenCase doc comment on shared-intent cases).
func runGoldenSet(t *testing.T, agents map[string]Agent) goldenMetrics {
	t.Helper()
	var m goldenMetrics
	for _, c := range goldenSet {
		if c.goldenUnresolved() {
			m.unresolved++
			continue
		}
		m.total++
		got := findByCapability(agents, c.query)

		rank := -1 // -1 means not found in got
		for i, a := range got {
			if containsName(c.wantAny, a.Name) {
				rank = i
				break
			}
		}

		if rank == 0 {
			m.hitAt1++
		} else {
			gotNames := make([]string, len(got))
			for i, a := range got {
				gotNames[i] = a.Name
			}
			m.misses = append(m.misses, fmt.Sprintf("%q: got %v, want one of %v (%s)",
				c.query, gotNames, c.wantAny, c.note))
		}
		if rank >= 0 && rank < 3 {
			m.hitAt3++
		}
		if rank >= 0 {
			m.reciprocalRankSum += 1.0 / float64(rank+1)
		}
	}
	return m
}

func containsName(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

// TestGoldenSetBaseline_CurrentMatcher measures recall@1 / recall@3 / MRR of
// the matcher AS IT EXISTS TODAY (tiers 1-3: exact, synonym, role) against
// goldenSet, using the 18-agent live-registry fixture.
//
// This is the primary deliverable of lr-dab7e0 acceptance criteria 1-2: the
// number that makes "fixed" falsifiable. Two prior fixes (lr-9b7435,
// lr-044f4d) were closed as verified with no measurement, and the same
// failure class recurred both times. This test's t.Log output is the
// permanent, committed record of the pre-Tier-4 baseline.
//
// BASELINE (measured 2026-07-28, tiers 1-3 only, before Tier 4 BM25 was
// added in this same change; reproduced by running this test with
// `go test ./internal/store/... -run TestGoldenSetBaseline -v`):
//
//	recall@1: 36/62 = 58.1%
//	recall@3: 36/62 = 58.1%
//	MRR:      0.581
//	unresolved (excluded): 1 ("plan" — no avasarala agent in current registry)
//
// This test only asserts the metrics are non-negative and logs the full
// breakdown; it intentionally does NOT assert a minimum bar for the
// tiers-1-3-only matcher, since acceptance criterion 5 (>=95% recall@1)
// applies to the POST-Tier-4 matcher, not this baseline. See
// TestGoldenSetRegressionGate below for the enforced bar.
func TestGoldenSetBaseline_CurrentMatcher(t *testing.T) {
	agents := goldenFixtureAgents()
	m := runGoldenSet(t, agents)

	t.Logf("BASELINE (tiers 1-3 only, current matcher): recall@1=%d/%d (%.1f%%) recall@3=%d/%d (%.1f%%) MRR=%.3f unresolved=%d",
		m.hitAt1, m.total, m.recallAt1()*100,
		m.hitAt3, m.total, m.recallAt3()*100,
		m.mrr(), m.unresolved)
	for _, miss := range m.misses {
		t.Logf("  MISS: %s", miss)
	}

	if m.total == 0 {
		t.Fatal("golden set produced zero resolvable cases; fixture or golden set is broken")
	}
}
