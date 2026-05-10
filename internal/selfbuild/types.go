package selfbuild

import "time"

// Confidence indicates how a field value was determined.
type Confidence string

const (
	ConfidenceExtracted Confidence = "extracted" // directly from source data
	ConfidenceInferred  Confidence = "inferred"  // derived heuristically
)

// AnnotatedString is a string value with a confidence label.
type AnnotatedString struct {
	Value      string     `yaml:"value"`
	Confidence Confidence `yaml:"confidence"`
}

// AnnotatedStrings is a string slice with a confidence label.
type AnnotatedStrings struct {
	Values     []string   `yaml:"values"`
	Confidence Confidence `yaml:"confidence"`
}

// ProposedCapability mirrors store.Capability but carries per-field confidence labels.
type ProposedCapability struct {
	ID          AnnotatedString   `yaml:"id"`
	Name        AnnotatedString   `yaml:"name"`
	Description AnnotatedString   `yaml:"description"`
	Intents     AnnotatedStrings  `yaml:"intents"`
	Format      AnnotatedString   `yaml:"format"`
}

// ProposedChange is written to proposed_changes/<agent>.<timestamp>.yaml.
// It is never applied to the live registry — an operator PR gate is required.
type ProposedChange struct {
	SchemaVersion int                  `yaml:"schema_version"`
	GeneratedAt   time.Time            `yaml:"generated_at"`
	Source        string               `yaml:"source"` // "mcp-discovery" | "engram-watch" | "usage-inference"
	AgentName     string               `yaml:"agent_name"`
	Capabilities  []ProposedCapability `yaml:"capabilities,omitempty"`
	DriftReports  []DriftReport        `yaml:"drift_reports,omitempty"`
	Notes         []string             `yaml:"notes,omitempty"`
}

// DriftReport captures an observed vs registered sequencing mismatch.
type DriftReport struct {
	Actor              string  `yaml:"actor"`
	NextActor          string  `yaml:"next_actor"`
	ConversationKind   string  `yaml:"conversation_kind"`
	ObservedCount      int     `yaml:"observed_count"`
	RegisteredAfterSeq bool    `yaml:"registered_after_seq"`
}
