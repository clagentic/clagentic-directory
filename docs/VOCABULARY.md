# Agent Entry Vocabulary

Canonical vocabulary for `schema_version: 2` agent entries in the clagentic-directory registry.

All values used in `capabilities[].triggers.intents`, `capabilities[].triggers.conversation_kinds`,
`trust_labels`, and `capabilities[].returns.format` must come from this list.

To add a new vocabulary item:
1. Add it here with semantics.
2. Add it to `schemas/vocabulary.v1.yaml` (`x-canonical-values` list + `definitions` block).
3. Add it to the enum in `schemas/agent-entry.v2.yaml`.
4. Add it to the appropriate `v2Valid*` map in `internal/store/filestore.go`.
5. Use it in agent YAML files.

Vocabulary grows monotonically. Values are never removed from the enum once shipped —
agents that used a removed value would silently break at `FindByCapability()` call sites.

---

## Intents

An intent is a signal that routes work to an agent. It answers: "what action or event happened?"
Intents are matched by `FindByCapability(intent...)` to find agents that handle a given intent.

| Intent | Semantics | When to use |
|--------|-----------|-------------|
| `code_work_requested` | A code change task has been dispatched to the fleet. | Primary trigger for builders (AMoS, ROCI). Use when work is fully scoped and ready to be implemented. |
| `code-generation` | A code generation task from a specification has been requested. | Use when the task is to produce new code from a spec, not to modify existing code in place. |
| `implement-task` | A described task must be implemented end-to-end. | General-purpose builder trigger; broader than `code-generation`, covers task completion including tests and PRs. |
| `pr_opened` | A pull request has been opened by an upstream agent or operator. | Triggers reviewers (PEACHES). Fires after a builder opens a PR. |
| `code_ready_for_review` | Code has been marked ready for structured review. | Explicit review request, as opposed to the implicit `pr_opened` trigger. |
| `code-review` | A general code review has been requested. | Use when the review scope is not limited to a single PR. |
| `review-pr` | Review of a specific pull request has been requested. | Scoped to a single PR. Use with a PR URL or ID in the dispatch context. |
| `review-commit` | Review of a specific commit has been requested. | Scoped to a single commit SHA. |
| `merge_requested` | An operator or agent has requested that an approved PR be merged. | Primary trigger for merge-gate agents (NAOMI). Fires after review passes. |
| `merge-pr` | Merge of a specific pull request has been requested. | Specific PR merge, as opposed to a general merge-gate trigger. |
| `release` | A release action has been requested. | Triggers agents authorized to cut a release (e.g. tag + publish). |
| `tag-release` | A tag-and-release action specifically has been requested. | More specific than `release`; implies a git tag is part of the action. |
| `diagnostic_requested` | A diagnostic investigation of a failure or anomaly has been requested. | Primary trigger for diagnosis agents (MILLER). Use when root cause is suspected but not confirmed. |
| `root_cause_unknown` | A failure has been observed and root cause is unknown. | Escalation trigger for diagnosis agents. Implies the caller has exhausted first-pass investigation. |
| `escalation_diagnosis` | A diagnostic investigation has been escalated from another agent. | Use when a diagnosis is passed to a specialist (e.g. MILLER) after a general agent failed to resolve. |
| `deploy_requested` | A deployment action has been requested. | Primary trigger for ops agents (DRUMMER). |
| `runbook_run` | A specific runbook execution has been requested. | Use when a named runbook is identified and should be executed. |
| `ops_check` | An operational health check has been requested. | Non-destructive check; returns status of a system or service. |
| `research_requested` | A research task has been requested. | Primary trigger for research agents (PRAX). Use when a structured research question needs answering. |
| `survey_requested` | A survey of a topic has been requested. | Broader than `research_requested`; implies multiple sources and a comparison output. |
| `research` | General research capability trigger. | Use for agents that handle ad-hoc research queries not fitting narrower categories. |
| `investigate` | Investigation of a topic or system has been requested. | Similar to `research` but with a more detective/forensic connotation. |
| `find-information` | Agent is requested to find specific information. | Narrower than `research`; implies a factual lookup rather than synthesis. |
| `scaffold_requested` | A project or code scaffolding task has been requested. | Primary trigger for scaffolding agents (ROCI). |
| `new_project_setup` | A new project setup task has been requested. | More specific than `scaffold_requested`; implies initial repo and config initialization. |
| `escalation` | An issue has been escalated for routing or decision. | Routes to the escalation surface (agentic-director). Use when a blocking issue needs human or director judgment. |
| `portfolio_question` | A question about the agent portfolio or dispatch routing. | Triggers the director for questions like "which agent should handle X?" |
| `dispatch_routing` | A routing or dispatch decision is needed. | The director is requested to decide how to route a task or escalation. |
| `web-research` | Fetch and synthesize information from web pages or search results. | Use for agents that query external web sources and synthesize results. |
| `web-search` | Run a web search query and return results. | Narrower than `web-research`; implies a search-engine query rather than direct URL fetch. |
| `url-fetch` | Retrieve and summarize the content of a specific URL. | Use when a known URL must be fetched and summarized. |
| `fact-lookup` | Look up a specific fact, version, or configuration detail. | Narrower than `research`; implies a factual lookup rather than synthesis. |
| `doc-lookup` | Check official documentation for a known library, API, or tool. | Use when the target is official docs for a named dependency. |
| `large-context-analysis` | Analyze content requiring more than 100K tokens of context. | Primary trigger for Gemini-backed agents; implies context-window requirement exceeds Claude's native limit. |
| `codebase-survey` | Read and summarize a large codebase in one pass. | Use for full-repo analysis tasks routed to Gemini or other large-context agents. |
| `community-sentiment` | Research what online communities think about a topic. | Triggers agents that query Reddit, forums, or social platforms. |
| `reddit-research` | Search Reddit subreddits and synthesize community opinion. | Use for scoped Reddit research tasks. |
| `user-opinion-research` | Gather user perspectives from online forums or communities. | Broader than `reddit-research`; covers any forum or community sentiment source. |
| `deep-analysis` | Apply extended reasoning to a complex, ambiguous, or high-stakes problem. | Routes to Opus-backed or other deep-reasoning agents (MILLER, agentic-director). |
| `architecture-review` | Evaluate a system design or architectural decision. | Use for design-level review tasks where trade-offs and long-term implications must be weighed. |
| `security-review` | Analyze code or design for security vulnerabilities or privacy risks. | Routes to agents with security-analysis capability. |
| `tradeoff-evaluation` | Weigh competing options and recommend a course of action. | Use when the task is to compare two or more paths and produce a recommendation. |
| `second-opinion` | Get an independent evaluation of a design, plan, or code change. | Triggers cross-provider or adversarial review agents (Codex, MILLER). |
| `delegate-to-codex` | Route a task to the OpenAI Codex CLI for GPT-family execution. | Primary trigger for the `codex` agent. Use when Codex is explicitly requested. |
| `codex-review` | Request an adversarial or second-opinion code review from Codex. | Use when a Codex-backed review is requested by name. |
| `gpt-reasoning` | Apply GPT-5.x reasoning to a task for cross-provider verification. | Use when GPT-family reasoning is explicitly requested. |
| `local-inference` | Run inference on local models hosted on ollama.akuehner.com. | Primary trigger for the `ollama` agent. |
| `cheap-inference` | Execute a task using low-cost local models when quality requirements are modest. | Use when cost is the driving constraint and quality can be reduced. |
| `offline-inference` | Run inference without calling external APIs. | Use when network isolation or air-gap requirements apply. |
| `embeddings` | Generate vector embeddings for semantic search or RAG pipelines. | Routes to embedding-capable local models (nomic-embed-text via Ollama). |
| `inspect-repo` | Run a Stage 1 Tiamut inspection on a repository advisory. | Primary trigger for the `tiamut` inspect stage. |
| `harvest-intelligence` | Extract high-signal findings from a repository for potential ingestion. | Broader Tiamut trigger; used when the source is not a named advisory. |
| `ingest-candidate` | Run a Stage 2 Tiamut ingest on a user-approved advisory. | Triggers Tiamut's user-gated ingest stage. |
| `probe` | Send a probe to verify agent wiring or routing configuration. | Use for smoke-testing agent routing; triggers test and probe agents. |
| `wiring-test` | Test that named-agent routing resolves correctly end-to-end. | More specific than `probe`; implies the Clay named-agents routing layer is the target. |

