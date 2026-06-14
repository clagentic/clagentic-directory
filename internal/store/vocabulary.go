package store

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ValidationMode controls how the store handles schema_version: 2 vocabulary checks.
type ValidationMode int

const (
	// ValidateStrict requires all v2 intent/kind/format/trust_label values to be
	// present in the loaded vocabulary. Entries with unknown values fail to load.
	ValidateStrict ValidationMode = iota
	// ValidateOpen skips vocabulary checking for v2 entries. All values are accepted.
	// This mode is active when no --vocab-file is provided at startup.
	ValidateOpen
)

// vocabulary holds the closed sets of valid values for schema_version: 2 entries.
// It is immutable after construction — all exported query methods are safe for
// concurrent use without locking.
//
// A nil *vocabulary means ValidateOpen: all values are accepted.
type vocabulary struct {
	intents           map[string]bool
	conversationKinds map[string]bool
	trustLabels       map[string]bool
	formats           map[string]bool
}

// rawVocabularyFile is the on-disk representation of a vocabulary config file.
// schema_version must be 1. Keys are valid vocabulary values; the string value
// is an informational description only and is not used at runtime.
//
// yaml.KnownFields is enabled at decode time so typos like "format" vs "formats"
// fail loudly rather than being silently ignored.
type rawVocabularyFile struct {
	SchemaVersion     int               `yaml:"schema_version"`
	Intents           map[string]string `yaml:"intents"`
	ConversationKinds map[string]string `yaml:"conversation_kinds"`
	TrustLabels       map[string]string `yaml:"trust_labels"`
	Formats           map[string]string `yaml:"formats"`
}

// loadVocabulary reads and parses the vocabulary file at path, returning an
// immutable *vocabulary. Returns an error if the file cannot be read or parsed,
// or if schema_version is not 1.
func loadVocabulary(path string) (*vocabulary, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load vocabulary %s: %w", path, err)
	}
	var raw rawVocabularyFile
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("parse vocabulary %s: %w", path, err)
	}
	if raw.SchemaVersion != 1 {
		return nil, fmt.Errorf("vocabulary %s: unsupported schema_version %d (only 1 is supported)", path, raw.SchemaVersion)
	}
	v := &vocabulary{
		intents:           make(map[string]bool, len(raw.Intents)),
		conversationKinds: make(map[string]bool, len(raw.ConversationKinds)),
		trustLabels:       make(map[string]bool, len(raw.TrustLabels)),
		formats:           make(map[string]bool, len(raw.Formats)),
	}
	for k := range raw.Intents {
		v.intents[k] = true
	}
	for k := range raw.ConversationKinds {
		v.conversationKinds[k] = true
	}
	for k := range raw.TrustLabels {
		v.trustLabels[k] = true
	}
	for k := range raw.Formats {
		v.formats[k] = true
	}
	return v, nil
}

// mergeExtensions returns a new *vocabulary with the extension values merged in.
// The receiver is not modified. If v is nil, a new vocabulary is built from ext alone.
//
// Deprecated: VocabularyExtensions is a compatibility shim for the
// --vocabulary-extensions flag. Use --vocab-file instead.
func (v *vocabulary) mergeExtensions(ext VocabularyExtensions) *vocabulary {
	out := &vocabulary{
		intents:           make(map[string]bool),
		conversationKinds: make(map[string]bool),
		trustLabels:       make(map[string]bool),
		formats:           make(map[string]bool),
	}
	if v != nil {
		for k, val := range v.intents {
			out.intents[k] = val
		}
		for k, val := range v.conversationKinds {
			out.conversationKinds[k] = val
		}
		for k, val := range v.trustLabels {
			out.trustLabels[k] = val
		}
		for k, val := range v.formats {
			out.formats[k] = val
		}
	}
	for _, k := range ext.Intents {
		out.intents[k] = true
	}
	for _, k := range ext.ConversationKinds {
		out.conversationKinds[k] = true
	}
	for _, k := range ext.TrustLabels {
		out.trustLabels[k] = true
	}
	for _, k := range ext.Formats {
		out.formats[k] = true
	}
	return out
}

