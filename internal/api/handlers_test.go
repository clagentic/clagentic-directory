package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/clagentic/clagentic-directory/internal/store"
)

// fakeStore is a minimal store.Store backed by an in-memory map, for handler tests.
type fakeStore struct {
	agents map[string]store.Agent
}

func (f *fakeStore) ListAgents() []store.Agent {
	out := make([]store.Agent, 0, len(f.agents))
	for _, a := range f.agents {
		out = append(out, a)
	}
	return out
}

func (f *fakeStore) GetAgent(name string) (store.Agent, bool) {
	a, ok := f.agents[name]
	return a, ok
}

func (f *fakeStore) FindByCapability(intents ...string) []store.Agent { return nil }
func (f *fakeStore) FindByConversationKind(kind string) []store.Agent { return nil }
func (f *fakeStore) FindBySequencing(afterAgent string) []store.Agent { return nil }
func (f *fakeStore) Reload() error                                    { return nil }

func newTestAgent() store.Agent {
	return store.Agent{
		Name:        "reviewer",
		Version:     "1.0.0",
		Description: "Performs structured code review on pull requests and commits.",
		Capabilities: []store.Capability{
			{
				ID:          "review-pr",
				Name:        "Review Pull Request",
				Description: "Reads a PR or commit diff and emits structured findings against a rulebook.",
				Triggers: store.Triggers{
					Intents:           []string{"code-review", "review-pr", "review-commit"},
					ConversationKinds: []string{"review"},
				},
				Returns: store.Returns{
					VerdictField: "review_result",
					Format:       "structured-markdown",
				},
			},
		},
		TrustLabels: []string{"trusted"},
	}
}

// TestAgentCardGAConformance asserts the served card matches the A2A GA v1.0
// AgentCard field inventory (a2a-sdk 1.1.1, protobuf lf.a2a.v1):
// see /workspace/a2a/findings/fixtures/agentcard-auth/card-field-inventory.md
// and 01-agent-card.json for the authoritative wire shape this test is driven
// from.
func TestAgentCardGAConformance(t *testing.T) {
	s := &fakeStore{agents: map[string]store.Agent{"reviewer": newTestAgent()}}
	h := New(s, "")
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/.well-known/agent-card.json/reviewer", nil)
	req.Host = "directory.example.invalid"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var card map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &card); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// GA dropped the pre-GA schemaVersion field entirely (see
	// card-field-inventory.md AgentCard root table — no schema_version row,
	// and it is absent from 01-agent-card.json). Never re-add it.
	if _, present := card["schemaVersion"]; present {
		t.Error("schemaVersion must not be present on a GA AgentCard")
	}

	// Root fields present in the GA inventory.
	if got := card["name"]; got != "reviewer" {
		t.Errorf("name: got %v, want reviewer", got)
	}
	if got := card["version"]; got != "1.0.0" {
		t.Errorf("version: got %v, want 1.0.0", got)
	}
	if got := card["description"]; got != "Performs structured code review on pull requests and commits." {
		t.Errorf("description: got %v", got)
	}

	// supportedInterfaces replaces the pre-GA top-level url/preferred_transport
	// (card-field-inventory.md: "1.1.1 has NO top-level url/preferred_transport").
	if _, present := card["url"]; present {
		t.Error("top-level url must not be present on a GA AgentCard")
	}
	ifaces, ok := card["supportedInterfaces"].([]any)
	if !ok || len(ifaces) != 1 {
		t.Fatalf("expected 1 supportedInterfaces entry, got %v", card["supportedInterfaces"])
	}
	iface, ok := ifaces[0].(map[string]any)
	if !ok {
		t.Fatalf("supportedInterfaces[0] is not an object: %v", ifaces[0])
	}
	if got := iface["url"]; got != "http://directory.example.invalid/v1/agents/reviewer" {
		t.Errorf("supportedInterfaces[0].url: got %v", got)
	}
	if got := iface["protocolBinding"]; got != "HTTP+JSON" {
		t.Errorf("supportedInterfaces[0].protocolBinding: got %v", got)
	}
	if got := iface["protocolVersion"]; got != "1.0" {
		t.Errorf("supportedInterfaces[0].protocolVersion: got %v", got)
	}

	// capabilities is the GA AgentCapabilities feature-flag object, NOT the
	// native clagentic capabilities array (card-field-inventory.md:
	// AgentCapabilities is streaming/pushNotifications/extensions/
	// extendedAgentCard only).
	capsObj, ok := card["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("capabilities must be an AgentCapabilities object, got %T: %v", card["capabilities"], card["capabilities"])
	}
	for _, forbidden := range []string{"triggers", "returns", "id"} {
		if _, present := capsObj[forbidden]; present {
			t.Errorf("capabilities must not carry native capability fields, found %q", forbidden)
		}
	}

	// defaultInputModes / defaultOutputModes are GA-only fields, absent pre-GA.
	if modes, ok := card["defaultInputModes"].([]any); !ok || len(modes) == 0 {
		t.Errorf("defaultInputModes missing or empty: %v", card["defaultInputModes"])
	}
	if modes, ok := card["defaultOutputModes"].([]any); !ok || len(modes) == 0 {
		t.Errorf("defaultOutputModes missing or empty: %v", card["defaultOutputModes"])
	}

	// skills is the GA semantic replacement for the pre-GA capabilities array.
	skills, ok := card["skills"].([]any)
	if !ok || len(skills) != 1 {
		t.Fatalf("expected 1 skill, got %v", card["skills"])
	}
	skill, ok := skills[0].(map[string]any)
	if !ok {
		t.Fatalf("skills[0] is not an object: %v", skills[0])
	}
	if got := skill["id"]; got != "review-pr" {
		t.Errorf("skills[0].id: got %v", got)
	}
	if got := skill["name"]; got != "Review Pull Request" {
		t.Errorf("skills[0].name: got %v", got)
	}
	tags, ok := skill["tags"].([]any)
	if !ok || len(tags) != 3 {
		t.Fatalf("skills[0].tags: got %v", skill["tags"])
	}

	// Declaration-only security: no auth token configured on this handler, so
	// no security scheme should be declared.
	if _, present := card["securitySchemes"]; present {
		t.Error("securitySchemes must be absent when no auth token is configured")
	}
	if _, present := card["securityRequirements"]; present {
		t.Error("securityRequirements must be absent when no auth token is configured")
	}
}

