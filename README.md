<p align="center">
  <img src="media/logo/directory-lockup-256.png" alt="clagentic:directory" width="260" />
</p>

<h4 align="center">Agent capability registry. Built for builders.</h4>

<p align="center">
  <a href="https://clagentic.ai"><img src="https://img.shields.io/badge/-clagentic.ai-00CFFF?style=flat&logoColor=white" alt="clagentic.ai" /></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-FSL--1.1--MIT-blue?style=flat" alt="License: FSL-1.1-MIT" /></a>
  <img src="https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go&logoColor=white" alt="Go 1.25+" />
  <a href="https://ko-fi.com/clagentic"><img src="https://img.shields.io/badge/Ko--fi-FF5E5B?style=flat&logo=ko-fi&logoColor=white&label=support" alt="Support on Ko-fi" /></a>
</p>

A self-hosted HTTP registry that stores agent capability declarations and answers routing
queries: which agents handle a given intent, accept a conversation kind, or follow a
specific agent in a sequencing chain. Part of the [clagentic](https://clagentic.ai) suite.

## What it does

- **Capability registry.** Each agent declares its intents, conversation kinds, sequencing
  constraints, trust labels, and return format in a versioned YAML entry.
- **Routing queries.** Three query surfaces: `FindByCapability(intent...)`,
  `FindByConversationKind(kind)`, `FindBySequencing(afterAgent)`.
- **Two registry backends.** `--registry-source file` for local directories (with fsnotify
  hot-reload); `--registry-source git` for a tracked git repo (polled).
- **Strict vocabulary validation.** `schema_version: 2` entries are validated against a
  closed vocabulary. Extend it per-deployment with `--vocabulary-extensions`.
- **Self-build mechanisms.** Three opt-in mechanisms observe running agents and write
  `proposed_changes/` entries for operator review: MCP discovery, source watch,
  usage-driven inference.

No runtime dependencies. Drop-in binary or container. Operators own the registry; agents
never write to it directly.

## Quick start

```bash
# Build
go build -o clagentic-directory ./cmd/clagentic-directory/

# Run against the example registry
./clagentic-directory \
  --registry-source file \
  --registry-dir ./examples/registry/ \
  --listen :8444

# Query
curl -s localhost:8444/v1/agents | jq '.[].name'
curl -s localhost:8444/v1/agents/find?intent=code-review | jq
curl -s localhost:8444/readyz
```

## Install

```bash
go install github.com/clagentic/clagentic-directory/cmd/clagentic-directory@latest
```

Or build from source:

```bash
git clone https://github.com/clagentic/clagentic-directory
cd clagentic-directory
go build -o clagentic-directory ./cmd/clagentic-directory/
```

**Requirements:** Go 1.25+.

## Subcommands

- `inspect` — one-shot MCP introspection for an agent, writes a `proposed_changes/` entry
  without starting the service:

  ```bash
  clagentic-directory inspect --agent <name> --mcp-url <http://localhost:PORT/mcp>
  ```

  See [docs/SELF-BUILD.md](docs/SELF-BUILD.md) for full flag reference and operator workflow.

## Documentation

| Document | Contents |
|---|---|
| [docs/DESIGN.md](docs/DESIGN.md) | Architecture, store interface, data flow |
| [docs/SCHEMA.md](docs/SCHEMA.md) | Agent entry schema reference (v1 and v2) |
| [docs/VOCABULARY.md](docs/VOCABULARY.md) | Canonical vocabulary for v2 entries |
| [docs/DEPLOY.md](docs/DEPLOY.md) | Operator deployment guide |
| [docs/SELF-BUILD.md](docs/SELF-BUILD.md) | Self-build mechanisms (MCP, source watch, usage inference) |
| [docs/A2A-COMPAT.md](docs/A2A-COMPAT.md) | A2A AgentCard compatibility |
| [docs/SECURITY.md](docs/SECURITY.md) | Security and auth notes |

## Support

If clagentic:directory is useful to you: [ko-fi.com/clagentic](https://ko-fi.com/clagentic)

## Disclaimer

Not affiliated with Anthropic or OpenAI. Claude is a trademark of Anthropic. Codex is a
trademark of OpenAI. Provided "as is" without warranty. Users are responsible for
complying with their AI provider's terms of service.

## License

[FSL-1.1-MIT](LICENSE) — Functional Source License 1.1, with MIT as the Change License.

Free for personal, internal-business, evaluation, research, and non-commercial use.
Not free for offering this tool (or a substantial fork) as a competing commercial product.
Each release auto-converts to MIT on its second anniversary.
