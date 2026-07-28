package store

// This file provides a fixture snapshot of the live clagentic-config agent
// registry (18 agents, schema_version: 2) for use by the golden-set recall
// benchmark (golden_test.go) and its regression gate.
//
// clagentic-config is a separate repo (out of scope for lr-dab7e0) and is
// not guaranteed to be checked out alongside clagentic-directory in CI, so
// the fields below are a committed, hand-synced copy of each agent's
// identity/capabilities/trust_labels as of 2026-07-28. If the live registry
// changes shape (new agent, renamed intent, etc.), this fixture and
// golden_set.go's cases must be updated together — see goldenFixtureAgents
// doc comment on drift risk.
//
// Only the fields the matcher reads are reproduced: name, description, role,
// capability name/id/description, capability intents, trust_labels. Version
// numbers and non-matching fields are omitted as irrelevant to recall
// measurement.

// goldenFixtureAgents returns a fresh copy of the 18-agent registry snapshot
// for golden-set benchmarking. Returns a map keyed by name, matching the
// shape findByCapability expects.
func goldenFixtureAgents() map[string]Agent {
	agents := []Agent{
		{
			Name:        "amos",
			Description: "Autonomous builder (crew-manifest Layer 2). Picks up exactly one unit of code work per invocation — mode=task hydrates from a LORE task_id, mode=ad_hoc takes a free-form instruction. Builds on a feature branch, runs tests, opens a PR, and merges if the project's .crew/amos.yaml authorizes it. Never picks its own work; never pushes to main directly.",
			Role:        "builder",
			TrustLabels: []string{"trusted", "autonomous", "lore-writer"},
			Capabilities: []Capability{{
				ID:          "implement-task",
				Name:        "Implement Task",
				Description: "Hydrates from a LORE task or ad-hoc instruction, implements the minimal change that satisfies the envelope, runs the project test suite, and opens a PR on a feature branch. Merges if project merge_allowed gate passes (Phase B only).",
				Triggers:    Triggers{Intents: []string{"code-generation", "implement-task"}},
			}},
		},
		{
			Name:        "ashford",
			Description: "Mutating-infrastructure identity and the crew's general local/remote-host operator (crew-manifest Layer 2). Executes exactly ONE enumerated op per invocation — install_binary, rotate_token, restart_service, install_local_package, or run_scoped_command — against a target host (localhost or a remote FQDN) via a fail-closed verb allowlist and a short-lived per-op minted credential. Never Bash(*), never a standing SSH key, never repo-write or merge.",
			Role:        "ops",
			TrustLabels: []string{"trusted", "autonomous", "high-stakes"},
			Capabilities: []Capability{
				{ID: "install-binary", Name: "Install Binary", Description: "Installs a named binary from a package registry onto a target host (localhost or remote FQDN) via a fixed-path wrapper. Destructive; requires HITL approval.", Triggers: Triggers{Intents: []string{"install-binary"}}},
				{ID: "rotate-token", Name: "Rotate Token", Description: "Rotates a credential or token via OpenBao dynamic secrets against a target host or credential store. Destructive; requires HITL approval.", Triggers: Triggers{Intents: []string{"rotate-token"}}},
				{ID: "restart-service", Name: "Restart Service", Description: "Restarts a named service on a target host via a fixed-path wrapper. Not destructive; does not require HITL approval.", Triggers: Triggers{Intents: []string{"restart-service"}}},
				{ID: "install-local-package", Name: "Install Local Package", Description: "Reinstalls a local editable-venv checkout via its own repo-relative install script on localhost. Destructive; requires HITL approval.", Triggers: Triggers{Intents: []string{"install-local-package"}}},
				{ID: "run-scoped-command", Name: "Run Scoped Command", Description: "Runs a host-declared command_template_id registry entry against a target host (localhost or remote FQDN) with typed template params. Mutating templates are destructive by default and require HITL approval; a read-only template entry may opt out.", Triggers: Triggers{Intents: []string{"run-scoped-command"}}},
				{ID: "ops-check", Name: "Ops Check", Description: "Verifies ASHFORD's own wiring, credential mint path, or registry resolution without performing a mutating op. Read-only.", Triggers: Triggers{Intents: []string{"ops-check"}}},
			},
		},
		{
			Name:        "bobbie",
			Description: "Read-only pre-merge security-audit gate (crew-manifest Layer 2). Reads one PR diff on a clean context window, orchestrates deterministic scanners (gitleaks/semgrep/osv), reasons over output against RULEBOOK.md, and emits review.status clean|blocking. Posts exactly one PR comment per invocation. Never authors code, never merges (NAOMI's gate), never dispatches other agents.",
			Role:        "reviewer",
			TrustLabels: []string{"trusted", "read-only", "lore-writer", "high-stakes"},
			Capabilities: []Capability{{
				ID:          "security-audit-pr",
				Name:        "Security Audit PR",
				Description: "Orchestrates gitleaks/semgrep/osv-scanner over the PR diff, reasons over findings against RULEBOOK.md, emits structured findings (rule_id, severity, line citation, exposure scenario). Posts one PR comment when post_comment is true. review.status is 'blocking' if any finding is blocking.",
				Triggers:    Triggers{Intents: []string{"security-review", "code-review"}},
			}},
		},
		{
			Name:        "codex",
			Description: "Thin router to OpenAI's Codex CLI (GPT-5.x family) backed by a local ChatGPT Plus subscription. Selects model tier (flagship / mini / spark) based on task shape and delegates execution. Surfaces errors verbatim; never falls back silently to a Claude model.",
			Role:        "researcher",
			TrustLabels: []string{"external-model", "read-only"},
			Capabilities: []Capability{{
				ID:          "delegate-to-codex",
				Name:        "Delegate to Codex CLI",
				Description: "Translates an incoming task into a codex exec invocation, selects the appropriate model tier, runs it, and returns Codex's output verbatim. Flagship for design/review/ambiguity; mini for mechanical/triage; spark for tight code loops with concrete specs.",
				Triggers:    Triggers{Intents: []string{"second-opinion", "codex-review", "delegate-to-codex", "gpt-reasoning"}},
			}},
		},
		{
			Name:        "drummer",
			Description: "Read-only platform observer (crew-manifest Layer 2). Runs one named check_set per invocation (e.g. platform_health, dispatch_backlog) against the platform or a target project, hydrates from LORE engrams/retros, and emits a structured findings report (per-check green|amber|red|unknown + diagnostic_context + drift_detected + recommended_bounce_target). Never diagnoses (MILLER's job), never fixes, never mutates.",
			Role:        "diagnostician",
			TrustLabels: []string{"trusted", "read-only", "lore-writer"},
			Capabilities: []Capability{{
				ID:          "run-check",
				Name:        "Run Check Set",
				Description: "Executes a named check_set against the platform or target project. Each check produces a status (green|amber|red|unknown), diagnostic_context, and a recommended_bounce_target when non-green. Emits a findings report and files drift tasks in LORE when drift_detected is true.",
				Triggers:    Triggers{Intents: []string{"probe", "wiring-test"}},
			}},
		},
		{
			Name:        "gemini-researcher",
			Description: "Gemini CLI research agent running on claude-main. Handles tasks requiring large context (1M+ tokens), real-time web research with Google Search grounding, reading entire large codebases in one pass, or parallel research passes where fresh web knowledge matters.",
			Role:        "researcher",
			TrustLabels: []string{"external-model", "read-only"},
			Capabilities: []Capability{{
				ID:          "research-large-context",
				Name:        "Large-Context Research",
				Description: "Invokes Gemini CLI (gemini-2.5-flash primary, pro for deep reasoning) for research tasks that would exceed Claude's context window or require real-time Google Search grounding. Each invocation is fully self-contained — no conversation history access.",
				Triggers:    Triggers{Intents: []string{"web-search"}},
			}},
		},
		{
			Name:        "holden",
			Description: "Universal project lead parameterized at invocation by cwd + .crew/holden.yaml. Reads the config at session start to specialize: project name, responsibilities, status line, failure modes, gates, and dispatch bounds. With no holden.yaml, falls back to a sane vanilla default (read-only, generic status line, standard loop). agentic-director = HOLDEN at program scope. Use when Andy asks project-internal questions — task triage, task management, or a dispatch scoped to the project's working tree.",
			TrustLabels: []string{"trusted", "lore-writer"},
			Capabilities: []Capability{{
				ID:          "project-lead",
				Name:        "Project Lead",
				Description: "Handles project-internal questions: task triage, status checks, dispatch of scoped build work, and project-level routing. Reads .crew/holden.yaml to specialize per project. Falls back to a sane vanilla default when no config is present.",
				Triggers:    Triggers{Intents: []string{"deep-analysis", "architecture-review", "tradeoff-evaluation"}},
			}},
		},
		{
			Name:        "miller",
			Description: "Read-only troubleshooting detective (crew-manifest Layer 2). Receives one failure artifact, hydrates from LORE engrams/retros and on-disk state, applies the troubleshooting-methodology skill (Tier 0 triage to Tier 2 structured diagnosis), and emits a root-cause diagnosis with cynefin_domain, loop_class, evidence citations, and a bounce_target naming who should act. Never authors code, never mutates services, never self-dispatches.",
			Role:        "diagnostician",
			TrustLabels: []string{"trusted", "read-only", "lore-writer"},
			Capabilities: []Capability{{
				ID:          "diagnose-failure",
				Name:        "Diagnose Failure",
				Description: "Receives a failure_artifact (log path, error string, agent_result, or service endpoint). Hydrates from LORE institutional memory first, then on-disk state. Produces a structured diagnosis: cynefin_domain, loop_class, evidence, root_cause, and a bounce_target naming the agent who should act next.",
				Triggers:    Triggers{Intents: []string{"deep-analysis", "second-opinion"}},
			}},
		},
		{
			Name:        "naomi",
			Description: "Release gate (crew-manifest Layer 2). The only identity authorized to merge PRs or push to main. Runs pre-merge checks (project pre_checks + envelope-supplied additional_checks + CI status); merges on green, refuses on red. One action per invocation: merge_pr, push_main, tag_release, or cut_release. Never opens PRs, never writes feature code, never debugs. Bounces failures to AMoS (code) or MILLER (unclear).",
			Role:        "merger",
			TrustLabels: []string{"trusted", "autonomous", "lore-writer", "merge-authority", "high-stakes"},
			Capabilities: []Capability{{
				ID:          "merge-pr",
				Name:        "Merge Pull Request",
				Description: "Runs all pre-merge gates for the target repo. If every check passes and PEACHES review.status is not blocking, merges the PR via Forgejo API as the naomi identity. Writes a structured failure result and creates a LORE task for the responsible agent on any gate failure.",
				Triggers:    Triggers{Intents: []string{"merge-pr", "release"}},
			}},
		},
		{
			Name: "ollama",
			// Description is a hand-synced copy of the live clagentic-config
			// entry with the deployment-specific internal hostname replaced by
			// a generic descriptor (BOBBIE bobbie.bleed.1, PR #18 comment
			// 5107681580): this file ships in the production binary and this
			// repo is public, so the real internal FQDN must never appear here
			// even though it carries no credential material. The replacement
			// preserves every token the BM25 matcher scores against (local
			// inference, self-hosted, llama.cpp, Vulkan GPU, Ollama,
			// embeddings/RAG, phi4) — only the FQDN is redacted.
			Description: "Lightweight local inference agent backed by a self-hosted llama.cpp server (Vulkan GPU) and Ollama (CPU) on an internal self-hosted host. Best for cheap/offline tasks: classification, summarization, format conversion, embeddings/RAG, and code snippets where API cost matters and quality requirements are modest.",
			Role:        "researcher",
			TrustLabels: []string{"local-model", "read-only"},
			Capabilities: []Capability{{
				ID:          "local-inference",
				Name:        "Local LLM Inference",
				Description: "Routes tasks to the appropriate local model: phi4-mini (3.8B, Vulkan GPU) for general tasks, qwen2.5-coder:3b (CPU) for code tasks, or nomic-embed-text (CPU) for embeddings. Does not fall back to cloud models on failure.",
				Triggers:    Triggers{Intents: []string{"local-inference", "cheap-inference", "embeddings", "offline-inference"}},
			}},
		},
		{
			Name:        "opus",
			Description: "Deep reasoning agent running on Claude Opus. Selected when tasks require careful, thorough thinking: architecture decisions, complex multi-file refactors, debugging systemic issues, technical proposals, security analysis, and significant trade-off evaluation.",
			Role:        "researcher",
			TrustLabels: []string{"trusted", "high-stakes"},
			Capabilities: []Capability{{
				ID:          "deep-reasoning",
				Name:        "Deep Reasoning",
				Description: "Handles tasks that benefit from extended reasoning where Sonnet's depth is insufficient. Returns actionable output after careful consideration of implications, risks, and trade-offs.",
				Triggers:    Triggers{Intents: []string{"architecture-review", "deep-analysis", "security-review", "tradeoff-evaluation"}},
			}},
		},
		{
			Name:        "prax",
			Description: "Read-only crew researcher (crew-manifest Layer 2). Takes one research question on a clean context window and emits structured findings (claims, sources, disagreements, confidence) backed by an enforced methodology: recursive query fan-out, layered authority + topic-tunable recency triage, miner/skeptic/judge verification, citation discipline, explicit fallback envelope. Engram-first by contract. External research routes through gemini CLI (Flash primary, Pro escalation). Never authors code, never browses with raw WebFetch/WebSearch, never dispatches other crew agents.",
			Role:        "researcher",
			TrustLabels: []string{"trusted", "read-only", "lore-writer", "external-source"},
			Capabilities: []Capability{{
				ID:          "research-question",
				Name:        "Research Question",
				Description: "Accepts one research question. Fans out across LORE engrams, on-disk sources, and external engines (gemini Flash → Pro → web-researcher fallback). Applies miner/skeptic/judge verification. Emits structured findings: claims[], sources[], disagreements[], confidence, engine_used.",
				Triggers:    Triggers{Intents: []string{"research", "web-research", "fact-lookup", "doc-lookup", "large-context-analysis", "codebase-survey", "community-sentiment"}},
			}},
		},
		{
			Name:        "reddit-researcher",
			Description: "Research agent that investigates what Reddit communities think about a topic. Searches multiple subreddits, reads threads, and synthesizes community sentiment, consensus, and debate into a structured report.",
			Role:        "researcher",
			TrustLabels: []string{"external-source", "read-only"},
			Capabilities: []Capability{{
				ID:          "reddit-research",
				Name:        "Reddit Community Research",
				Description: "Runs structured Reddit research: broad search across subreddits, reads top threads with comments, identifies consensus vs. debate, checks recency, and returns a synthesized findings report.",
				Triggers:    Triggers{Intents: []string{"community-sentiment", "reddit-research", "user-opinion-research"}},
			}},
		},
		{
			Name:        "roci",
			Description: "Agent builder (crew-manifest Layer 2). Rapidly Orchestrates Configured Identities — scaffolds new agent definitions, schemas, and identity entries when authorized by an operator. Writes to agent-definition paths only (agents/** scope). Never self-dispatches and never modifies platform code. Lifecycle: planned (no agent file yet).",
			Role:        "builder",
			TrustLabels: []string{"trusted", "autonomous", "lore-writer"},
			Capabilities: []Capability{{
				ID:          "scaffold-agent",
				Name:        "Scaffold Agent Definition",
				Description: "Creates or updates agent definition files (plugins/crew-manifest/agents/<name>.md, input-schema.json, output-schema.json) for a named crew member. Validates naming conventions, frontmatter required fields, and I/O schema rules. Produces a PR on a feature branch for NAOMI to merge.",
				Triggers:    Triggers{Intents: []string{"wiring-test", "probe"}},
			}},
		},
		{
			Name:        "test-agent",
			Description: "Throwaway probe agent for verifying named-agents-as-projects wiring. Not for production use. When greeted, introduces itself and stops.",
			Role:        "tester",
			TrustLabels: []string{"test-only"},
			Capabilities: []Capability{{
				ID:          "wiring-probe",
				Name:        "Wiring Verification Probe",
				Description: "Responds to greetings with a fixed self-identification string to confirm that the Clay named-agents routing is wired correctly.",
				Triggers:    Triggers{Intents: []string{"probe", "wiring-test"}},
			}},
		},
		{
			Name:        "tiamut",
			Description: "Autonomous intelligence harvester. Inspects repos, ingests findings into Lore, and creates scoped tasks from high-signal discoveries. Runs in two user-gated stages: inspect (automatic, scores and classifies) and ingest (user-approved, creates tasks and tomes).",
			Role:        "researcher",
			TrustLabels: []string{"autonomous", "lore-writer"},
			Capabilities: []Capability{
				{ID: "inspect-repo", Name: "Inspect Repository", Description: "Stage 1: triggered by a pipeline inspect_repo advisory. Scores the repo on a 1-10 harvest scale. Writes a comparison tome and creates an ingest_candidate advisory if score >= 7. Writes a read-only tome for scores 4-6. Silent dismissal for score <= 3.", Triggers: Triggers{Intents: []string{"inspect-repo", "harvest-intelligence"}}},
				{ID: "ingest-repo", Name: "Ingest Repository Findings", Description: "Stage 2: triggered by user approval of an ingest_candidate advisory. Creates implementation tomes and Lore tasks from the approved report. Does not run autonomously — requires explicit user gate.", Triggers: Triggers{Intents: []string{"ingest-candidate"}}},
			},
		},
		{
			Name:        "web-researcher",
			Description: "Focused web research agent running on Claude Haiku. Handles simple, self-contained web lookups when Gemini is unavailable. Best for reading a single URL, looking up a specific fact or API detail, checking documentation, or verifying version numbers. Fallback to gemini-researcher for large-context or multi-source synthesis tasks.",
			Role:        "researcher",
			TrustLabels: []string{"external-source", "read-only"},
			Capabilities: []Capability{{
				ID:          "web-lookup",
				Name:        "Web Lookup",
				Description: "Runs structured web research: 2-3 search queries, source triage, extraction and cross-referencing, then synthesized output with citations. Falls back gracefully from single-URL reads to multi-source research using WebSearch and WebFetch.",
				Triggers:    Triggers{Intents: []string{"web-research", "url-fetch", "fact-lookup", "doc-lookup"}},
			}},
		},
	}

	out := make(map[string]Agent, len(agents))
	for _, a := range agents {
		out[a.Name] = a
	}
	return out
}
