# clagentic-directory: Architecture

## What it is

`clagentic-directory` is a parameterized agent capability registry for the Clagentic platform. It stores structured YAML entries describing which agents exist, what capabilities they expose, and when those capabilities apply (by intent, conversation kind, or sequencing after another agent). The service exposes a read-only HTTP API; no writes happen at runtime.

Backends are pluggable via the `Store` interface. The registry source is a runtime parameter — no agent data is baked into the binary.

## Store interface

`internal/store/store.go` defines:

```go
type Store interface {
    ListAgents() []Agent
    GetAgent(name string) (Agent, bool)
    FindByCapability(intents ...string) []Agent
    FindByConversationKind(kind string) []Agent
    FindBySequencing(afterAgent string) []Agent
    Reload() error
}
```

All methods are goroutine-safe. `Reload` is the only mutating operation; implementations hold an `sync.RWMutex` to allow concurrent reads.

## FileStore

`internal/store/filestore.go` reads all `*.yaml` / `*.yml` files from a local directory. On startup it does a blocking load and then starts an `fsnotify` watcher for hot-reload. When the watched directory sees a Write, Create, or Remove event, `Reload` is called automatically. The watcher goroutine exits cleanly when `Close` is called.

Use `--registry-source file --registry-dir <path>` to activate.

## GitStore

`internal/store/gitstore.go` clones a remote git repository at startup and polls it at a configurable interval (default 60s). Each poll does a `git fetch --depth=1` followed by `git reset --hard origin/<ref>`. This keeps the local cache shallow and current. SSH deploy keys are supported via `GIT_SSH_COMMAND`.

Use `--registry-source git --registry-git-url <url>` to activate.

## HTTP API surface

| Method | Path | Description |
|--------|------|-------------|
| GET | `/v1/agents` | List all agents |
| GET | `/v1/agents/{name}` | Get single agent by name |
| GET | `/v1/find?intent=<x>` | Find agents by intent |
| GET | `/v1/find?conversation_kind=<x>` | Find agents by conversation kind |
| GET | `/v1/find?after_agent=<x>` | Find agents by sequencing |
| GET | `/healthz` | Always 200 — process is alive |
| GET | `/readyz` | 200 if agents loaded, 503 if none |
| GET | `/.well-known/agent-card.json/{name}` | A2A-compatible agent card |

All responses are `application/json`. No authentication at the API layer in the current release (see `docs/SECURITY.md`).

## Go client

`client/go/client.go` provides a typed HTTP client for the above API surface. Import path:

```
forgejo.akuehner.com/clagentic/clagentic-directory/client/go
```

The client has a 10-second default timeout and exposes typed methods matching each query endpoint.

## Data flow

```
YAML files / git repo
        |
    loadDir()
        |
    parseEntry()
        |
  map[string]Agent (in-memory, RWMutex-guarded)
        |
    Store interface
        |
    HTTP handlers
        |
    JSON response
```
