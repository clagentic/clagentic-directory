package store

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// vocabularyExtensionsFile is the on-disk schema for a vocabulary extensions file.
// Deployers add platform-specific values to any of the four closed enums without
// forking the binary. All four fields are optional; omitted fields are ignored.
//
// Example:
//
//	trust_labels:
//	  - session-logger
//	  - knowledge-writer
//	formats:
//	  - knowledge-record
//	  - knowledge-tasks
type vocabularyExtensionsFile struct {
	Intents           []string `yaml:"intents"`
	ConversationKinds []string `yaml:"conversation_kinds"`
	TrustLabels       []string `yaml:"trust_labels"`
	Formats           []string `yaml:"formats"`
}

// VocabularyExtensions holds additional vocabulary values to merge into the
// base closed enums at store construction time.
type VocabularyExtensions struct {
	Intents           []string
	ConversationKinds []string
	TrustLabels       []string
	Formats           []string
}

// LoadVocabularyExtensions reads the YAML file at path and returns a
// VocabularyExtensions ready to pass to NewFileStore or NewGitStore.
// An empty path is not an error — it returns an empty VocabularyExtensions.
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

// applyExtensions merges ext values into the four base valid-value maps.
// Called once at store construction; the resulting maps are used for all
// subsequent v2 validation. Base values are never removed.
func applyExtensions(ext VocabularyExtensions) {
	for _, v := range ext.Intents {
		v2ValidIntents[v] = true
	}
	for _, v := range ext.ConversationKinds {
		v2ValidConversationKinds[v] = true
	}
	for _, v := range ext.TrustLabels {
		v2ValidTrustLabels[v] = true
	}
	for _, v := range ext.Formats {
		v2ValidFormats[v] = true
	}
}
