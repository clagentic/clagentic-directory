package store

import (
	"math"
	"sort"
	"strings"
)

// bm25K1 and bm25B are the standard Okapi BM25 tuning parameters. k1 controls
// term-frequency saturation; b controls document-length normalization.
// These are the widely-used defaults (Robertson/Sparck Jones) and are not
// tuned against the golden set — tuning parameters to fit a specific golden
// set would be the same defect class this task exists to fix (hand-tuning to
// pass), just moved from a synonym table into a scoring constant.
const (
	bm25K1 = 1.2
	bm25B  = 0.75
)

// bm25Document is one agent capability flattened into a token bag for BM25
// scoring, plus a back-reference to the owning agent.
type bm25Document struct {
	agent  Agent
	tokens []string
}

// tokenize lowercases s and splits it into word tokens (letters, digits, and
// hyphens treated as part of a token; hyphens are also split into their own
// separate token so that "code-review" contributes both "code-review" and
// "code"/"review" to the bag — natural-language queries commonly use the
// unhyphenated form while registry vocabulary uses the hyphenated form).
func tokenize(s string) []string {
	s = strings.ToLower(s)
	var tokens []string
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			tokens = append(tokens, b.String())
			b.Reset()
		}
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			// End the current token, but also keep the hyphenated form
			// intact by writing the separator through so multi-word
			// canonical tokens ("code-review") remain matchable as a
			// single term in addition to their parts.
			b.WriteRune('-')
		default:
			flush()
		}
	}
	flush()

	// Expand each hyphenated token into its parts as additional tokens, so
	// both "code-review" and "code"/"review" independently score matches.
	out := make([]string, 0, len(tokens)*2)
	for _, t := range tokens {
		out = append(out, t)
		if strings.Contains(t, "-") {
			out = append(out, strings.Split(t, "-")...)
		}
	}
	return out
}

// buildBM25Corpus flattens agents into one bm25Document per agent: the
// agent's own name+description, plus every capability's name+id+description,
// all tokenized into a single bag. One document per agent (not per
// capability) because FindByCapability returns agents, and an agent with
// multiple capabilities should be scored as the union of what it can do.
func buildBM25Corpus(agents map[string]Agent) []bm25Document {
	docs := make([]bm25Document, 0, len(agents))
	for _, a := range agents {
		var tokens []string
		tokens = append(tokens, tokenize(a.Name)...)
		tokens = append(tokens, tokenize(a.Description)...)
		for _, c := range a.Capabilities {
			tokens = append(tokens, tokenize(c.ID)...)
			tokens = append(tokens, tokenize(c.Name)...)
			tokens = append(tokens, tokenize(c.Description)...)
		}
		docs = append(docs, bm25Document{agent: a, tokens: tokens})
	}
	return docs
}

// bm25Score scores every document in corpus against queryTokens using Okapi
// BM25 and returns agents ranked by descending score, ties broken by
// rankAgents (canonical trust label, then name) for determinism.
//
// Deterministic and reproducible by construction: term frequencies, document
// frequencies, and average document length are all computed once per call
// from corpus (no randomness, no network call, no model inference).
func bm25Score(corpus []bm25Document, queryTokens []string) []Agent {
	if len(corpus) == 0 || len(queryTokens) == 0 {
		return nil
	}

	// Document frequency per query term (dedup query tokens first so a
	// repeated query term doesn't double-count a document's presence).
	queryTermSet := make(map[string]bool, len(queryTokens))
	for _, t := range queryTokens {
		queryTermSet[t] = true
	}

	docFreq := make(map[string]int, len(queryTermSet))
	totalLen := 0
	for _, d := range corpus {
		totalLen += len(d.tokens)
		seen := make(map[string]bool, len(queryTermSet))
		for _, tok := range d.tokens {
			if queryTermSet[tok] && !seen[tok] {
				docFreq[tok]++
				seen[tok] = true
			}
		}
	}
	avgLen := float64(totalLen) / float64(len(corpus))
	n := float64(len(corpus))

	type scored struct {
		agent Agent
		score float64
	}
	var results []scored
	for _, d := range corpus {
		termFreq := make(map[string]int, len(d.tokens))
		for _, tok := range d.tokens {
			termFreq[tok]++
		}
		docLen := float64(len(d.tokens))

		var score float64
		for term := range queryTermSet {
			df := docFreq[term]
			if df == 0 {
				continue
			}
			tf := float64(termFreq[term])
			if tf == 0 {
				continue
			}
			idf := math.Log(1 + (n-float64(df)+0.5)/(float64(df)+0.5))
			numerator := tf * (bm25K1 + 1)
			denominator := tf + bm25K1*(1-bm25B+bm25B*(docLen/avgLen))
			score += idf * (numerator / denominator)
		}
		if score > 0 {
			results = append(results, scored{agent: d.agent, score: score})
		}
	}

	sort.SliceStable(results, func(i, j int) bool {
		if results[i].score != results[j].score {
			return results[i].score > results[j].score
		}
		// Deterministic tie-break: canonical rank, then name, matching
		// rankAgents' contract for tiers 1-3.
		ri, rj := canonicalRank(results[i].agent), canonicalRank(results[j].agent)
		if ri != rj {
			return ri < rj
		}
		return results[i].agent.Name < results[j].agent.Name
	})

	out := make([]Agent, len(results))
	for i, r := range results {
		out[i] = r.agent
	}
	return out
}
