// Package directory provides a typed Go client for the clagentic-directory HTTP API.
package directory

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Client is a typed Go client for the clagentic-directory service.
type Client struct {
	base string
	http *http.Client
}

// New returns a Client targeting the given base URL (e.g. "http://localhost:8444").
func New(baseURL string) *Client {
	return &Client{
		base: baseURL,
		http: &http.Client{Timeout: 10 * time.Second},
	}
}

// Agent is a loaded agent from the directory.
type Agent struct {
	Name         string       `json:"name"`
	Version      string       `json:"version"`
	Description  string       `json:"description"`
	Capabilities []Capability `json:"capabilities"`
	TrustLabels  []string     `json:"trust_labels"`
}

// Capability is one capability entry.
type Capability struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Triggers    Triggers `json:"triggers"`
	Returns     Returns  `json:"returns"`
}

// Triggers lists when the capability applies.
type Triggers struct {
	Intents           []string `json:"intents"`
	ConversationKinds []string `json:"conversation_kinds"`
	AfterRoles        []string `json:"after_roles"`
	AfterAgents       []string `json:"after_agents"`
}

// Returns describes what the capability returns.
type Returns struct {
	VerdictField string `json:"verdict_field"`
	Format       string `json:"format"`
}

func (c *Client) get(path string, out any) error {
	resp, err := c.http.Get(c.base + path)
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: status %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// ListAgents returns all agents in the directory.
func (c *Client) ListAgents() ([]Agent, error) {
	var agents []Agent
	if err := c.get("/v1/agents", &agents); err != nil {
		return nil, err
	}
	return agents, nil
}

// GetAgent returns the agent with the given name.
func (c *Client) GetAgent(name string) (Agent, error) {
	var agent Agent
	if err := c.get("/v1/agents/"+url.PathEscape(name), &agent); err != nil {
		return Agent{}, err
	}
	return agent, nil
}

// findResponse is the wire shape of GET /v1/find: always a JSON object
// carrying an "agents" array, never a bare array (server-side contract,
// internal/api/handlers.go findResult/emptyFindResult — PEACHES
// amos.code-craft.13, PR #18 comment 5107685720: the match and no-match
// paths used to differ in shape, which this client previously encoded by
// decoding straight into []Agent; that broke once the server made both
// paths consistent). Suggestion is only populated on a no-match response.
type findResponse struct {
	Agents     []Agent `json:"agents"`
	Suggestion string  `json:"suggestion,omitempty"`
}

// FindByIntent returns agents that handle the given intent.
func (c *Client) FindByIntent(intent string) ([]Agent, error) {
	var resp findResponse
	if err := c.get("/v1/find?intent="+url.QueryEscape(intent), &resp); err != nil {
		return nil, err
	}
	return resp.Agents, nil
}

// FindByConversationKind returns agents whose capabilities include the given kind.
func (c *Client) FindByConversationKind(kind string) ([]Agent, error) {
	var resp findResponse
	if err := c.get("/v1/find?conversation_kind="+url.QueryEscape(kind), &resp); err != nil {
		return nil, err
	}
	return resp.Agents, nil
}

// FindByAfterAgent returns agents that sequence after the given agent.
func (c *Client) FindByAfterAgent(agentName string) ([]Agent, error) {
	var resp findResponse
	if err := c.get("/v1/find?after_agent="+url.QueryEscape(agentName), &resp); err != nil {
		return nil, err
	}
	return resp.Agents, nil
}
