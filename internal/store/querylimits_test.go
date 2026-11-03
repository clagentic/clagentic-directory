package store

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateFindParamLength(t *testing.T) {
	for _, param := range []string{"intent", "conversation_kind", "after_agent"} {
		if err := ValidateFindParam(param, strings.Repeat("a", MaxFindQueryBytes)); err != nil {
			t.Errorf("%s: at-limit length rejected: %v", param, err)
		}
		err := ValidateFindParam(param, strings.Repeat("a", MaxFindQueryBytes+1))
		var qe *QueryLimitError
		if !errors.As(err, &qe) || qe.Param != param || qe.Unit != "bytes" || qe.Limit != MaxFindQueryBytes {
			t.Errorf("%s: limit+1 length: got %v", param, err)
		}
	}
}

func TestValidateFindParamTerms(t *testing.T) {
	words := func(n int) string { return strings.TrimSpace(strings.Repeat("ab ", n)) }
	for _, param := range []string{"intent", "conversation_kind", "after_agent"} {
		if err := ValidateFindParam(param, words(MaxFindQueryTerms)); err != nil {
			t.Errorf("%s: at-limit terms rejected: %v", param, err)
		}
		err := ValidateFindParam(param, words(MaxFindQueryTerms+1))
		var qe *QueryLimitError
		if !errors.As(err, &qe) || qe.Param != param || qe.Unit != "terms" || qe.Limit != MaxFindQueryTerms {
			t.Errorf("%s: limit+1 terms: got %v", param, err)
		}
	}
}

func TestGoldenSetWithinFindQueryLimits(t *testing.T) {
	for _, c := range goldenSet {
		if err := ValidateFindParam("intent", c.query); err != nil {
			t.Errorf("golden query %q exceeds limit: %v", c.query, err)
		}
	}
}
