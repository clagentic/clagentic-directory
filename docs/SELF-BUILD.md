# Self-Build Mechanisms

clagentic-directory supports three opt-in mechanisms that observe running agent activity
and propose registry updates. All three are **disabled by default**. None of them write
to the live registry directly — all output lands in `proposed_changes/` and requires an
operator PR gate before any capability change takes effect.

## Security boundary

```
  live registry/   <--  operator PR gate  <--  proposed_changes/  <-- mechanisms
```

- Mechanisms write only to `proposed_changes/<agent>.<timestamp>.yaml`.
- The live registry (YAML files under `--registry-dir` or the git source) is never touched.
- An operator reviews and merges the proposed change. Agents do not silently acquire capabilities.

This boundary is enforced in code: `WriteProposedChange` in `internal/selfbuild/writer.go`
always writes under a `proposed_changes/` subdirectory of `--self-build-base-dir`, and the
HTTP handlers have no write path into the registry.

---

## Common flag

All three mechanisms share one prerequisite flag:

| Flag | Required | Description |
|---|---|---|
| `--self-build-base-dir` | Yes (if any mechanism enabled) | Base directory where `proposed_changes/` is written |

---

## Mechanism 1 — MCP discovery

**Flag:** `--self-build-mcp-discovery=true`

Connects to a target agent's MCP server, calls `tools/list`, and maps each tool to a
draft `Capability` entry with per-field confidence labels.

### How it works

1. Issue a JSON-RPC 2.0 `tools/list` call to the agent's MCP endpoint.
2. For each tool: extract `name` (confidence: `extracted`), `description` (confidence: `extracted`).
3. Infer `intents` by splitting the tool name on `_`/`-` separators (confidence: `inferred`).
4. Infer `format` from description keywords: `json`, `markdown`, `yaml`, `text` (confidence: `inferred`).
5. Write `proposed_changes/<agent>.<timestamp>.yaml`.

### Ad-hoc invocation

```
clagentic-directory inspect --agent <agent-name> --mcp-url <mcp-server-url>
```

