package store

import "fmt"

// Bounds on a /v1/find query parameter, enforced before any store lookup so
// Tier 4 BM25 (cost O(agents x query terms)) never scores pathological input.
//
// Derivation, measured against goldenSet (golden_set.go) with the same
// tokenize() Tier 4 scores with:
//
//	longest golden query: 37 bytes  ("read this single url and summarize it")
//	most golden terms:     7 terms  (same query)
//
// MaxFindQueryBytes = 256 is about 7x the measured byte maximum; a long but
// real natural-language phrasing is well under it.
// MaxFindQueryTerms = 32 is about 4.5x the measured term maximum. The count is
// post-tokenization, so a hyphenated word counts as the whole plus its parts.
//
// TestGoldenSetWithinFindQueryLimits fails if the golden set outgrows either
// limit, forcing a conscious revisit of this derivation.
const (
	MaxFindQueryBytes = 256
	MaxFindQueryTerms = 32
)

// QueryLimitError reports which /v1/find parameter exceeded which limit.
type QueryLimitError struct {
	Param string
	Limit int
	Unit  string // "bytes" or "terms"
}

func (e *QueryLimitError) Error() string {
	return fmt.Sprintf("%s exceeds the maximum of %d %s", e.Param, e.Limit, e.Unit)
}

// ValidateFindParam returns a *QueryLimitError when value is longer than
// MaxFindQueryBytes or tokenizes to more than MaxFindQueryTerms terms. The
// byte check runs first so the tokenizer never sees an oversized string.
func ValidateFindParam(param, value string) error {
	if len(value) > MaxFindQueryBytes {
		return &QueryLimitError{Param: param, Limit: MaxFindQueryBytes, Unit: "bytes"}
	}
	if len(tokenize(value)) > MaxFindQueryTerms {
		return &QueryLimitError{Param: param, Limit: MaxFindQueryTerms, Unit: "terms"}
	}
	return nil
}
