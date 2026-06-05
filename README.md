<p align="center">
  <img src="media/logo/directory-lockup-256.png" alt="Clagentic: Directory" width="260" />
</p>

<h4 align="center">Agent capability registry for multi-agent platforms.</h4>


A self-hosted HTTP registry that stores agent capability declarations and answers routing queries:
which agents handle a given intent, accept a conversation kind, or follow a specific agent in a
sequencing chain. Backed by a local directory of YAML files or a git repository.

No runtime dependencies. Drop-in binary or container. Operators own the registry; agents never
write to it directly.

## Quick start

```bash
clagentic-directory \
  --registry-source file \
  --registry-dir ./examples/registry/ \
  --listen :8444
```

```bash
curl -s localhost:8444/v1/agents | jq '.[].name'
curl -s localhost:8444/v1/agents/find?intent=code-review | jq
curl -s localhost:8444/readyz
```

## What it does

- **Capability registry.** Each agent declares its intents, conversation kinds, sequencing
  constraints, trust labels, and return format in a versioned YAML entry.
- **Routing queries.** Three query surfaces: `FindByCapability(intent...)`,
  `FindByConversationKind(kind)`, `FindBySequencing(afterAgent)`.
- **Two registry backends.** `--registry-source file` for local directories (with fsnotify
  hot-reload); `--registry-source git` for a tracked git repo (polled).
- **Strict vocabulary validation.** `schema_version: 2` entries are validated against a closed
  vocabulary. Extend it per-deployment with `--vocabulary-extensions`.
- **Self-build mechanisms.** Three opt-in mechanisms observe running agents and write
  `proposed_changes/` entries for operator review: MCP discovery, engram watch, usage-driven
  inference.

## Install

```bash
go install forgejo.akuehner.com/clagentic/clagentic-directory/cmd/clagentic-directory@latest
```

Or build from source:

```bash
git clone https://forgejo.akuehner.com/clagentic/clagentic-directory.git
cd clagentic-directory
go build -o clagentic-directory ./cmd/clagentic-directory/
```

## Subcommands

- `inspect` — one-shot MCP introspection for an agent, writes a `proposed_changes/` entry
  without starting the service:

  ```bash
  clagentic-directory inspect --agent <name> --mcp-url <http://localhost:PORT/mcp>
  ```

  See [docs/SELF-BUILD.md](docs/SELF-BUILD.md) for full flag reference and operator workflow.

## Documentation

- [docs/DESIGN.md](docs/DESIGN.md) — architecture and design decisions
- [docs/SCHEMA.md](docs/SCHEMA.md) — agent entry schema reference (v1 and v2)
- [docs/VOCABULARY.md](docs/VOCABULARY.md) — canonical vocabulary for v2 entries
- [docs/DEPLOY.md](docs/DEPLOY.md) — operator deployment guide
- [docs/SELF-BUILD.md](docs/SELF-BUILD.md) — self-build mechanisms (MCP, engram watch, usage inference)
- [docs/A2A-COMPAT.md](docs/A2A-COMPAT.md) — A2A AgentCard compatibility
- [docs/SECURITY.md](docs/SECURITY.md) — security and auth notes

## License

FSL-1.1-MIT — Functional Source License, Version 1.1, MIT Future License. See [LICENSE](LICENSE).
