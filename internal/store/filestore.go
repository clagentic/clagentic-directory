package store

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
	"gopkg.in/yaml.v3"
)

// v2ValidIntents is the closed set of intent strings valid in schema_version: 2 entries.
// Any string not in this set causes a hard load failure for v2 entries.
// Update this alongside schemas/agent-entry.v2.yaml and schemas/vocabulary.v1.yaml.
var v2ValidIntents = map[string]bool{
	// Code work
	"code_work_requested":   true,
	"code-generation":       true,
	"implement-task":        true,
	// Code review
	"pr_opened":             true,
	"code_ready_for_review": true,
	"code-review":           true,
	"review-pr":             true,
	"review-commit":         true,
	// Merge / release
	"merge_requested":       true,
	"merge-pr":              true,
	"release":               true,
	"tag-release":           true,
	// Diagnosis
	"diagnostic_requested":  true,
	"root_cause_unknown":    true,
	"escalation_diagnosis":  true,
	// Ops
	"deploy_requested":      true,
	"runbook_run":           true,
	"ops_check":             true,
	// Research
	"research_requested":    true,
	"survey_requested":      true,
	"research":              true,
	"investigate":           true,
	"find-information":      true,
	// Web research
	"web-research":          true,
	"web-search":            true,
	"url-fetch":             true,
	"fact-lookup":           true,
	"doc-lookup":            true,
	"large-context-analysis": true,
	"codebase-survey":       true,
	"community-sentiment":   true,
	"reddit-research":       true,
	"user-opinion-research": true,
	// Analysis & review
	"deep-analysis":         true,
	"architecture-review":   true,
	"security-review":       true,
	"tradeoff-evaluation":   true,
	"second-opinion":        true,
	// Model delegation
	"delegate-to-codex":     true,
	"codex-review":          true,
	"gpt-reasoning":         true,
	"local-inference":       true,
	"cheap-inference":       true,
	"offline-inference":     true,
	"embeddings":            true,
	// Intelligence harvesting
	"inspect-repo":          true,
	"harvest-intelligence":  true,
	"ingest-candidate":      true,
	// Scaffolding
	"scaffold_requested":    true,
	"new_project_setup":     true,
	// Testing & probing
	"probe":                 true,
	"wiring-test":           true,
	// Routing / escalation
	"escalation":            true,
	"portfolio_question":    true,
	"dispatch_routing":      true,
}

// v2ValidConversationKinds is the closed set of conversation_kinds valid in schema_version: 2.
var v2ValidConversationKinds = map[string]bool{
	"build":           true,
	"consult":         true,
	"smoke":           true,
	"gate":            true,
	"research":        true,
	"review":          true,
	"deploy":          true,
	"planning":        true,
	"directive":       true,
	"escalation":      true,
	"coordination":    true,
	// Additional kinds from clagentic-config canonical agents
	"advisory":        true,
	"code-generation": true,
	"classification":  true,
	"summarization":   true,
	"design":          true,
	"test":            true,
}

// v2ValidTrustLabels is the closed set of trust_labels valid in schema_version: 2.
var v2ValidTrustLabels = map[string]bool{
	// Write / gate labels
	"read-only":          true,
	"write-pr":           true,
	"write-ops":          true,
	"merge-gate":         true,
	"publish":            true,
	// Routing / authority labels
	"observe":            true,
	"escalation-surface": true,
	"dispatch-authority": true,
	// Agent character labels
	"trusted":            true,
	"autonomous":         true,
	"high-stakes":        true,
	"release-authorized": true,
	// Model / source origin labels
	"external-model":     true,
	"external-source":    true,
	"local-model":        true,
	// Lifecycle labels
	"test-only":          true,
}

// v2ValidFormats is the closed set of returns.format values valid in schema_version: 2.
var v2ValidFormats = map[string]bool{
	"json":                 true,
	"structured":           true,
	"structured-markdown":  true,
	"url":                  true,
	"text":                 true,
	// Additional formats
	"agent-result-json":     true,
	"verbatim-model-output": true,
	"plaintext":             true,
}

// FileStore implements Store by reading YAML agent entries from a directory.
type FileStore struct {
	dir     string
	mu      sync.RWMutex
	agents  map[string]Agent
	watcher *fsnotify.Watcher
}

