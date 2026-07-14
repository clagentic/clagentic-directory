package store

// intentSynonyms maps a natural-language query term to the canonical intent
// it aliases. Used by findByCapability as a fallback tier when no agent
// declares an exact match on the raw query intent(s).
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

// findByCapability implements the shared FindByCapability matching contract
// for both FileStore and GitStore. It is tiered:
//  1. Exact match: an agent capability declares one of the raw query intents.
//  2. Synonym match: a raw query intent aliases (via intentSynonyms) to a
//     canonical intent an agent capability declares.
//  3. Role match: a raw query intent equals an agent's declared Role.
//
// Each tier only runs if the prior tier produced no results, so an exact
// match is never diluted by broader fallback matches.
func findByCapability(agents map[string]Agent, intents ...string) []Agent {
	if out := matchByIntentSet(agents, intents, false); len(out) > 0 {
		return out
	}
	if out := matchByIntentSet(agents, intents, true); len(out) > 0 {
		return out
	}
	return matchByRole(agents, intents)
}

// matchByIntentSet matches agent capability intents against the raw query
// intents. When synonyms is true, each raw query intent is first resolved
// through intentSynonyms before matching (a query intent with no synonym
// entry is dropped from this tier's set).
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

// matchByRole matches agents whose declared Role equals one of the raw query
// intents (e.g. /v1/find?intent=builder resolves an agent with role: builder).
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
