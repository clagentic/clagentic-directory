package selfbuild

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// MCPConfig configures the MCP discovery mechanism.
type MCPConfig struct {
	// BaseDir is the root directory where proposed_changes/ will be written.
	BaseDir string
	// HTTPTimeout for MCP server calls.
	HTTPTimeout time.Duration
}

// MCPDiscovery connects to an agent's MCP server, lists its tools, and writes
// a proposed_changes/ entry mapping tool names/descriptions to Capability drafts.
//
// It never writes to the live registry.
type MCPDiscovery struct {
	cfg    MCPConfig
	client *http.Client
}

// NewMCPDiscovery creates an MCPDiscovery with the given config.
func NewMCPDiscovery(cfg MCPConfig) *MCPDiscovery {
	timeout := cfg.HTTPTimeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	return &MCPDiscovery{
		cfg:    cfg,
		client: &http.Client{Timeout: timeout},
	}
}

// mcpRequest is a JSON-RPC 2.0 envelope for MCP calls.
type mcpRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

// mcpResponse is a partial JSON-RPC 2.0 response.
type mcpResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// mcpTool is a single tool returned by tools/list.
type mcpTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema struct {
		Type       string                     `json:"type"`
		Properties map[string]json.RawMessage `json:"properties"`
	} `json:"inputSchema"`
}

type mcpToolsResult struct {
	Tools []mcpTool `json:"tools"`
}

// Inspect queries the MCP server at serverURL for the named agent and writes a
// proposed_changes/ file to cfg.BaseDir. Returns the path written.
func (d *MCPDiscovery) Inspect(ctx context.Context, agentName, serverURL string) (string, error) {
	slog.Info("mcp-discovery: inspecting agent", "agent", agentName, "url", serverURL)

	tools, err := d.listTools(ctx, serverURL)
	if err != nil {
		return "", fmt.Errorf("mcp-discovery: list_tools: %w", err)
	}

	slog.Debug("mcp-discovery: tools listed", "agent", agentName, "count", len(tools))

	caps := make([]ProposedCapability, 0, len(tools))
	for _, t := range tools {
		caps = append(caps, toolToCapability(t))
	}

	pc := ProposedChange{
		SchemaVersion: 1,
		GeneratedAt:   time.Now().UTC(),
		Source:        "mcp-discovery",
		AgentName:     agentName,
		Capabilities:  caps,
		Notes: []string{
			fmt.Sprintf("Discovered %d tools from MCP server at %s", len(tools), serverURL),
		},
	}

	path, err := WriteProposedChange(d.cfg.BaseDir, pc)
	if err != nil {
		return "", err
	}

	slog.Info("mcp-discovery: proposed change written", "agent", agentName, "path", path)
	return path, nil
}

func (d *MCPDiscovery) listTools(ctx context.Context, serverURL string) ([]mcpTool, error) {
	req := mcpRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "tools/list",
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, serverURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := d.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("MCP server returned status %d", resp.StatusCode)
	}

	var rpc mcpResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpc); err != nil {
		return nil, fmt.Errorf("decode MCP response: %w", err)
	}
	if rpc.Error != nil {
		return nil, fmt.Errorf("MCP error: %s", rpc.Error.Message)
	}

	var result mcpToolsResult
	if err := json.Unmarshal(rpc.Result, &result); err != nil {
		return nil, fmt.Errorf("decode tools/list result: %w", err)
	}
	return result.Tools, nil
}

// toolToCapability maps an MCP tool to a ProposedCapability with confidence labels.
func toolToCapability(t mcpTool) ProposedCapability {
	// Tool name is the canonical ID, directly extracted.
	// Intents: split the name on underscores/hyphens — inferred heuristic.
	intents := splitIntoIntents(t.Name)

	// Format: infer from description keywords — inferred.
	format := inferFormat(t.Description)

	return ProposedCapability{
		ID:          AnnotatedString{Value: t.Name, Confidence: ConfidenceExtracted},
		Name:        AnnotatedString{Value: humanizeName(t.Name), Confidence: ConfidenceInferred},
		Description: AnnotatedString{Value: t.Description, Confidence: ConfidenceExtracted},
		Intents:     AnnotatedStrings{Values: intents, Confidence: ConfidenceInferred},
		Format:      AnnotatedString{Value: format, Confidence: ConfidenceInferred},
	}
}

func splitIntoIntents(name string) []string {
	// Replace underscores with hyphens, then use the whole name as the primary intent.
	// Also include any sub-parts as secondary intent labels.
	canonical := strings.ReplaceAll(name, "_", "-")
	parts := strings.FieldsFunc(canonical, func(r rune) bool { return r == '-' })

	seen := map[string]bool{canonical: true}
	result := []string{canonical}
	for _, p := range parts {
		if len(p) > 2 && !seen[p] {
			seen[p] = true
			result = append(result, p)
		}
	}
	return result
}

func humanizeName(name string) string {
	parts := strings.FieldsFunc(name, func(r rune) bool { return r == '_' || r == '-' })
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

func inferFormat(description string) string {
	lower := strings.ToLower(description)
	switch {
	case strings.Contains(lower, "json"):
		return "json"
	case strings.Contains(lower, "markdown"):
		return "structured-markdown"
	case strings.Contains(lower, "yaml"):
		return "yaml"
	case strings.Contains(lower, "text"):
		return "text"
	default:
		return "text"
	}
}
