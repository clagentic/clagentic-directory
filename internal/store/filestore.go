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

// FileStore implements Store by reading YAML agent entries from a directory.
type FileStore struct {
	dir     string
	mu      sync.RWMutex
	agents  map[string]Agent
	watcher *fsnotify.Watcher
}

// NewFileStore creates a FileStore rooted at dir and does an initial load.
// It starts an inotify watcher for hot-reload in the background.
func NewFileStore(dir string) (*FileStore, error) {
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
	if raw.SchemaVersion != 1 {
		return Agent{}, fmt.Errorf("%s: unsupported schema_version %d (expected 1)", path, raw.SchemaVersion)
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
		Name:         raw.Identity.Name,
		Version:      raw.Identity.Version,
		Description:  raw.Identity.Description,
		Capabilities: caps,
		TrustLabels:  raw.TrustLabels,
		SourceFile:   path,
	}, nil
}
