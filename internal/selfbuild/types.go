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
// Fields populated only from self-declaration blocks (ConversationKinds, AfterRoles,
// AfterAgents, VerdictField) are pointer types so they are omitted when nil.
type ProposedCapability struct {
	ID                AnnotatedString    `yaml:"id"`
	Name              AnnotatedString    `yaml:"name"`
	Description       AnnotatedString    `yaml:"description"`
	Intents           AnnotatedStrings   `yaml:"intents"`
	Format            AnnotatedString    `yaml:"format"`
	ConversationKinds *AnnotatedStrings  `yaml:"conversation_kinds,omitempty"`
	AfterRoles        *AnnotatedStrings  `yaml:"after_roles,omitempty"`
	AfterAgents       *AnnotatedStrings  `yaml:"after_agents,omitempty"`
	VerdictField      *AnnotatedString   `yaml:"verdict_field,omitempty"`
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
	Actor              string `yaml:"actor"`
	NextActor          string `yaml:"next_actor"`
	ConversationKind   string `yaml:"conversation_kind"`
	ObservedCount      int    `yaml:"observed_count"`
	RegisteredAfterSeq bool   `yaml:"registered_after_seq"`
	// ResearchFirstFlag is true when the actor is a lead or director who
	// posted in this conversation window without any recorded lore search
	// (LastLoreSearchAt absent in the event window). This extends the
	// lr-5646 usage-inference mechanism to surface the research-first
	// failure pattern documented in lr-d482 (tome #453).
	// Populated only when the relay event API exposes ActorRole and
	// LastLoreSearchAt on RelayEvent; zero-value (false) otherwise.
	ResearchFirstFlag bool `yaml:"research_first_flag,omitempty"`
}