---

## Conversation Kinds

A conversation kind describes the type of conversation the agent participates in.
It is a coarse filter: "only engage me in conversations of this kind."
Used by `FindByConversationKind(kind)`.

| Kind | Semantics | Typical agents |
|------|-----------|----------------|
| `build` | A code-change workflow: feature, fix, or chore PR. | AMoS, PEACHES, NAOMI, DRUMMER, ROCI |
| `consult` | A consulting or advisory session: research, diagnosis, Q&A. | PRAX, MILLER |
| `smoke` | A lightweight smoke-test or health-check conversation. | Ops agents |
| `gate` | A gate check: is this safe/ready to proceed? | NAOMI, policy agents |
| `research` | A structured research session. | PRAX, researcher |
| `review` | A code review session. | PEACHES, reviewer |
| `deploy` | A deployment or release conversation. | DRUMMER, NAOMI, merger |
| `planning` | A planning or design session. | PRAX, research agents |
| `directive` | An operator directive: run this, set up that. | DRUMMER, ROCI |
| `escalation` | An escalation session: blocked work, unresolved issue. | agentic-director, MILLER |
| `coordination` | A cross-agent coordination conversation. | agentic-director |
| `advisory` | An advisory session: the agent provides a recommendation or finding on a specific question. | amos, drummer, miller, naomi, roci, tiamut, codex |
| `code-generation` | A session whose primary output is generated code (spec → code). | amos, ollama, codex |
| `classification` | A session where the primary task is classifying input into categories. | ollama (phi4-mini, qwen2.5-coder) |
| `summarization` | A session where the primary task is condensing a large input. | ollama |
| `design` | A design-level session: architecture, API design, or system specification. | opus, agentic-director |
| `test` | A test or wiring-verification session; not for production use. | test-agent |

