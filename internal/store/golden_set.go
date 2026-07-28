package store

// goldenCase is one golden-set query: a free-form intent query string and the
// set of agent names that are an acceptable rank-0 (recall@1) result.
//
// wantAny holds more than one name only when multiple registered agents
// genuinely share the same matched intent/role today (verified against
// goldenFixtureAgents, not guessed) — e.g. both miller and holden declare
// the deep-analysis intent, so either is a legitimately correct top-1 answer
// until the registry disambiguates them (that disambiguation is Defect B,
// out of scope for this task; see clagentic-config lr-3834c4).
type goldenCase struct {
	// query is the raw, unnormalized intent string a caller might send.
	query string
	// wantAny lists agent names, any one of which is an acceptable rank-0
	// result. A nil/empty slice marks the case unresolvable (see
	// goldenUnresolved) and it is excluded from recall/MRR accounting.
	wantAny []string
	// note documents why this case is in the set (a prior recurrence, a
	// natural phrasing seed, or a regression guard for a working query).
	note string
}

// goldenUnresolved reports whether a case has no acceptable answer in the
// current fixture (see the "plan"/avasarala case below) and must be
// excluded from recall@k/MRR denominators rather than silently scored as a
// pass or fail against a fabricated expectation.
func (c goldenCase) goldenUnresolved() bool {
	return len(c.wantAny) == 0
}

