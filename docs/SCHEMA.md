# Agent Entry Schema Reference

Two schema versions are supported. New entries must use `schema_version: 2`.
Schema v1 is accepted with a deprecation warning at load time.

- `schemas/agent-entry.v1.yaml` — v1, lenient (no vocabulary enum)
- `schemas/agent-entry.v2.yaml` — v2, strict (closed vocabulary enum)

See [VOCABULARY.md](VOCABULARY.md) for the canonical list of valid intents,
conversation_kinds, trust_labels, and return formats.

---

## Version differences (v1 → v2)

| Aspect | v1 | v2 |
|--------|----|----|
| `schema_version` | `const: 1` | `const: 2` |
| `capabilities[].triggers.intents` | free-form strings | enum (see VOCABULARY.md) |
| `capabilities[].triggers.conversation_kinds` | free-form strings | enum (see VOCABULARY.md) |
| `trust_labels` | free-form strings | enum (see VOCABULARY.md) |
| `capabilities[].returns` | optional | **required** |
| `capabilities[].returns.format` | free-form string | enum (see VOCABULARY.md) |
| `additionalProperties` | `true` (lenient) | `false` (strict) |
| Loader behavior | loads silently | loads and validates vocabulary |

---

## Top-level fields

| Field | Required | Type | Description |
|-------|----------|------|-------------|
| `schema_version` | yes | integer (const: 2) | Schema version. Must be `2` for new entries. |
| `identity` | yes | object | Agent identity block. |
| `capabilities` | yes | array | List of capability objects. |
| `trust_labels` | no | array of enum strings | Deployment trust tags. Values must be from VOCABULARY.md. |

---

## `identity`

| Field | Required | Type | Description |
|-------|----------|------|-------------|
| `name` | yes | string | Lowercase slug. Pattern: `^[a-z][a-z0-9-]*$`. Must be unique across all files in the registry. |
| `version` | yes | string | Semver string. Pattern: `^[0-9]+\.[0-9]+\.[0-9]+$`. |
| `description` | no | string | Human-readable description of the agent. |

---

## `capabilities[]`

Each entry in the array describes one thing the agent can do.

| Field | Required | Type | Description |
|-------|----------|------|-------------|
| `id` | yes | string | Stable capability identifier (e.g. `review-pr`). |
| `name` | yes | string | Human-readable capability name. |
| `description` | yes | string | What the capability does. Multi-line YAML literal blocks are fine. |
| `triggers` | no | object | When this capability is invoked. See below. |
| `returns` | **yes (v2)** | object | What the capability returns. Required in v2. |

---

## `capabilities[].triggers`

| Field | Type | Description |
|-------|------|-------------|
| `intents` | array of enum strings | Intent labels that invoke this capability. Used by `GET /v1/find?intent=`. Must be from vocabulary. |
| `conversation_kinds` | array of enum strings | Conversation kind labels. Used by `GET /v1/find?conversation_kind=`. Must be from vocabulary. |
| `after_roles` | array of strings | This capability runs after agents fulfilling these roles (free-form). |
| `after_agents` | array of strings | This capability runs after the named agents. Used by `GET /v1/find?after_agent=`. |

---

## `capabilities[].returns`

Required in v2.

| Field | Type | Description |
|-------|------|-------------|
| `verdict_field` | string | The key in the agent's output envelope that carries the primary result. |
| `format` | enum string | Output format hint. Must be from vocabulary (`json`, `structured`, `structured-markdown`, `url`, `text`). |

---

## `trust_labels`

Enum-constrained in v2. Values must be from [VOCABULARY.md](VOCABULARY.md).

---

## Complete example (v2)

```yaml
schema_version: 2
identity:
  name: reviewer
  version: 1.0.0
  description: Performs structured code review on pull requests and commits.
capabilities:
  - id: review-pr
    name: Review Pull Request
    description: |
      Reads a PR or commit diff and emits structured findings against a rulebook.
      Posts at most one review comment per invocation summarizing findings.
    triggers:
      intents:
        - code-review
        - review-pr
      conversation_kinds:
        - review
      after_roles: []
      after_agents: []
    returns:
      verdict_field: review_result
      format: structured-markdown
trust_labels:
  - read-only
```
