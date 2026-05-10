# A2A AgentCard Compatibility

## Endpoint

```
GET /.well-known/agent-card.json/{name}
```

Returns an agent card for the named agent in a format compatible with the Google A2A (Agent-to-Agent) protocol's `AgentCard` type.

## Mapping

The response is a JSON object with these fields:

| A2A field | Source |
|-----------|--------|
| `schemaVersion` | Hardcoded `"1.0"` |
| `name` | `identity.name` |
| `version` | `identity.version` |
| `description` | `identity.description` |
| `capabilities` | Full capabilities array from the registry entry |

## Example

Request:

```bash
curl -s localhost:8444/.well-known/agent-card.json/reviewer | jq
```

Response:

```json
{
  "schemaVersion": "1.0",
  "name": "reviewer",
  "version": "1.0.0",
  "description": "Performs structured code review on pull requests and commits.",
  "capabilities": [
    {
      "id": "review-pr",
      "name": "Review Pull Request",
      "description": "Reads a PR or commit diff and emits structured findings against a rulebook.\nPosts at most one review comment per invocation summarizing findings.\n",
      "triggers": {
        "intents": ["code-review", "review-pr", "review-commit"],
        "conversation_kinds": ["review"],
        "after_roles": [],
        "after_agents": []
      },
      "returns": {
        "verdict_field": "review_result",
        "format": "structured-markdown"
      }
    }
  ]
}
```

## Notes on A2A compliance

The Google A2A spec defines `AgentCard` as a discovery artifact served at `/.well-known/agent-card.json`. This endpoint follows that convention with these caveats:

- The spec anticipates a single card per host; this service parameterizes by agent name in the path, enabling multiple agents per host.
- The `capabilities` array in the A2A spec uses a different schema than ours. This endpoint returns the native clagentic-directory capability format, not the A2A `AgentCapability` type. Consumers that require strict A2A schema conformance should adapt accordingly.
- Authentication, `inputModes`, and `outputModes` from the A2A spec are not present in this response. These fields may be added in a future schema version.

The `.json` suffix in the path is optional — the handler strips it if present.