// goldenSet is the committed regression fixture for lr-dab7e0. It measures
// recall@1 / recall@3 / MRR against goldenFixtureAgents (a synced snapshot of
// the 18-agent live clagentic-config registry — see golden_fixture.go).
//
// Composition, per lr-dab7e0 acceptance criterion 1:
//   - The six reproductions confirmed live and failing on 2026-07-28
//     (diagnose, debug, plan, security-audit, review-a-pull-request,
//     troubleshoot-a-failure).
//   - Natural-language phrasings seeded broadly across all 18 registered
//     agents — not authored solely from the phrasings that prompted this
//     task (that bias caused lr-9b7435 and lr-044f4d to both recur).
//   - Two known-working regression guards (research -> prax, write-code ->
//     amos) that must not regress when Tier 4 is added.
//
// Note on "19 registered agents": the task brief (lr-dab7e0) and the prior
// session's live reproduction both refer to a 19th agent, "avasarala"
// (intent=plan, "AVASARALA registered"). No avasarala.yaml exists in the
// live clagentic-config registry as of this session (verified: exactly 18
// agent files present, see golden_fixture.go doc comment). The "plan" case
// below is retained as a confirmed-failing repro per the task's explicit
// list, with wantAny left empty — goldenUnresolved() excludes it from
// recall/MRR rather than silently passing it against a fabricated
// expectation or dropping it outright.
var goldenSet = []goldenCase{
	// --- Confirmed-failing reproductions (lr-dab7e0 repro, 2026-07-28) ---
	{query: "diagnose", wantAny: []string{"miller"}, note: "repro: MILLER is the diagnosis agent"},
	{query: "debug", wantAny: []string{"miller"}, note: "repro"},
	{query: "plan", wantAny: nil, note: "repro: task brief names 'avasarala' but no such registry entry exists in the current fixture; unresolvable, excluded from recall/MRR, tracked separately"},
	{query: "security-audit", wantAny: []string{"bobbie"}, note: "repro: BOBBIE is the security-audit gate"},
	{query: "review-a-pull-request", wantAny: []string{"bobbie"}, note: "repro: only registered PR-diff reviewer in current fixture is bobbie (code-review intent); no peaches.yaml present"},
	{query: "troubleshoot-a-failure", wantAny: []string{"miller"}, note: "repro"},

	// --- Known-working regression guards (must not regress) ---
	{query: "research", wantAny: []string{"prax"}, note: "canonical crew agent ranks ahead of gemini-researcher fallback"},
	{query: "write-code", wantAny: []string{"amos"}, note: "canonical write-code intent"},

	// --- Natural phrasings seeded broadly across all 18 registered agents ---
	// amos (builder)
	{query: "build", wantAny: []string{"amos"}, note: "synonym tier"},
	{query: "implement", wantAny: []string{"amos"}, note: "synonym tier"},
	{query: "write some code", wantAny: []string{"amos"}, note: "NL phrasing, normalized"},
	{query: "fix", wantAny: []string{"amos"}, note: "synonym tier"},
	{query: "code-generation", wantAny: []string{"amos"}, note: "exact tier"},

	// ashford (ops)
	{query: "install-binary", wantAny: []string{"ashford"}, note: "exact tier"},
	{query: "rotate-token", wantAny: []string{"ashford"}, note: "exact tier"},
	{query: "restart-service", wantAny: []string{"ashford"}, note: "exact tier"},
	{query: "restart a service", wantAny: []string{"ashford"}, note: "NL phrasing"},
	{query: "ops-check", wantAny: []string{"ashford"}, note: "exact tier"},

	// bobbie (security-audit gate)
	{query: "audit", wantAny: []string{"bobbie"}, note: "synonym tier"},
	{query: "security", wantAny: []string{"bobbie"}, note: "synonym tier"},
	{query: "security-review", wantAny: []string{"bobbie", "opus"}, note: "exact tier, shared intent"},

	// codex
	{query: "delegate-to-codex", wantAny: []string{"codex"}, note: "exact tier"},
	{query: "use gpt", wantAny: []string{"codex"}, note: "NL phrasing"},
	{query: "ask codex for a second opinion", wantAny: []string{"codex"}, note: "NL phrasing"},

	// drummer (platform observer)
	{query: "probe", wantAny: []string{"drummer", "roci", "test-agent"}, note: "shared exact intent across 3 agents"},
	{query: "wiring-test", wantAny: []string{"drummer", "roci", "test-agent"}, note: "shared exact intent"},
	{query: "run a platform health check", wantAny: []string{"drummer"}, note: "NL phrasing, description-only match (BM25 target)"},

	// gemini-researcher
	{query: "web-search", wantAny: []string{"gemini-researcher"}, note: "exact tier"},
	{query: "search google for this", wantAny: []string{"gemini-researcher"}, note: "NL phrasing"},

	// holden (project lead)
	{query: "architecture-review", wantAny: []string{"holden", "opus"}, note: "shared exact intent"},
	{query: "triage my tasks", wantAny: []string{"holden"}, note: "NL phrasing, description-only match"},
	{query: "who should handle this dispatch", wantAny: []string{"holden"}, note: "NL phrasing, description-only match"},

	// miller (diagnostician)
	{query: "deep-analysis", wantAny: []string{"miller", "holden"}, note: "shared exact intent"},
	{query: "root cause analysis", wantAny: []string{"miller"}, note: "NL phrasing, description-only match"},
	{query: "what broke", wantAny: []string{"miller"}, note: "NL phrasing, description-only match"},
	{query: "diagnostician", wantAny: []string{"miller", "drummer"}, note: "role match tier; both declare role=diagnostician"},

	// naomi (release gate)
	{query: "merge-pr", wantAny: []string{"naomi"}, note: "exact tier"},
	{query: "merge", wantAny: []string{"naomi"}, note: "synonym tier"},
	{query: "release", wantAny: []string{"naomi"}, note: "exact tier"},
	{query: "cut a release", wantAny: []string{"naomi"}, note: "NL phrasing"},

	// ollama (local inference)
	{query: "local-inference", wantAny: []string{"ollama"}, note: "exact tier"},
	{query: "cheap-inference", wantAny: []string{"ollama"}, note: "exact tier"},
	{query: "run this on a local model", wantAny: []string{"ollama"}, note: "NL phrasing"},
	{query: "embeddings", wantAny: []string{"ollama"}, note: "exact tier"},

	// opus (deep reasoning)
	{query: "tradeoff-evaluation", wantAny: []string{"opus", "holden"}, note: "shared exact intent"},
	{query: "deep reasoning about this design", wantAny: []string{"opus"}, note: "NL phrasing, description-only match"},

	// prax (researcher)
	{query: "web-research", wantAny: []string{"prax", "web-researcher"}, note: "shared exact intent"},
	{query: "fact-lookup", wantAny: []string{"prax", "web-researcher"}, note: "shared exact intent"},
	{query: "look something up for me", wantAny: []string{"prax"}, note: "NL phrasing, description-only match"},
	{query: "codebase-survey", wantAny: []string{"prax"}, note: "exact tier"},

	// reddit-researcher
	{query: "reddit-research", wantAny: []string{"reddit-researcher"}, note: "exact tier"},
	{query: "what does reddit think", wantAny: []string{"reddit-researcher"}, note: "NL phrasing, description-only match"},
	{query: "community-sentiment", wantAny: []string{"reddit-researcher", "prax"}, note: "shared exact intent"},

	// roci (agent builder)
	{query: "scaffold a new agent", wantAny: []string{"roci"}, note: "NL phrasing, description-only match"},
	{query: "create an agent definition", wantAny: []string{"roci"}, note: "NL phrasing, description-only match"},

	// test-agent
	{query: "wiring probe", wantAny: []string{"test-agent", "drummer", "roci"}, note: "NL phrasing normalizes toward wiring-test-adjacent term, shared across agents"},

	// tiamut (intelligence harvester)
	{query: "inspect-repo", wantAny: []string{"tiamut"}, note: "exact tier"},
	{query: "harvest-intelligence", wantAny: []string{"tiamut"}, note: "exact tier"},
	{query: "scan this repo for useful findings", wantAny: []string{"tiamut"}, note: "NL phrasing, description-only match"},

	// web-researcher
	{query: "url-fetch", wantAny: []string{"web-researcher"}, note: "exact tier"},
	{query: "read this single url and summarize it", wantAny: []string{"web-researcher"}, note: "NL phrasing, description-only match"},

	// code review NL phrasing (repro-adjacent)
	{query: "review a pull request", wantAny: []string{"bobbie"}, note: "NL phrasing; only registered PR-diff reviewer in current fixture is bobbie (code-review intent)"},
	{query: "code review", wantAny: []string{"bobbie"}, note: "exact tier (code-review)"},
}