// validateV2 checks all v2-specific vocabulary fields in raw against v.
// Returns a RegistryValidationErrors aggregating all violations found.
// A nil receiver means ValidateOpen: always returns nil.
func (v *vocabulary) validateV2(path string, raw *rawEntry) error {
	if v == nil {
		return nil
	}
	var errs RegistryValidationErrors
	for _, rc := range raw.Capabilities {
		for _, intent := range rc.Triggers.Intents {
			if !v.intents[intent] {
				errs = append(errs, VocabularyConflictError{
					AgentPath: path,
					CapID:     rc.ID,
					Field:     "triggers.intents",
					Value:     intent,
				})
			}
		}
		for _, kind := range rc.Triggers.ConversationKinds {
			if !v.conversationKinds[kind] {
				errs = append(errs, VocabularyConflictError{
					AgentPath: path,
					CapID:     rc.ID,
					Field:     "triggers.conversation_kinds",
					Value:     kind,
				})
			}
		}
		if rc.Returns.Format != "" && !v.formats[rc.Returns.Format] {
			errs = append(errs, VocabularyConflictError{
				AgentPath: path,
				CapID:     rc.ID,
				Field:     "returns.format",
				Value:     rc.Returns.Format,
			})
		}
	}
	for _, label := range raw.TrustLabels {
		if !v.trustLabels[label] {
			errs = append(errs, VocabularyConflictError{
				AgentPath: path,
				Field:     "trust_labels",
				Value:     label,
			})
		}
	}
	if len(errs) > 0 {
		return errs
	}
	return nil
}

// --- Backward-compat shim: VocabularyExtensions ---
//
// VocabularyExtensions is kept for one release as a compatibility shim for the
// --vocabulary-extensions flag. New deployments should use --vocab-file instead.

// vocabularyExtensionsFile is the on-disk schema for a vocabulary extensions file.
type vocabularyExtensionsFile struct {
	Intents           []string `yaml:"intents"`
	ConversationKinds []string `yaml:"conversation_kinds"`
	TrustLabels       []string `yaml:"trust_labels"`
	Formats           []string `yaml:"formats"`
}

// VocabularyExtensions holds additional vocabulary values.
//
// Deprecated: use a vocabulary file with --vocab-file instead.
type VocabularyExtensions struct {
	Intents           []string
	ConversationKinds []string
	TrustLabels       []string
	Formats           []string
}

// LoadVocabularyExtensions reads the YAML file at path and returns a
// VocabularyExtensions ready to pass to NewFileStore or NewGitStore.
// An empty path is not an error — it returns an empty VocabularyExtensions.
//
// Deprecated: use LoadVocabulary with --vocab-file instead.
func LoadVocabularyExtensions(path string) (VocabularyExtensions, error) {
	if path == "" {
		return VocabularyExtensions{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return VocabularyExtensions{}, fmt.Errorf("load vocabulary extensions %s: %w", path, err)
	}
	var f vocabularyExtensionsFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return VocabularyExtensions{}, fmt.Errorf("parse vocabulary extensions %s: %w", path, err)
	}
	return VocabularyExtensions{
		Intents:           f.Intents,
		ConversationKinds: f.ConversationKinds,
		TrustLabels:       f.TrustLabels,
		Formats:           f.Formats,
	}, nil
}

// resolveVocabulary loads the vocabulary from vocabPath and applies any
// deprecated VocabularyExtensions shim. If vocabPath is empty, returns a nil
// *vocabulary (ValidateOpen) unless extensions are provided, in which case
// the extensions form the vocabulary (for compat).
func resolveVocabulary(vocabPath string, ext VocabularyExtensions) (*vocabulary, ValidationMode, error) {
	hasExt := len(ext.Intents) > 0 || len(ext.ConversationKinds) > 0 ||
		len(ext.TrustLabels) > 0 || len(ext.Formats) > 0

	if vocabPath != "" {
		v, err := loadVocabulary(vocabPath)
		if err != nil {
			return nil, ValidateOpen, err
		}
		if hasExt {
			slog.Warn("--vocabulary-extensions is deprecated; values merged into --vocab-file vocabulary. Use --vocab-file only in future deployments.",
				"vocab_path", vocabPath)
			v = v.mergeExtensions(ext)
		}
		return v, ValidateStrict, nil
	}

	if hasExt {
		// Compat: extensions-only path — build a vocabulary from extensions alone.
		slog.Warn("--vocabulary-extensions is deprecated; migrate to --vocab-file.")
		v := (*vocabulary)(nil)
		v = v.mergeExtensions(ext)
		return v, ValidateStrict, nil
	}

	// No vocab file, no extensions: ValidateOpen — no vocabulary checking.
	return nil, ValidateOpen, nil
}

// cleanVocabPath returns the cleaned absolute path to the vocab file's parent
// directory for fsnotify, and the cleaned absolute file path.
// Returns an error if the path is empty or cannot be made absolute.
func cleanVocabPath(path string) (absFile, absDir string, err error) {
	if path == "" {
		return "", "", fmt.Errorf("vocab path is empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", "", fmt.Errorf("vocab path %s: %w", path, err)
	}
	return abs, filepath.Dir(abs), nil
}
