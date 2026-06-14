package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileStoreBasic(t *testing.T) {
	dir := t.TempDir()

	// Write a valid agent YAML
	yaml := `schema_version: 1
identity:
  name: reviewer
  version: 1.0.0
  description: Reviews code changes
capabilities:
  - id: review-pr
    name: Review PR
    description: Performs structured code review
    triggers:
      intents: [review, code-review]
      conversation_kinds: [review]
      after_roles: []
      after_agents: []
    returns:
      verdict_field: review_result
      format: json
trust_labels: [trusted]
`
	if err := os.WriteFile(filepath.Join(dir, "reviewer.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	fs, err := NewFileStore(dir, "", VocabularyExtensions{})
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()

	agents := fs.ListAgents()
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}
	if agents[0].Name != "reviewer" {
		t.Errorf("expected name reviewer, got %s", agents[0].Name)
	}

	a, ok := fs.GetAgent("reviewer")
	if !ok {
		t.Fatal("GetAgent returned false for reviewer")
	}
	if a.Version != "1.0.0" {
		t.Errorf("expected version 1.0.0, got %s", a.Version)
	}

	found := fs.FindByCapability("review")
	if len(found) != 1 {
		t.Errorf("FindByCapability(review): expected 1, got %d", len(found))
	}

	found = fs.FindByConversationKind("review")
	if len(found) != 1 {
		t.Errorf("FindByConversationKind(review): expected 1, got %d", len(found))
	}

	found = fs.FindByCapability("nonexistent")
	if len(found) != 0 {
		t.Errorf("FindByCapability(nonexistent): expected 0, got %d", len(found))
	}
}

func TestFileStoreMultipleAgents(t *testing.T) {
	dir := t.TempDir()

	writeAgent := func(name, filename string, kinds []string) {
		kindYAML := ""
		for _, k := range kinds {
			kindYAML += "      - " + k + "\n"
		}
		y := "schema_version: 1\nidentity:\n  name: " + name + "\n  version: 1.0.0\ncapabilities:\n  - id: do-thing\n    name: Do Thing\n    description: Does a thing\n    triggers:\n      intents: []\n      conversation_kinds:\n" + kindYAML + "      after_roles: []\n      after_agents: []\n    returns: {}\n"
		if err := os.WriteFile(filepath.Join(dir, filename), []byte(y), 0644); err != nil {
			t.Fatal(err)
		}
	}

	writeAgent("builder", "builder.yaml", []string{"build"})
	writeAgent("merger", "merger.yaml", []string{"build", "deploy"})
	writeAgent("researcher", "researcher.yaml", []string{"research"})

	fs, err := NewFileStore(dir, "", VocabularyExtensions{})
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()

	if len(fs.ListAgents()) != 3 {
		t.Fatalf("expected 3 agents, got %d", len(fs.ListAgents()))
	}

	found := fs.FindByConversationKind("build")
	if len(found) != 2 {
		t.Errorf("FindByConversationKind(build): expected 2, got %d", len(found))
	}

	found = fs.FindByConversationKind("research")
	if len(found) != 1 {
		t.Errorf("FindByConversationKind(research): expected 1, got %d", len(found))
	}
}

func TestFileStoreReload(t *testing.T) {
	dir := t.TempDir()

	yaml1 := "schema_version: 1\nidentity:\n  name: agent-one\n  version: 1.0.0\ncapabilities: []\n"
	if err := os.WriteFile(filepath.Join(dir, "agent-one.yaml"), []byte(yaml1), 0644); err != nil {
		t.Fatal(err)
	}

	fs, err := NewFileStore(dir, "", VocabularyExtensions{})
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()

	if len(fs.ListAgents()) != 1 {
		t.Fatalf("initial load: expected 1 agent")
	}

	yaml2 := "schema_version: 1\nidentity:\n  name: agent-two\n  version: 1.0.0\ncapabilities: []\n"
	if err := os.WriteFile(filepath.Join(dir, "agent-two.yaml"), []byte(yaml2), 0644); err != nil {
		t.Fatal(err)
	}

	if err := fs.Reload(); err != nil {
		t.Fatal(err)
	}

	if len(fs.ListAgents()) != 2 {
		t.Fatalf("after reload: expected 2 agents, got %d", len(fs.ListAgents()))
	}
}