// NewFileStore creates a FileStore rooted at dir and does an initial load.
// It starts an inotify watcher for hot-reload in the background.
// ext is merged into the base vocabulary before the first load; pass a zero
// VocabularyExtensions if no extensions are needed.
func NewFileStore(dir string, ext VocabularyExtensions) (*FileStore, error) {
	applyExtensions(ext)
	fs := &FileStore{
		dir:    dir,
		agents: make(map[string]Agent),
	}
	if err := fs.Reload(); err != nil {
		return nil, err
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		slog.Warn("fsnotify unavailable, hot-reload disabled", "err", err)
		return fs, nil
	}
	if err := w.Add(dir); err != nil {
		slog.Warn("could not watch registry dir", "dir", dir, "err", err)
		w.Close()
		return fs, nil
	}
	fs.watcher = w
	go fs.watchLoop()
	return fs, nil
}

func (f *FileStore) watchLoop() {
	for {
		select {
		case event, ok := <-f.watcher.Events:
			if !ok {
				return
			}
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) || event.Has(fsnotify.Remove) {
				if err := f.Reload(); err != nil {
					slog.Error("hot-reload failed", "err", err)
				} else {
					slog.Info("registry hot-reloaded", "trigger", event.Name)
				}
			}
		case err, ok := <-f.watcher.Errors:
			if !ok {
				return
			}
			slog.Error("fsnotify error", "err", err)
		}
	}
}

// Close shuts down the background watcher.
func (f *FileStore) Close() error {
	if f.watcher != nil {
		return f.watcher.Close()
	}
	return nil
}

// Reload re-reads all YAML files in the directory.
func (f *FileStore) Reload() error {
	entries, err := loadDir(f.dir)
	if err != nil {
		return err
	}
	f.mu.Lock()
	f.agents = entries
	f.mu.Unlock()
	return nil
}

func (f *FileStore) ListAgents() []Agent {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]Agent, 0, len(f.agents))
	for _, a := range f.agents {
		out = append(out, a)
	}
	return out
}

func (f *FileStore) GetAgent(name string) (Agent, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	a, ok := f.agents[name]
	return a, ok
}

