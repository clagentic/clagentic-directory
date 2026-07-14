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

// snapshot is the atomic unit of store state: agents and vocabulary are always
// published together. Readers hold a reference to a snapshot; reloads produce
// a new snapshot and swap it under mu.
type snapshot struct {
	agents map[string]Agent
	vocab  *vocabulary // nil means ValidateOpen
}

// FileStore implements Store by reading YAML agent entries from a directory.
// An optional vocabulary file is watched alongside the registry directory;
// when either changes, the store reloads atomically.
type FileStore struct {
	dir       string
	vocabPath string // absolute path; empty means ValidateOpen
	mode      ValidationMode

	reloadMu sync.Mutex   // serializes reload candidate building; never held during I/O waits
	mu       sync.RWMutex // protects snap; held only for the final swap and reads
	snap     snapshot

	watcher *fsnotify.Watcher
}

// NewFileStore creates a FileStore rooted at dir and does an initial load.
// It starts an inotify watcher for hot-reload in the background.
//
// vocabPath is the path to a vocabulary.v1.yaml file (see docs/VOCABULARY.md).
// When empty, vocabulary checking is skipped (ValidateOpen).
//
// ext is a deprecated shim for --vocabulary-extensions; pass zero value when
// using --vocab-file. If both vocabPath and ext are provided, ext values are
// merged into the loaded vocabulary with a deprecation warning.
func NewFileStore(dir, vocabPath string, ext VocabularyExtensions) (*FileStore, error) {
	vocab, mode, err := resolveVocabulary(vocabPath, ext)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	var absVocab, vocabDir string
	if vocabPath != "" {
		absVocab, vocabDir, err = cleanVocabPath(vocabPath)
		if err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}
	}

	fs := &FileStore{
		dir:       dir,
		vocabPath: absVocab,
		mode:      mode,
	}

	if err := fs.reload(vocab); err != nil {
		return nil, err
	}

	w, werr := fsnotify.NewWatcher()
	if werr != nil {
		slog.Warn("fsnotify unavailable, hot-reload disabled", "err", werr)
		return fs, nil
	}
	if err := w.Add(dir); err != nil {
		slog.Warn("could not watch registry dir", "dir", dir, "err", err)
		w.Close()
		return fs, nil
	}
	if vocabDir != "" && vocabDir != dir {
		if err := w.Add(vocabDir); err != nil {
			slog.Warn("could not watch vocab file parent dir", "dir", vocabDir, "err", err)
			// Non-fatal: registry-only hot-reload still works.
		}
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
			if !f.isRelevantEvent(event) {
				continue
			}
			// Re-add watch on vocab file parent dir after rename/remove
			// (editors often replace files via rename).
			if f.vocabPath != "" &&
				(event.Has(fsnotify.Rename) || event.Has(fsnotify.Remove)) {
				vocabDir := filepath.Dir(f.vocabPath)
				_ = f.watcher.Add(vocabDir)
			}
			if err := f.Reload(); err != nil {
				slog.Error("hot-reload failed", "err", err)
			} else {
				slog.Info("registry hot-reloaded", "trigger", event.Name)
			}
		case err, ok := <-f.watcher.Errors:
			if !ok {
				return
			}
			slog.Error("fsnotify error", "err", err)
		}
	}
}

// isRelevantEvent returns true for fsnotify events that should trigger a reload.
// Events from the vocab file's parent directory are filtered to only the vocab
// file path; all changes within the registry dir are treated as relevant.
//
// Why we watch the parent dir rather than the file directly: editors commonly
// write via a temp file and then rename it over the target. A direct file watch
// is lost when the original inode is replaced by rename/remove. Watching the
// parent dir survives the rename; the re-add logic in watchLoop re-registers
// the parent after Remove/Rename events so the watch persists across repeated
// atomic saves. Create events for the new file are then caught and trigger a
// reload via this function's vocab-file path filter.
func (f *FileStore) isRelevantEvent(event fsnotify.Event) bool {
	if !event.Has(fsnotify.Write) && !event.Has(fsnotify.Create) &&
		!event.Has(fsnotify.Remove) && !event.Has(fsnotify.Rename) {
		return false
	}
	evPath, _ := filepath.Abs(event.Name)
	cleanDir := filepath.Clean(f.dir)
	// Any change in the registry dir is relevant.
	if strings.HasPrefix(evPath, cleanDir+string(filepath.Separator)) ||
		filepath.Clean(evPath) == cleanDir {
		return true
	}
	// For the vocab file parent dir, only trigger on the vocab file itself.
	if f.vocabPath != "" && filepath.Clean(evPath) == filepath.Clean(f.vocabPath) {
		return true
	}
	return false
}

// Close shuts down the background watcher.
func (f *FileStore) Close() error {
	if f.watcher != nil {
		return f.watcher.Close()
	}
	return nil
}