This is the primary interface for MCP discovery. See the
[CLI usage: inspect subcommand](#cli-usage-inspect-subcommand) section below for full flag reference.
The `inspect` subcommand runs regardless of whether `--self-build-mcp-discovery` is set.

### Example output

```yaml
schema_version: 1
source: mcp-discovery
agent_name: researcher
capabilities:
  - id:
      value: search-web
      confidence: extracted
    description:
      value: Search the web and return JSON results.
      confidence: extracted
    intents:
      values: [search-web, search, web]
      confidence: inferred
    format:
      value: json
      confidence: inferred
```

---

## Mechanism 2 — Engram watch

**Flag:** `--self-build-engram-watch-url <lore-api-url>`

Polls the LORE engram/codex stream for `file-diff` events on `SKILL.md` and `AGENT.md`
files. When a diff is detected, it does a best-effort parse of added lines for trigger
and capability keywords and writes a `proposed_changes/` entry.

### Flags

| Flag | Default | Description |
|---|---|---|
| `--self-build-engram-watch-url` | (disabled) | LORE API base URL, e.g. `http://localhost:9100` |
| `--self-build-engram-watch-interval` | `60s` | Poll interval |
| `--self-build-engram-watch-window` | `5m` | Rate-limit dedup window per agent |

### How it works

1. Poll `GET <lore-url>/v1/engram/events?kinds=file-diff` on the configured interval.
2. Filter for events whose `file_path` ends with `SKILL.md` or `AGENT.md`.
3. Resolve agent name from the event's `agent` field, or extract from path segments.
4. Rate-limit: skip if a proposed change for this agent was written within the dedup window.
5. Parse `+`-prefixed lines in the diff for trigger/capability/returns keywords.
6. Write `proposed_changes/<agent>.<timestamp>.yaml`.

### Why rate-limit?

Agent definition files can receive multiple rapid edits during a session. Without
deduplication, each edit would produce a separate proposal file. The rate window
coalesces proposals within a short burst into one.

---

## Mechanism 3 — Usage-driven inference

**Flags:** `--self-build-usage-relay-url <url>` and `--self-build-usage-window <duration>`

Pulls conversation events from the clagentic-relay event store over a rolling time window,
aggregates `(actor, next_actor, conversation_kind)` tuples, and compares empirical
sequencing against the registered `after_agents` in the live registry. Emits a
`drift_report` when an unregistered sequencing pattern is observed.

### Flags

| Flag | Default | Description |
|---|---|---|
| `--self-build-usage-relay-url` | (disabled) | clagentic-relay event store base URL |
| `--self-build-usage-window` | `1h` | Rolling aggregation window |

### How it works

1. On each tick (interval = window duration), fetch `GET <relay-url>/v1/events?since=<window-start>`.
2. Aggregate `(actor, next_actor, conversation_kind)` counts from events where both fields are non-empty.
3. For each observed tuple: check whether `next_actor` appears as an `after_agent` for `actor`
   in the registry via `FindBySequencing(actor)`.
4. On mismatch: add to a `DriftReport`.
5. Group drift reports by actor; write one `proposed_changes/<actor>.<timestamp>.yaml` per actor
   with unregistered patterns.

### Example output

```yaml
schema_version: 1
source: usage-inference
agent_name: peaches
drift_reports:
  - actor: peaches
    next_actor: naomi
    conversation_kind: code-review
    observed_count: 47
    registered_after_seq: false
notes:
  - "Drift detected over rolling window: 1h0m0s"
  - "Observed 1 unregistered sequencing pattern(s) for actor \"peaches\""
```

---

## Proposed change schema

All three mechanisms write files conforming to this structure:

```yaml
schema_version: 1            # always 1
generated_at: <RFC3339>
source: <mcp-discovery|engram-watch|usage-inference>
agent_name: <slug>
capabilities:                # present for mcp-discovery and engram-watch
  - id:
      value: <string>
      confidence: extracted|inferred
    name:
      value: <string>
      confidence: extracted|inferred
    description:
      value: <string>
      confidence: extracted|inferred
    intents:
      values: [<string>, ...]
      confidence: extracted|inferred
    format:
      value: <string>
      confidence: extracted|inferred
drift_reports:               # present for usage-inference
  - actor: <string>
    next_actor: <string>
    conversation_kind: <string>
    observed_count: <int>
    registered_after_seq: false
notes:
  - <human-readable context>
```

`confidence: extracted` — value came directly from source data (MCP tool name, diff content).  
`confidence: inferred` — value was derived heuristically and should be reviewed before merging.

---

## Operator workflow

1. One or more mechanisms write files to `proposed_changes/`.
2. Operator reviews each file:
   - Validate `inferred` fields; correct them if wrong.
   - Decide whether the proposed capability or sequencing pattern should be registered.
3. If accepted: copy/merge the capability data into the relevant agent YAML in the registry.
4. Open a PR (or push directly if operating with `source=file`).
5. The service hot-reloads the updated registry (FileStore: fsnotify; GitStore: next poll).

The `proposed_changes/` directory is intentionally separate from the registry directory.
It is safe to delete or archive old proposals at any time.

---

## CLI usage: inspect subcommand

The `inspect` subcommand performs a one-shot MCP introspection without
starting the service. Useful for operator-on-demand discovery.

```
clagentic-directory inspect \
  --agent <name> \
  --mcp-url <http://localhost:PORT/mcp> \
  [--config <path>] \
  [--output-dir <path>]
```

Flags:
- `--agent` (required): agent name to record in the proposed entry
- `--mcp-url` (required): MCP server endpoint to introspect
- `--config`: path to config file (default: `~/.config/clagentic/directory.yaml`)
- `--output-dir`: root for proposed_changes/ output (default: from config, or `./proposed_changes`)

On success, prints the absolute path of the written YAML file to stdout
and exits 0. On failure, prints to stderr and exits non-zero.

The inspect subcommand operates independently of the `--self-build-mcp-discovery`
daemon flag — it always runs regardless of service configuration.
