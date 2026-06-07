package store

// Agent is a loaded agent capability entry from the registry.
type Agent struct {
	Name         string
	Version      string
	Description  string
	Capabilities []Capability
	TrustLabels  []string
	// SchemaVersion is the schema_version from the source YAML (1 or 2).
	// Both versions expose identical fields to API callers for backward compat.
	SchemaVersion int
	// SourceFile is the YAML file this entry was loaded from.
	SourceFile string
}

// Capability is one capability declared by an agent.
type Capability struct {
	ID          string
	Name        string
	Description string
	Triggers    Triggers
	Returns     Returns
}

// Triggers describes when this capability applies.
type Triggers struct {
	Intents           []string
	ConversationKinds []string
	AfterRoles        []string
	AfterAgents       []string
}

// Returns describes what this capability returns.
type Returns struct {
	VerdictField string
	Format       string
}

// Store is the interface all registry backends implement.
type Store interface {
	// ListAgents returns all loaded agents.
	ListAgents() []Agent
	// GetAgent returns the agent with the given name, and whether it was found.
	GetAgent(name string) (Agent, bool)
	// FindByCapability returns agents that declare a capability matching any of
	// the given intents.
	FindByCapability(intents ...string) []Agent
	// FindByConversationKind returns agents whose capabilities include the given
	// conversation_kind trigger.
	FindByConversationKind(kind string) []Agent
	// FindBySequencing returns agents whose capabilities declare after_agent
	// matching the given agent name.
	FindBySequencing(afterAgent string) []Agent
	// Reload re-reads the backend source. Implementations must be safe to call
	// from multiple goroutines.
	Reload() error
}