// TestAgentCardDeclaresAuthWhenConfigured asserts that when the directory has
// a bearer token configured, the card declares an HTTP bearer security scheme.
// Declaration only — enforcement remains server middleware and is unchanged
// by this mapping (card-field-inventory.md: "Enforcement is NOT provided by
// the SDK server stack").
func TestAgentCardDeclaresAuthWhenConfigured(t *testing.T) {
	s := &fakeStore{agents: map[string]store.Agent{"reviewer": newTestAgent()}}
	h := New(s, "secret-token")
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/.well-known/agent-card.json/reviewer", nil)
	req.Host = "directory.example.invalid"
	req.Header.Set("Authorization", "Bearer secret-token")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var card map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &card); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	schemes, ok := card["securitySchemes"].(map[string]any)
	if !ok {
		t.Fatalf("expected securitySchemes object, got %v", card["securitySchemes"])
	}
	if _, present := schemes["bearerAuth"]; !present {
		t.Errorf("expected bearerAuth scheme declared, got %v", schemes)
	}

	reqs, ok := card["securityRequirements"].([]any)
	if !ok || len(reqs) != 1 {
		t.Fatalf("expected 1 securityRequirements entry, got %v", card["securityRequirements"])
	}
}

// TestRequestBaseURLRejectsBogusForwardedProto asserts that an
// X-Forwarded-Proto value other than exactly "http" or "https" is ignored,
// falling back to the request's own scheme, rather than being passed through
// into supportedInterfaces[].url. Guards against malformed/injected schemes
// (e.g. "javascript:", CRLF) reaching the served card — matches the
// http/https-only posture enforced elsewhere in this project by
// validateSelfBuildURL().
func TestRequestBaseURLRejectsBogusForwardedProto(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   string
	}{
		{"valid https is honored", "https", "https://directory.example.invalid"},
		{"valid http is honored", "http", "http://directory.example.invalid"},
		{"javascript scheme is ignored", "javascript:alert(1)", "http://directory.example.invalid"},
		{"CRLF injection is ignored", "http\r\nX-Injected: yes", "http://directory.example.invalid"},
		{"arbitrary garbage is ignored", "ftp", "http://directory.example.invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/.well-known/agent-card.json/reviewer", nil)
			req.Host = "directory.example.invalid"
			req.Header.Set("X-Forwarded-Proto", tc.header)
			if got := requestBaseURL(req); got != tc.want {
				t.Errorf("requestBaseURL: got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestAgentCardIgnoresBogusForwardedProtoEndToEnd asserts the handler-level
// behavior: a bogus X-Forwarded-Proto must not leak into the served card's
// supportedInterfaces[].url.
func TestAgentCardIgnoresBogusForwardedProtoEndToEnd(t *testing.T) {
	s := &fakeStore{agents: map[string]store.Agent{"reviewer": newTestAgent()}}
	h := New(s, "")
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/.well-known/agent-card.json/reviewer", nil)
	req.Host = "directory.example.invalid"
	req.Header.Set("X-Forwarded-Proto", "javascript:alert(1)")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var card map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &card); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	ifaces, ok := card["supportedInterfaces"].([]any)
	if !ok || len(ifaces) != 1 {
		t.Fatalf("expected 1 supportedInterfaces entry, got %v", card["supportedInterfaces"])
	}
	iface, ok := ifaces[0].(map[string]any)
	if !ok {
		t.Fatalf("supportedInterfaces[0] is not an object: %v", ifaces[0])
	}
	if got := iface["url"]; got != "http://directory.example.invalid/v1/agents/reviewer" {
		t.Errorf("supportedInterfaces[0].url: got %v, want fallback to http scheme", got)
	}
}

// TestAgentCardNotFound preserves existing not-found behavior.
func TestAgentCardNotFound(t *testing.T) {
	s := &fakeStore{agents: map[string]store.Agent{}}
	h := New(s, "")
	mux := http.NewServeMux()
	h.Register(mux)

	req := httptest.NewRequest(http.MethodGet, "/.well-known/agent-card.json/nonexistent", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}
