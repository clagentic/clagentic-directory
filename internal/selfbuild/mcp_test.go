package selfbuild_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"forgejo.akuehner.com/clagentic/clagentic-directory/internal/selfbuild"
	"gopkg.in/yaml.v3"
)

// mockMCPTools is the fixture returned by the mock MCP server.
var mockMCPTools = []map[string]interface{}{
	{
		"name":        "search-codebase",
		"description": "Search the codebase for a pattern and return JSON results.",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"pattern": map[string]interface{}{"type": "string"},
			},
		},
	},
	{
		"name":        "read-file",
		"description": "Read a file and return its contents as markdown.",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"path": map[string]interface{}{"type": "string"},
			},
		},
	},
}

func startMockMCPServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if req["method"] != "tools/list" {
			http.Error(w, "method not found", http.StatusNotFound)
			return
		}
		resp := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      req["id"],
			"result":  map[string]interface{}{"tools": mockMCPTools},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
}

func TestMCPDiscovery_Inspect(t *testing.T) {
	srv := startMockMCPServer(t)
	defer srv.Close()

	baseDir := t.TempDir()
	disc := selfbuild.NewMCPDiscovery(selfbuild.MCPConfig{BaseDir: baseDir})

	path, err := disc.Inspect(context.Background(), "test-agent", srv.URL)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}

	// File must be inside proposed_changes/, not the live registry.
	if filepath.Dir(path) != filepath.Join(baseDir, "proposed_changes") {
		t.Errorf("proposed change written outside proposed_changes/: %s", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}

	var pc selfbuild.ProposedChange
	if err := yaml.Unmarshal(data, &pc); err != nil {
		t.Fatalf("unmarshal proposed change: %v", err)
	}

	if pc.Source != "mcp-discovery" {
		t.Errorf("Source = %q, want %q", pc.Source, "mcp-discovery")
	}
	if pc.AgentName != "test-agent" {
		t.Errorf("AgentName = %q, want %q", pc.AgentName, "test-agent")
	}
	if len(pc.Capabilities) != 2 {
		t.Fatalf("len(Capabilities) = %d, want 2", len(pc.Capabilities))
	}

	// First capability: search-codebase
	cap0 := pc.Capabilities[0]
	if cap0.ID.Value != "search-codebase" {
		t.Errorf("cap[0].ID = %q, want %q", cap0.ID.Value, "search-codebase")
	}
	if cap0.ID.Confidence != selfbuild.ConfidenceExtracted {
		t.Errorf("cap[0].ID.Confidence = %q, want extracted", cap0.ID.Confidence)
	}
	if cap0.Format.Value != "json" {
		t.Errorf("cap[0].Format = %q, want json", cap0.Format.Value)
	}

	// Intents must include the canonical name.
	foundCanonical := false
	for _, intent := range cap0.Intents.Values {
		if intent == "search-codebase" {
			foundCanonical = true
		}
	}
	if !foundCanonical {
		t.Errorf("cap[0].Intents does not include canonical name; got %v", cap0.Intents.Values)
	}
}

func TestMCPDiscovery_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	baseDir := t.TempDir()
	disc := selfbuild.NewMCPDiscovery(selfbuild.MCPConfig{BaseDir: baseDir})

	_, err := disc.Inspect(context.Background(), "broken-agent", srv.URL)
	if err == nil {
		t.Fatal("expected error from 500 response, got nil")
	}
}

func TestMCPDiscovery_NoDirectRegistryWrite(t *testing.T) {
	srv := startMockMCPServer(t)
	defer srv.Close()

	baseDir := t.TempDir()
	disc := selfbuild.NewMCPDiscovery(selfbuild.MCPConfig{BaseDir: baseDir})

	_, err := disc.Inspect(context.Background(), "my-agent", srv.URL)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}

	// proposed_changes/ must exist; nothing directly in baseDir (except that subdir).
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if e.Name() != "proposed_changes" {
			t.Errorf("unexpected file/dir in baseDir: %s", e.Name())
		}
	}
}
