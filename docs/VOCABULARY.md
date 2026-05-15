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
