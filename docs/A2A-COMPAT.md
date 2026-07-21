# A2A AgentCard Compatibility

## Endpoint

```
GET /.well-known/agent-card.json/{name}
```

Returns an agent card for the named agent, conformant with the A2A
(Agent-to-Agent) protocol GA v1.0 `AgentCard` type (Linux Foundation,
GA since 2026-03-12; wire shape verified against a2a-sdk 1.1.1,
protobuf schema `lf.a2a.v1`).

> **Schema migration note:** prior to this change, this endpoint served a
> pre-GA placeholder shape (hardcoded `"schemaVersion": "1.0"`, native
> clagentic `capabilities` array reused as-is, no `skills`/`securitySchemes`/
> input-output modes). That shape is no longer served. Any consumer written
> against the old response must be updated — this is a breaking wire-format
> change on this endpoint.

## Mapping

The response is a JSON object with these fields:

| A2A GA field | Source |
|-----------|--------|
| `name` | `identity.name` |
| `description` | `identity.description` |
| `version` | `identity.version` |
| `supportedInterfaces` | Derived from the request: one `AgentInterface` entry pointing at `GET /v1/agents/{name}` on this host, `protocolBinding: "HTTP+JSON"`, `protocolVersion: "1.0"` |
| `capabilities` | `AgentCapabilities` feature-flag object (streaming/pushNotifications/extensions/extendedAgentCard) — currently always `{}`; the registry does not yet declare these flags |
| `defaultInputModes` / `defaultOutputModes` | Hardcoded `["application/json"]` |
| `skills` | Registry `capabilities` array, mapped to `AgentSkill` (`id`, `name`, `description`, `tags` from `triggers.intents`) |
| `securitySchemes` / `securityRequirements` | Present only when the directory has an auth token configured (`--auth-token` / `CLAGENTIC_DIRECTORY_TOKEN`); declares an HTTP bearer scheme |

GA has **no top-level `schemaVersion` field** — it was dropped in favor of
the per-interface `protocolVersion` inside `supportedInterfaces`. Do not
reintroduce a root `schemaVersion` field on this card.

## Example

Request:

```bash
curl -s localhost:8444/.well-known/agent-card.json/reviewer | jq
```

Response (auth disabled):

```json
{
  "name": "reviewer",
  "description": "Performs structured code review on pull requests and commits.",
  "version": "1.0.0",
  "supportedInterfaces": [
    {
      "url": "http://localhost:8444/v1/agents/reviewer",
      "protocolBinding": "HTTP+JSON",
      "protocolVersion": "1.0"
    }
  ],
  "capabilities": {},
  "defaultInputModes": ["application/json"],
  "defaultOutputModes": ["application/json"],
  "skills": [
    {
      "id": "review-pr",
      "name": "Review Pull Request",
      "description": "Reads a PR or commit diff and emits structured findings against a rulebook.",
      "tags": ["code-review", "review-pr", "review-commit"]
    }
  ]
}
```

When an auth token is configured, the card additionally includes:

```json
{
  "securitySchemes": {
    "bearerAuth": {
      "httpAuthSecurityScheme": {
        "description": "Bearer token required for all routes except /healthz.",
        "scheme": "bearer"
      }
    }
  },
  "securityRequirements": [
    { "schemes": { "bearerAuth": {} } }
  ]
}
```

## Notes on A2A GA conformance

- The GA spec anticipates a single card per host; this service parameterizes
  by agent name in the path, enabling multiple agents per host. This remains
  a deliberate deviation from the single-card convention (not a conformance
  gap in the card *shape* itself).
- `securitySchemes`/`securityRequirements` are **declaration-only**. Declaring
  a scheme on the card does not add or change runtime enforcement — that is
  server middleware, and adding bearer-token enforcement is out of scope for
  this endpoint's card-shape conformance work. The directory's existing
  `requireAuth` bearer-token gate (unchanged by this endpoint) is the actual
  enforcement point when a token is configured.
- `capabilities` (the GA `AgentCapabilities` feature-flag object: streaming,
  pushNotifications, extensions, extendedAgentCard) is currently always an
  empty object — the registry schema does not yet capture these flags. This
  is accurate (no false claims of streaming/push support), not a gap.
- The `.json` suffix in the path is optional — the handler strips it if
  present.
