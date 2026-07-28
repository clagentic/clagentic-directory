package store

import (
	"fmt"
	"testing"
)

// goldenMetrics holds the recall@1, recall@3, and MRR scores for a run of
// goldenSet against a matcher function, plus enough detail to log a
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

// matcherFunc is the shape of both the tiers-1-3-only matcher (tiers1Through3)
// and the full tiered matcher including Tier 4 (findByCapability), so
// runGoldenSet can score either one against the same golden set.
type matcherFunc func(agents map[string]Agent, intents ...string) []Agent

// tiers1Through3 runs only the exact/synonym/role tiers, with no Tier 4 BM25
// fallback — this is the matcher AS IT EXISTED before this change, preserved
// here (rather than only in git history) so the historical baseline in
// TestGoldenSetBaseline_PreTier4 stays mechanically reproducible from source
// forever, independent of whether someone later modifies findByCapability.
func tiers1Through3(agents map[string]Agent, intents ...string) []Agent {
	normalized := make([]string, len(intents))
	for i, in := range intents {
		normalized[i] = normalizeIntent(in)
	}
	if out := matchByIntentSet(agents, normalized, false); len(out) > 0 {
		return rankAgents(out)
	}
	if out := matchByIntentSet(agents, normalized, true); len(out) > 0 {
		return rankAgents(out)
	}
	return rankAgents(matchByRole(agents, normalized))
}

// runGoldenSet evaluates every resolvable case in goldenSet against agents
// using the given matcher and returns aggregate recall@1/recall@3/MRR.
//
// containsName(wantAny, name) determines a hit — any agent in a case's
// wantAny set counts as correct (see goldenCase doc comment on shared-intent
// cases).
func runGoldenSet(agents map[string]Agent, matcher matcherFunc) goldenMetrics {
	var m goldenMetrics
	for _, c := range goldenSet {
		if c.goldenUnresolved() {
			m.unresolved++
			continue
		}
		m.total++
		got := matcher(agents, c.query)

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

// TestGoldenSetBaseline_PreTier4 measures recall@1 / recall@3 / MRR of the
// matcher AS IT EXISTED BEFORE Tier 4 (tiers 1-3 only: exact, synonym, role)
// against goldenSet, using the 18-agent live-registry fixture.
//
// This is the primary deliverable of lr-dab7e0 acceptance criteria 1-2: the
// number that makes "fixed" falsifiable. Two prior fixes (lr-9b7435,
// lr-044f4d) were closed as verified with no measurement, and the same
// failure class recurred both times. This test's t.Log output plus the
// committed BASELINE figures below are the permanent record of the
// pre-Tier-4 number — measured against tiers1Through3 (a frozen copy of the
// pre-Tier-4 matcher, see doc comment above) so this stays reproducible even
// after findByCapability itself changes further in the future.
//
// BASELINE (measured 2026-07-28, tiers 1-3 only, before Tier 4 BM25 was
// added in this same change; reproduced by running
// `go test ./internal/store/... -run TestGoldenSetBaseline_PreTier4 -v`):
//
//	recall@1: 36/62 = 58.1%
//	recall@3: 36/62 = 58.1%
//	MRR:      0.581
//	unresolved (excluded): 1 ("plan" — no avasarala agent in current registry)
//
// This test only asserts the golden set itself is non-degenerate; it
// intentionally does NOT assert a minimum recall bar for the tiers-1-3-only
// matcher, since acceptance criterion 5 (>=95% recall@1) applies to the
// POST-Tier-4 matcher. See TestGoldenSetRegressionGate for the enforced bar.
func TestGoldenSetBaseline_PreTier4(t *testing.T) {
	agents := goldenFixtureAgents()
	m := runGoldenSet(agents, tiers1Through3)

	t.Logf("BASELINE (tiers 1-3 only, pre-Tier-4 matcher): recall@1=%d/%d (%.1f%%) recall@3=%d/%d (%.1f%%) MRR=%.3f unresolved=%d",
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

// postTier4RecallAt1Baseline is the recall@1 measured against the full
// matcher (tiers 1-4, including BM25) at the time Tier 4 was added
// (2026-07-28). TestGoldenSetRegressionGate fails if recall@1 drops by more
// than goldenRegressionTolerance from this figure, per lr-dab7e0 acceptance
// criterion 6 (CI regression gate).
//
// Measured value: 53/62 = 85.5%. This is BELOW the 95% target in acceptance
// criterion 5. Per lr-dab7e0's explicit instruction, this is reported as a
// measured result, not closed the gap with hand-tuned synonyms or a
// hand-tuned golden set — see the task's HARD CONSTRAINT. The remaining
// misses (see TestGoldenSetRegressionGate's logged breakdown) are genuine
// lexical gaps: e.g. "debug" has zero token overlap with any agent's
// registered description text (miller's description says "troubleshooting"
// and "diagnosis", never "debug"), which is a registry-content gap
// (clagentic-config Defect B, lr-3834c4, out of scope here), not a matcher
// defect BM25 can close on its own.
const postTier4RecallAt1Baseline = 53.0 / 62.0

// goldenRegressionTolerance is the maximum allowed recall@1 drop (as a
// fraction, e.g. 0.02 = 2 percentage points) before the CI gate fails, per
// lr-dab7e0 acceptance criterion 6 ("failure criteria recall@1 drop > 2%").
const goldenRegressionTolerance = 0.02

// TestGoldenSetRegressionGate is the CI regression gate for lr-dab7e0
// acceptance criterion 6. It measures the full matcher (tiers 1-4) against
// goldenSet and fails if recall@1 drops by more than
// goldenRegressionTolerance from postTier4RecallAt1Baseline.
//
// This runs in the existing `go test ./...` suite rather than a new CI
// workflow: this repo has no Forgejo/GitHub Actions runner wired up yet
// (.crew/naomi.yaml sets merge_requirements.ci_pass: false, see lr-2a849c),
// and a workflow file would need a runs-on label matching a registered
// runner that does not currently exist — inventing one would violate the
// runner-setup skill's guidance. A go test regression test that runs in the
// existing suite is the cleaner path per this task's own guidance.
func TestGoldenSetRegressionGate(t *testing.T) {
	agents := goldenFixtureAgents()
	m := runGoldenSet(agents, findByCapability)

	t.Logf("CURRENT (tiers 1-4, full matcher): recall@1=%d/%d (%.1f%%) recall@3=%d/%d (%.1f%%) MRR=%.3f unresolved=%d",
		m.hitAt1, m.total, m.recallAt1()*100,
		m.hitAt3, m.total, m.recallAt3()*100,
		m.mrr(), m.unresolved)
	for _, miss := range m.misses {
		t.Logf("  MISS: %s", miss)
	}

	drop := postTier4RecallAt1Baseline - m.recallAt1()
	if drop > goldenRegressionTolerance {
		t.Errorf("recall@1 regressed by %.1f%% (from %.1f%% to %.1f%%), exceeding the %.1f%% tolerance (lr-dab7e0 acceptance criterion 6)",
			drop*100, postTier4RecallAt1Baseline*100, m.recallAt1()*100, goldenRegressionTolerance*100)
	}
}
