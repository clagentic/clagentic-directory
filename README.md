# clagentic-directory

Parameterized agent capability registry for the Clagentic platform.

Public releasable. Consumes a separately-deployed configuration source (filesystem path or git URL)
for the agent registry. See [docs/DEPLOY.md](docs/DEPLOY.md) for setup.

## Quick start

    clagentic-directory --registry-source file --registry-dir ./examples/registry/ --listen :8444 &
    curl -s localhost:8444/v1/agents | jq

## Documentation

- [docs/DESIGN.md](docs/DESIGN.md) — architecture
- [docs/SCHEMA.md](docs/SCHEMA.md) — agent entry schema reference
- [docs/DEPLOY.md](docs/DEPLOY.md) — operator guide
- [docs/A2A-COMPAT.md](docs/A2A-COMPAT.md) — A2A AgentCard compatibility
- [docs/SECURITY.md](docs/SECURITY.md) — security and auth notes

## License

Apache 2.0
