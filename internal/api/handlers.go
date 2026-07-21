package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/clagentic/clagentic-directory/internal/store"
)

// Handler holds all HTTP handlers for the directory API.
type Handler struct {
	store     store.Store
	authToken string // empty = no authentication required
}

// New returns a new Handler wired to the given store.
// authToken is optional: when non-empty, all routes except /healthz require
// Authorization: Bearer <authToken>.
func New(s store.Store, authToken string) *Handler {
	return &Handler{store: s, authToken: authToken}
}

// requireAuth wraps h with bearer token enforcement.
// /healthz is always exempt — load balancers probe it without credentials.
func (h *Handler) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	if h.authToken == "" {
		return next // auth disabled
	}
	return func(w http.ResponseWriter, r *http.Request) {
		bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if bearer != h.authToken {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// Register registers all routes on mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/agents", h.requireAuth(h.listAgents))
	mux.HandleFunc("GET /v1/agents/{name}", h.requireAuth(h.getAgent))
	mux.HandleFunc("GET /v1/find", h.requireAuth(h.find))
	mux.HandleFunc("GET /healthz", h.healthz) // always unauthenticated
	mux.HandleFunc("GET /readyz", h.requireAuth(h.readyz))
	mux.HandleFunc("GET /.well-known/agent-card.json/{name}", h.requireAuth(h.agentCard))
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("json encode error", "err", err)
	}
}

func agentToMap(a store.Agent) map[string]any {
	caps := make([]map[string]any, 0, len(a.Capabilities))
	for _, c := range a.Capabilities {
		caps = append(caps, map[string]any{
			"id":          c.ID,
			"name":        c.Name,
			"description": c.Description,
			"triggers": map[string]any{
				"intents":            c.Triggers.Intents,
				"conversation_kinds": c.Triggers.ConversationKinds,
				"after_roles":        c.Triggers.AfterRoles,
				"after_agents":       c.Triggers.AfterAgents,
			},
			"returns": map[string]any{
				"verdict_field": c.Returns.VerdictField,
				"format":        c.Returns.Format,
			},
		})
	}
	return map[string]any{
		"name":         a.Name,
		"version":      a.Version,
		"description":  a.Description,
		"capabilities": caps,
		"trust_labels": a.TrustLabels,
	}
}

func (h *Handler) listAgents(w http.ResponseWriter, r *http.Request) {
	agents := h.store.ListAgents()
	out := make([]map[string]any, 0, len(agents))
	for _, a := range agents {
		out = append(out, agentToMap(a))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) getAgent(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	a, ok := h.store.GetAgent(name)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent not found"})
		return
	}
	writeJSON(w, http.StatusOK, agentToMap(a))
}

func (h *Handler) find(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var agents []store.Agent
	switch {
	case q.Get("intent") != "":
		agents = h.store.FindByCapability(q.Get("intent"))
	case q.Get("conversation_kind") != "":
		agents = h.store.FindByConversationKind(q.Get("conversation_kind"))
	case q.Get("after_agent") != "":
		agents = h.store.FindBySequencing(q.Get("after_agent"))
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "one of intent, conversation_kind, or after_agent is required"})
		return
	}
	out := make([]map[string]any, 0, len(agents))
	for _, a := range agents {
		out = append(out, agentToMap(a))
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *Handler) healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) readyz(w http.ResponseWriter, r *http.Request) {
	if len(h.store.ListAgents()) == 0 {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "no agents loaded"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) agentCard(w http.ResponseWriter, r *http.Request) {
	// A2A GA v1.0-conformant agent card, mapped from the registry entry.
	name := r.PathValue("name")
	// Strip .json suffix if present
	name = strings.TrimSuffix(name, ".json")
	a, ok := h.store.GetAgent(name)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "agent not found"})
		return
	}
	writeJSON(w, http.StatusOK, agentToA2ACard(a, requestBaseURL(r), h.authToken != ""))
}

// requestBaseURL derives the externally-reachable scheme+host for this request.
// The directory has no configured public base URL, so supportedInterfaces.url
// is derived from the incoming request rather than a hardcoded value.
func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	// Respect a reverse-proxy-set forwarded scheme when present.
	if fp := r.Header.Get("X-Forwarded-Proto"); fp != "" {
		scheme = fp
	}
	return scheme + "://" + r.Host
}

// a2aSkillsFromCapabilities maps native clagentic capabilities to GA AgentSkill
// entries. GA `skills` is the semantic replacement for the pre-GA `capabilities`
// array; GA `capabilities` (AgentCapabilities) is a distinct feature-flag object
// and must not be conflated with this.
func a2aSkillsFromCapabilities(caps []store.Capability) []map[string]any {
	skills := make([]map[string]any, 0, len(caps))
	for _, c := range caps {
		skills = append(skills, map[string]any{
			"id":          c.ID,
			"name":        c.Name,
			"description": c.Description,
			"tags":        c.Triggers.Intents,
		})
	}
	return skills
}

// agentToA2ACard maps a registry Agent to a GA v1.0-conformant AgentCard.
// baseURL is the scheme+host this card is being served from (used to populate
// supportedInterfaces.url); declareAuth indicates whether the directory has a
// bearer token configured, in which case the card declares (but does not
// enforce — enforcement is server middleware, unchanged by this mapping) an
// HTTP bearer security scheme.
func agentToA2ACard(a store.Agent, baseURL string, declareAuth bool) map[string]any {
	card := map[string]any{
		"name":        a.Name,
		"description": a.Description,
		"version":     a.Version,
		"supportedInterfaces": []map[string]any{
			{
				"url":             baseURL + "/v1/agents/" + a.Name,
				"protocolBinding": "HTTP+JSON",
				"protocolVersion": "1.0",
			},
		},
		"capabilities":       map[string]any{},
		"defaultInputModes":  []string{"application/json"},
		"defaultOutputModes": []string{"application/json"},
		"skills":             a2aSkillsFromCapabilities(a.Capabilities),
	}
	if declareAuth {
		card["securitySchemes"] = map[string]any{
			"bearerAuth": map[string]any{
				"httpAuthSecurityScheme": map[string]any{
					"description": "Bearer token required for all routes except /healthz.",
					"scheme":      "bearer",
				},
			},
		}
		card["securityRequirements"] = []map[string]any{
			{"schemes": map[string]any{"bearerAuth": map[string]any{}}},
		}
	}
	return card
}
