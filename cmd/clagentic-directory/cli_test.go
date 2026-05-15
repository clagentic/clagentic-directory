package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mockMCPTools is the fixture returned by the CLI test's mock MCP server.
var cliMockMCPTools = []map[string]interface{}{
	{
		"name":        "list-agents",
		"description": "List all registered agents and return JSON.",
		"inputSchema": map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
	},
	{
		"name":        "get-agent",
		"description": "Get a single agent entry by name.",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name": map[string]interface{}{"type": "string"},
			},
		},
	},
	{
		"name":        "propose-capability",
		"description": "Propose a new capability in YAML format.",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"yaml": map[string]interface{}{"type": "string"},
			},
		},
	},
}

func startCLIMockMCPServer(t *testing.T) *httptest.Server {
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
			"result":  map[string]interface{}{"tools": cliMockMCPTools},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp) //nolint:errcheck
	}))
}

func TestInspectSubcommand_Success(t *testing.T) {
	srv := startCLIMockMCPServer(t)
	defer srv.Close()

	outDir := t.TempDir()

	code := runInspect([]string{
		"--agent", "fakeagent",
		"--mcp-url", srv.URL,
		"--output-dir", outDir,
	})
	if code != 0 {
		t.Fatalf("runInspect returned %d, want 0", code)
	}

	// Expect proposed_changes/ to exist under outDir.
	propDir := filepath.Join(outDir, "proposed_changes")
	entries, err := os.ReadDir(propDir)
	if err != nil {
		t.Fatalf("ReadDir proposed_changes: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 file in proposed_changes/, got %d", len(entries))
	}

	name := entries[0].Name()
	if !strings.HasPrefix(name, "fakeagent.") || !strings.HasSuffix(name, ".yaml") {
		t.Errorf("unexpected file name %q; want fakeagent.<timestamp>.yaml", name)
	}

	// Read and spot-check the YAML content.
	data, err := os.ReadFile(filepath.Join(propDir, name))
	if err != nil {
		t.Fatalf("read proposed change file: %v", err)
	}
	content := string(data)
	// Agent name must appear.
	if !strings.Contains(content, "agent_name: fakeagent") {
		t.Errorf("proposed change missing agent_name field; content:\n%s", content)
	}
	// Source must be mcp-discovery.
	if !strings.Contains(content, "source: mcp-discovery") {
		t.Errorf("proposed change missing source field; content:\n%s", content)
	}
	// All three tool names must appear.
	for _, tool := range []string{"list-agents", "get-agent", "propose-capability"} {
		if !strings.Contains(content, tool) {
			t.Errorf("proposed change missing tool %q; content:\n%s", tool, content)
		}
	}
}

func TestInspectSubcommand_MissingAgent(t *testing.T) {
	code := runInspect([]string{"--mcp-url", "http://localhost:9999"})
	if code == 0 {
		t.Fatal("runInspect should return non-zero when --agent is missing")
	}
}

func TestInspectSubcommand_MissingMCPURL(t *testing.T) {
	code := runInspect([]string{"--agent", "myagent"})
	if code == 0 {
		t.Fatal("runInspect should return non-zero when --mcp-url is missing")
	}
}

func TestInspectSubcommand_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer srv.Close()

	outDir := t.TempDir()
	code := runInspect([]string{
		"--agent", "badagent",
		"--mcp-url", srv.URL,
		"--output-dir", outDir,
	})
	if code == 0 {
		t.Fatal("runInspect should return non-zero when MCP server errors")
	}
}

func TestInspectSubcommand_DefaultOutputDir(t *testing.T) {
	// When --output-dir is omitted and no config exists, output goes to ./proposed_changes
	// relative to the process working directory. We change to a temp dir for isolation.
	srv := startCLIMockMCPServer(t)
	defer srv.Close()

	tmpDir := t.TempDir()
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	defer os.Chdir(origDir) //nolint:errcheck

	// Use a non-existent config path so loadConfig returns an empty config.
	code := runInspect([]string{
		"--agent", "defaultagent",
		"--mcp-url", srv.URL,
		"--config", filepath.Join(tmpDir, "nonexistent.yaml"),
	})
	if code != 0 {
		t.Fatalf("runInspect returned %d, want 0", code)
	}

	propDir := filepath.Join(tmpDir, "proposed_changes")
	entries, err := os.ReadDir(propDir)
	if err != nil {
		t.Fatalf("ReadDir proposed_changes: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one file in proposed_changes/, got 0")
	}
}