// Reload re-reads the vocabulary file and all YAML agent files. If the reload
// produces no validation errors, the new snapshot is swapped in atomically.
// On conflict errors, the old snapshot is retained and the errors are logged.
func (f *FileStore) Reload() error {
	f.reloadMu.Lock()
	defer f.reloadMu.Unlock()

	var (
		vocab *vocabulary
		err   error
	)
	if f.vocabPath != "" {
		vocab, err = loadVocabulary(f.vocabPath)
		if err != nil {
			slog.Error("reload: failed to load vocabulary; keeping old snapshot",
				"vocab_path", f.vocabPath, "err", err)
			return err
		}
	}
	return f.reload(vocab)
}

// reload builds a new snapshot from vocab and the registry dir, then swaps it in.
// Vocabulary conflicts aggregate across all entries; on any conflict the old
// snapshot is retained and all conflicts are logged.
func (f *FileStore) reload(vocab *vocabulary) error {
	entries, err := loadDir(f.dir, vocab)
	if err != nil {
		var ve RegistryValidationErrors
		if asVE(err, &ve) {
			slog.Error("reload: vocabulary conflicts; keeping old snapshot",
				"vocab_path", f.vocabPath,
				"conflict_count", len(ve),
				"conflicts", ve.Error())
			return err
		}
		return err
	}
	f.mu.Lock()
	f.snap = snapshot{agents: entries, vocab: vocab}
	f.mu.Unlock()
	return nil
}

// asVE type-asserts err to RegistryValidationErrors. Returns true on match.
func asVE(err error, out *RegistryValidationErrors) bool {
	if err == nil {
		return false
	}
	if ve, ok := err.(RegistryValidationErrors); ok {
		*out = ve
		return true
	}
	return false
}

func (f *FileStore) ListAgents() []Agent {
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]Agent, 0, len(f.snap.agents))
	for _, a := range f.snap.agents {
		out = append(out, a)
	}
	return out
}

func (f *FileStore) GetAgent(name string) (Agent, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	a, ok := f.snap.agents[name]
	return a, ok
}

func (f *FileStore) FindByCapability(intents ...string) []Agent {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return findByCapability(f.snap.agents, intents...)
}

func (f *FileStore) FindByConversationKind(kind string) []Agent {
	f.mu.RLock()
	defer f.mu.RUnlock()
	var out []Agent
	for _, a := range f.snap.agents {
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
	for _, a := range f.snap.agents {
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
	Role        string `yaml:"role"`
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

// loadDir reads all YAML agent files in dir, validating v2 entries against vocab.
// Returns RegistryValidationErrors (aggregated) when vocabulary conflicts are found.
func loadDir(dir string, vocab *vocabulary) (map[string]Agent, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading registry dir %s: %w", dir, err)
	}
	agents := make(map[string]Agent)
	var allConflicts RegistryValidationErrors
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
		agent, err := parseEntry(path, data, vocab)
		if err != nil {
			// Aggregate vocabulary conflicts; fail fast on structural errors.
			if ve, ok := err.(RegistryValidationErrors); ok {
				allConflicts = append(allConflicts, ve...)
				continue
			}
			return nil, err
		}
		if _, dup := agents[agent.Name]; dup {
			return nil, fmt.Errorf("duplicate agent name %q in %s", agent.Name, path)
		}
		agents[agent.Name] = agent
	}
	if len(allConflicts) > 0 {
		return nil, allConflicts
	}
	return agents, nil
}

// maxYAMLSize is the maximum allowed size for a single agent YAML entry.
// This guards against yaml.v3 alias-bomb and deeply-nested DOS attacks.
const maxYAMLSize = 1 << 20 // 1 MiB

func parseEntry(path string, data []byte, vocab *vocabulary) (Agent, error) {
	if len(data) > maxYAMLSize {
		return Agent{}, fmt.Errorf("%s: YAML entry too large (%d bytes, max %d)", path, len(data), maxYAMLSize)
	}
	var raw rawEntry
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return Agent{}, fmt.Errorf("parsing %s: %w", path, err)
	}

	switch raw.SchemaVersion {
	case 1:
		// Transition-period: accept v1 entries with a deprecation warning.
		// v1 entries expose the same fields as v2 to all API callers.
		// TODO: remove v1 acceptance once fleet is fully migrated.
		slog.Warn("agent entry uses schema_version 1; please migrate to v2",
			"file", path)
	case 2:
		// Required-field checks run regardless of vocabulary mode.
		if err := checkV2RequiredFields(path, &raw); err != nil {
			return Agent{}, err
		}
		// Vocabulary validation: nil vocab means ValidateOpen, returns nil immediately.
		if err := vocab.validateV2(path, &raw); err != nil {
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
		Role:          raw.Identity.Role,
	}, nil
}

// checkV2RequiredFields enforces the non-vocabulary required fields for v2 entries.
// These checks run regardless of vocabulary mode (ValidateStrict or ValidateOpen).
func checkV2RequiredFields(path string, raw *rawEntry) error {
	for _, rc := range raw.Capabilities {
		if rc.Returns.VerdictField == "" {
			return fmt.Errorf("%s: capability %q: returns.verdict_field is required in schema_version: 2",
				path, rc.ID)
		}
		if rc.Returns.Format == "" {
			return fmt.Errorf("%s: capability %q: returns.format is required in schema_version: 2",
				path, rc.ID)
		}
	}
	return nil
}