func (f *FileStore) FindByCapability(intents ...string) []Agent {
	f.mu.RLock()
	defer f.mu.RUnlock()
	intentSet := make(map[string]bool, len(intents))
	for _, i := range intents {
		intentSet[i] = true
	}
	var out []Agent
	for _, a := range f.agents {
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

func (f *FileStore) FindByConversationKind(kind string) []Agent {
	f.mu.RLock()
	defer f.mu.RUnlock()
	var out []Agent
	for _, a := range f.agents {
		for _, cap := range a.Capabilities {
			matched := false
			for _, k := range cap.Triggers.ConversationKinds {
				if k == kind {
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

func (f *FileStore) FindBySequencing(afterAgent string) []Agent {
	f.mu.RLock()
	defer f.mu.RUnlock()
	var out []Agent
	for _, a := range f.agents {
		for _, cap := range a.Capabilities {
			matched := false
			for _, ag := range cap.Triggers.AfterAgents {
				if ag == afterAgent {
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

// --- YAML loading ---

type rawEntry struct {
	SchemaVersion int             `yaml:"schema_version"`
	Identity      rawIdentity     `yaml:"identity"`
	Capabilities  []rawCapability `yaml:"capabilities"`
	TrustLabels   []string        `yaml:"trust_labels"`
}

type rawIdentity struct {
	Name        string `yaml:"name"`
	Version     string `yaml:"version"`
	Description string `yaml:"description"`
}

type rawCapability struct {
	ID          string      `yaml:"id"`
	Name        string      `yaml:"name"`
	Description string      `yaml:"description"`
	Triggers    rawTriggers `yaml:"triggers"`
	Returns     rawReturns  `yaml:"returns"`
}

type rawTriggers struct {
	Intents           []string `yaml:"intents"`
	ConversationKinds []string `yaml:"conversation_kinds"`
	AfterRoles        []string `yaml:"after_roles"`
	AfterAgents       []string `yaml:"after_agents"`
}

type rawReturns struct {
	VerdictField string `yaml:"verdict_field"`
	Format       string `yaml:"format"`
}

func loadDir(dir string) (map[string]Agent, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading registry dir %s: %w", dir, err)
	}
	agents := make(map[string]Agent)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}
		agent, err := parseEntry(path, data)
		if err != nil {
			return nil, err
		}
		if _, dup := agents[agent.Name]; dup {
			return nil, fmt.Errorf("duplicate agent name %q in %s", agent.Name, path)
		}
		agents[agent.Name] = agent
	}
	return agents, nil
}

func parseEntry(path string, data []byte) (Agent, error) {
	var raw rawEntry
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return Agent{}, fmt.Errorf("parsing %s: %w", path, err)
	}

	switch raw.SchemaVersion {
	case 1:
		// Transition-period: accept v1 entries with a deprecation warning.
		// v1 entries expose the same fields as v2 to all API callers.
		// TODO(lr-1745): remove v1 acceptance once fleet is fully migrated.
		slog.Warn("agent entry uses schema_version 1; please migrate to v2; see lr-1745",
			"file", path)
	case 2:
		// v2 entries are validated strictly against the closed vocabulary.
		if err := validateV2(path, &raw); err != nil {
			return Agent{}, err
		}
	default:
		return Agent{}, fmt.Errorf("%s: unsupported schema_version %d (supported: 1, 2)", path, raw.SchemaVersion)
	}

	if raw.Identity.Name == "" {
		return Agent{}, fmt.Errorf("%s: identity.name is required", path)
	}

	caps := make([]Capability, 0, len(raw.Capabilities))
	for _, rc := range raw.Capabilities {
		caps = append(caps, Capability{
			ID:          rc.ID,
			Name:        rc.Name,
			Description: rc.Description,
			Triggers: Triggers{
				Intents:           rc.Triggers.Intents,
				ConversationKinds: rc.Triggers.ConversationKinds,
				AfterRoles:        rc.Triggers.AfterRoles,
				AfterAgents:       rc.Triggers.AfterAgents,
			},
			Returns: Returns{
				VerdictField: rc.Returns.VerdictField,
				Format:       rc.Returns.Format,
			},
		})
	}

	return Agent{
		Name:          raw.Identity.Name,
		Version:       raw.Identity.Version,
		Description:   raw.Identity.Description,
		Capabilities:  caps,
		TrustLabels:   raw.TrustLabels,
		SchemaVersion: raw.SchemaVersion,
		SourceFile:    path,
	}, nil
}

// validateV2 applies strict vocabulary validation to a schema_version: 2 entry.
// It fails with a clear error naming the offending field and value on any violation.
func validateV2(path string, raw *rawEntry) error {
	for _, rc := range raw.Capabilities {
		for _, intent := range rc.Triggers.Intents {
			if !v2ValidIntents[intent] {
				return fmt.Errorf("%s: capability %q: triggers.intents contains unknown value %q; see docs/VOCABULARY.md or schemas/vocabulary.v1.yaml for valid intents",
					path, rc.ID, intent)
			}
		}
		for _, kind := range rc.Triggers.ConversationKinds {
			if !v2ValidConversationKinds[kind] {
				return fmt.Errorf("%s: capability %q: triggers.conversation_kinds contains unknown value %q; see docs/VOCABULARY.md for valid kinds",
					path, rc.ID, kind)
			}
		}
		if rc.Returns.VerdictField == "" {
			return fmt.Errorf("%s: capability %q: returns.verdict_field is required in schema_version: 2",
				path, rc.ID)
		}
		if rc.Returns.Format == "" {
			return fmt.Errorf("%s: capability %q: returns.format is required in schema_version: 2",
				path, rc.ID)
		}
		if !v2ValidFormats[rc.Returns.Format] {
			return fmt.Errorf("%s: capability %q: returns.format contains unknown value %q; see docs/VOCABULARY.md for valid formats",
				path, rc.ID, rc.Returns.Format)
		}
	}
	for _, label := range raw.TrustLabels {
		if !v2ValidTrustLabels[label] {
			return fmt.Errorf("%s: trust_labels contains unknown value %q; see docs/VOCABULARY.md for valid trust_labels",
				path, label)
		}
	}
	return nil
}
