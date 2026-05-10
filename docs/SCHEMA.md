# Agent Entry Schema Reference

Schema: `schemas/agent-entry.v1.yaml` (JSON Schema draft 2020-12)

## Top-level fields

| Field | Required | Type | Description |
|-------|----------|------|-------------|
| `schema_version` | yes | integer (const: 1) | Schema version. Must be `1`. |
| `identity` | yes | object | Agent identity block. |
| `capabilities` | yes | array | List of capability objects. |
| `trust_labels` | no | array of strings | Deployment-specific trust tags. |

Unknown top-level keys are tolerated (`additionalProperties: true`) for forward compatibility.

---

## `identity`

| Field | Required | Type | Description |
|-------|----------|------|-------------|
| `name` | yes | string | Lowercase slug. Pattern: `^[a-z][a-z0-9-]*$`. Must be unique across all files in the registry. |
| `version` | yes | string | Semver string. Pattern: `^[0-9]+\.[0-9]+\.[0-9]+$`. |
| `description` | no | string | Human-readable description of the agent. |

Example:

```yaml
identity:
  name: reviewer
  version: 1.0.0
  description: Performs structured code review on pull requests and commits.
```

---

## `capabilities[]`

Each entry in the array describes one thing the agent can do.

| Field | Required | Type | Description |
|-------|----------|------|-------------|
| `id` | yes | string | Stable capability identifier (e.g. `review-pr`). |
| `name` | yes | string | Human-readable capability name. |
| `description` | yes | string | What the capability does. Multi-line YAML literal blocks are fine. |
| `triggers` | no | object | When this capability is invoked. See below. |
| `returns` | no | object | What the capability returns. See below. |

---

## `capabilities[].triggers`

| Field | Type | Description |
|-------|------|-------------|
| `intents` | array of strings | Intent labels that invoke this capability. Used by `GET /v1/find?intent=`. |
| `conversation_kinds` | array of strings | Conversation kind labels. Used by `GET /v1/find?conversation_kind=`. |
| `after_roles` | array of strings | This capability runs after agents fulfilling these roles. |
| `after_agents` | array of strings | This capability runs after the named agents. Used by `GET /v1/find?after_agent=`. |

Example:

```yaml
triggers:
  intents:
    - code-review
    - review-pr
  conversation_kinds:
    - review
  after_roles: []
  after_agents: []
```

---

## `capabilities[].returns`

| Field | Type | Description |
|-------|------|-------------|
| `verdict_field` | string | The key in the agent's output that carries the primary result. |
| `format` | string | Output format hint (e.g. `json`, `structured-markdown`). |

Example:

```yaml
returns:
  verdict_field: review_result
  format: structured-markdown
```

---

## `trust_labels`

Free-form string tags used by deployment policies to gate access or routing. Values are vocabulary-defined; see `schemas/vocabulary.v1.yaml`.

Example:

```yaml
trust_labels:
  - trusted
  - read-only
```

---

## Complete example

```yaml
schema_version: 1
identity:
  name: reviewer
  version: 1.0.0
  description: Performs structured code review on pull requests and commits.
capabilities:
  - id: review-pr
    name: Review Pull Request
    description: |
      Reads a PR or commit diff and emits structured findings against a rulebook.
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
  - trusted
  - read-only
```
