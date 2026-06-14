package store

import (
	"fmt"
	"strings"
)

// VocabularyConflictError describes a single agent entry that uses a value
// not present in the loaded vocabulary for a specific field.
type VocabularyConflictError struct {
	AgentPath string
	CapID     string
	Field     string
	Value     string
}

func (e VocabularyConflictError) Error() string {
	if e.CapID != "" {
		return fmt.Sprintf("%s: capability %q: %s contains unknown value %q",
			e.AgentPath, e.CapID, e.Field, e.Value)
	}
	return fmt.Sprintf("%s: %s contains unknown value %q", e.AgentPath, e.Field, e.Value)
}

// RegistryValidationErrors is the aggregate of all vocabulary conflicts found
// during a single reload attempt. It implements error so it can be returned
// directly from Reload and its callers.
type RegistryValidationErrors []VocabularyConflictError

func (e RegistryValidationErrors) Error() string {
	if len(e) == 0 {
		return "no validation errors"
	}
	msgs := make([]string, len(e))
	for i, c := range e {
		msgs[i] = c.Error()
	}
	return fmt.Sprintf("%d vocabulary conflict(s):\n%s", len(e), strings.Join(msgs, "\n"))
}
