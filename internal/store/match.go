package store

import (
	"sort"
	"strings"
)

// intentSynonyms maps a natural-language query term to the canonical intent
// it aliases. Used by findByCapability as a fallback tier when no agent
// declares an exact match on the raw query intent(s).
//
// Keys are stored pre-normalized (see normalizeIntent) so lookups only need
// to normalize the incoming query term, not every map key at call time.
//
// Keep in sync with docs/VOCABULARY.md's intent table: a synonym must alias
// to a canonical value that already exists in the enum.
var intentSynonyms = map[string]string{
	"build":          "code-generation",
	"implement":      "code-generation",
	"write-code":     "code-generation",
	"code":           "code-generation",
	"fix":            "code-generation",
	"refactor":       "code-generation",
	"feature":        "code-generation",
	"review":         "code-review",
	"second-opinion": "code-review",
	"security":       "security-review",
	"audit":          "security-review",
	"merge":          "merge-pr",
	"investigate":    "research",
	"lookup":         "research",
}

// canonicalRankTrustLabels lists trust labels that mark an agent as the
// canonical handler for a shared intent, in descending rank priority. An
// agent carrying an earlier label in this list outranks one that only
// carries a later label or none of them (lr-044f4d: canonical crew agents
// such as prax must sort ahead of their own fallback engines such as
// gemini-researcher when both declare the same intent).
//
// "trusted" ranks canonical crew agents; "external-model" and
// "external-source" are intentionally absent so agents carrying only those
// labels always rank behind any "trusted" agent for a shared intent.
var canonicalRankTrustLabels = []string{"trusted"}

// normalizeIntent lowercases and collapses whitespace/underscore separators
// to hyphens so that natural-language query phrasings ("write code",
// "write_code") resolve the same registered intent as the canonical
// hyphenated token ("write-code"). This runs on every raw query intent
// before exact, synonym, and role matching.
func normalizeIntent(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.Join(strings.Fields(s), "-") // collapse runs of whitespace to a single hyphen
	s = strings.ReplaceAll(s, "_", "-")
	return s
}

// bm25TopN caps how many agents Tier 4 returns. /v1/find must never return a
// bare [] (lr-dab7e0 acceptance criterion 4); this also keeps the fallback
// tier from dumping the entire registry when a query is so generic it
// scores against most agents' descriptions.
const bm25TopN = 5

// findByCapability implements the shared FindByCapability matching contract
// for both FileStore and GitStore. It is tiered:
//  1. Exact match: an agent capability declares one of the raw query intents
//     (after normalization).
//  2. Synonym match: a normalized query intent aliases (via intentSynonyms)
//     to a canonical intent an agent capability declares.
//  3. Role match: a normalized query intent equals an agent's declared Role.
//  4. BM25 fallback: when tiers 1-3 return nothing, ranks every agent by
//     Okapi BM25 score over agent name + description + capability
//     name/id/description against the raw query tokens, and returns the
//     top bm25TopN agents with a nonzero score (see bm25.go). Deterministic,
//     no network call, no model inference (lr-dab7e0 acceptance criterion 7).
//
// Each tier only runs if the prior tier produced no results, so an exact
// match is never diluted by broader fallback matches, and every query that
// resolves via tiers 1-3 today keeps resolving at the same rank
// (lr-dab7e0 acceptance criterion 3). Within tiers 1-3, results are ranked
// deterministically by rankAgents; Tier 4 ranks by BM25 score with the same
// canonical-rank/name tie-break.
func findByCapability(agents map[string]Agent, intents ...string) []Agent {
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
	if out := matchByRole(agents, normalized); len(out) > 0 {
		return rankAgents(out)
	}
	return bm25Fallback(agents, intents)
}

// bm25Fallback runs Tier 4: BM25 ranking over the raw (non-normalized)
// intents. The raw form is used (rather than normalized) because BM25
// tokenizes on word boundaries anyway (see tokenize in bm25.go) and the raw
// query preserves natural phrasing spacing that normalizeIntent's
// hyphen-collapsing would otherwise flatten before tokenization.
func bm25Fallback(agents map[string]Agent, intents []string) []Agent {
	var queryTokens []string
	for _, in := range intents {
		queryTokens = append(queryTokens, tokenize(in)...)
	}
	corpus := buildBM25Corpus(agents)
	ranked := bm25Score(corpus, queryTokens)
	if len(ranked) > bm25TopN {
		ranked = ranked[:bm25TopN]
	}
	return ranked
}

// matchByIntentSet matches agent capability intents against the (already
// normalized) query intents. When synonyms is true, each query intent is
// first resolved through intentSynonyms before matching (a query intent with
// no synonym entry is dropped from this tier's set).
func matchByIntentSet(agents map[string]Agent, intents []string, synonyms bool) []Agent {
	intentSet := make(map[string]bool)
	for _, i := range intents {
		if !synonyms {
			intentSet[i] = true
			continue
		}
		if canonical, ok := intentSynonyms[i]; ok {
			intentSet[canonical] = true
		}
	}
	if len(intentSet) == 0 {
		return nil
	}
	var out []Agent
	for _, a := range agents {
		for _, cap := range a.Capabilities {
			matched := false
			for _, t := range cap.Triggers.Intents {
				if intentSet[t] {
					matched = true
					break
				}
			}
			if matched {
				out = append(out, a)
				break
			}
		}
	}
	return out
}

// matchByRole matches agents whose declared Role equals one of the
// (already normalized) query intents (e.g. /v1/find?intent=builder resolves
// an agent with role: builder).
func matchByRole(agents map[string]Agent, intents []string) []Agent {
	roleSet := make(map[string]bool, len(intents))
	for _, i := range intents {
		roleSet[i] = true
	}
	var out []Agent
	for _, a := range agents {
		if a.Role != "" && roleSet[a.Role] {
			out = append(out, a)
		}
	}
	return out
}

// canonicalRank returns the rank of the highest-priority label in
// canonicalRankTrustLabels that a carries, or len(canonicalRankTrustLabels)
// if it carries none of them. Lower rank sorts first.
func canonicalRank(a Agent) int {
	labels := make(map[string]bool, len(a.TrustLabels))
	for _, l := range a.TrustLabels {
		labels[l] = true
	}
	for i, want := range canonicalRankTrustLabels {
		if labels[want] {
			return i
		}
	}
	return len(canonicalRankTrustLabels)
}

// rankAgents returns a new, deterministically ordered copy of agents:
//  1. Agents carrying a higher-priority canonicalRankTrustLabels entry
//     (e.g. "trusted") sort ahead of agents that don't — this is what
//     surfaces a canonical crew agent (prax) ahead of its own fallback
//     engine (gemini-researcher) when both declare the same intent.
//  2. Ties within the same canonical rank are broken by agent name,
//     ascending, so results are stable and reproducible across calls
//     regardless of Go's randomized map iteration order.
func rankAgents(agents []Agent) []Agent {
	out := make([]Agent, len(agents))
	copy(out, agents)
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := canonicalRank(out[i]), canonicalRank(out[j])
		if ri != rj {
			return ri < rj
		}
		return out[i].Name < out[j].Name
	})
	return out
}