---

## Trust Labels

A trust label declares what actions an agent is authorized to take.
Trust labels are not enforced by the registry itself — they are read by deployment
policies and relay logic to gate authorization decisions.

| Label | Semantics | Typical agents |
|-------|-----------|----------------|
| `read-only` | Agent may read data but may not write, merge, or publish. | PEACHES, PRAX, MILLER, reviewer, researcher |
| `write-pr` | Agent may open pull requests and push to feature branches. | AMoS, ROCI |
| `write-ops` | Agent may execute runbooks and ops-side write operations (not code branches). | DRUMMER |
| `merge-gate` | Agent is authorized to merge pull requests into main. | NAOMI, merger |
| `publish` | Agent is authorized to publish artifacts or releases (packages, container images). | NAOMI (when used in release mode) |
| `observe` | Agent observes events (e.g. monitors, logging agents) but takes no autonomous action. | Passive monitoring agents |
| `escalation-surface` | Agent is the escalation surface. Humans and agents route unresolved issues here. | agentic-director |
| `dispatch-authority` | Agent has authority to dispatch and route to other agents. | agentic-director |
| `trusted` | Agent operates within established crew-manifest role boundaries. | Most crew agents (amos, naomi, peaches, prax, miller, drummer) |
| `autonomous` | Agent may act without per-action user confirmation within its defined scope. | amos, naomi, tiamut, agentic-director |
| `lore-writer` | Agent is authorized to write tomes and create tasks in the Lore system. | amos, naomi, peaches, prax, miller, drummer, tiamut |
| `high-stakes` | Agent is used for decisions with significant or hard-to-reverse consequences. | naomi, opus, agentic-director |
| `release-authorized` | Agent is authorized to perform release-gate actions (merge, tag, push). | naomi |
| `external-model` | Agent delegates execution to a non-Claude model or API. | codex, gemini-researcher, ollama |
| `external-source` | Agent fetches information from external web sources. | prax, web-researcher, reddit-researcher |
| `local-model` | Agent uses locally-hosted models rather than cloud APIs. | ollama |
| `test-only` | Agent is for verification and testing purposes only; not for production use. | test-agent |

---

## Return Formats

The `format` field in `capabilities[].returns` is a hint to consumers about the
structure of the `verdict_field` value. It does not change how the registry stores the data.

| Format | Semantics |
|--------|-----------|
| `json` | Structured JSON object. Consumers should parse as JSON. |
| `structured` | A structured object (may be YAML, JSON, or typed SDK envelope). Consumers should use the SDK type. |
| `structured-markdown` | Markdown document with structured sections (headings, tables, code blocks). For human-readable reports. |
| `url` | A URL string. Typical for PR URLs returned by builders. |
| `text` | Unstructured plain text. For log output, diagnostic narratives, etc. |
| `agent-result-json` | Structured JSON matching the `agent_result` envelope schema used by crew agents. |
| `verbatim-model-output` | The raw output of a delegated model (Codex, Ollama) returned without transformation. |
| `lore-tome` | A LORE tome record. The verdict_field carries a tome ID or URL. |
| `lore-tasks` | One or more LORE tasks created as output. The verdict_field carries task IDs. |
| `plaintext` | Simple unstructured text, distinct from `text` to signal no markdown or structure is expected. |
