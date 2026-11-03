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
| GET | `/readyz` | 200 if agents loaded, 503 if none. In git mode the body carries `sync.consecutive_failures` and `sync.last_success` (RFC 3339, absent until a fetch succeeds in this process); 503 `registry stale` after 3 consecutive failed fetches |
| GET | `/.well-known/agent-card.json/{name}` | A2A-compatible agent card |

`/v1/find` bounds each of `intent`, `conversation_kind` and `after_agent` to 256 bytes and 32 post-tokenization terms (`store.MaxFindQueryBytes`, `store.MaxFindQueryTerms`; derivation in `internal/store/querylimits.go`). Over-limit input is rejected before any lookup with HTTP 400 and a JSON body `{"error": ..., "param": ..., "limit": ..., "unit": "bytes"|"terms"}`; it is never truncated.

In git mode every git subprocess (clone, fetch, reset, the `remote.origin.url` read) runs under a bounded timeout, `store.DefaultGitTimeout` (2 minutes, overridable with `GitStoreConfig.GitTimeout`). A timeout counts as a failed sync, so a hung fetch (stalled network, blocked ssh) increments `sync.consecutive_failures` and trips the `registry stale` threshold above like any other fetch failure. `Close()` cancels the in-flight git call, so shutdown is not held up by a hung fetch.

In git mode the daemon does not trust any git config it did not write. Before every git invocation in a sync (fetch, reset, the `remote.origin.url` read) `GitStore` atomically overwrites `<cache clone>/.git/config` with a canonical config it generates: `core.repositoryformatversion`, `core.bare=false`, `core.hooksPath=/dev/null`, `core.fsmonitor=false`, and `remote.origin.url`/`fetch` for the configured URL and ref. Every git subprocess runs with `GIT_CONFIG_NOSYSTEM=1` and `GIT_CONFIG_GLOBAL=/dev/null`, with `protocol.allow=never` plus `protocol.<scheme>.allow=always` only for the scheme of the configured URL (http, https, ssh or file; any other scheme is refused). A `.git` that is not a plain directory is refused. Planted `url.<x>.insteadOf`, `protocol.ext.allow`, `core.fsmonitor`, `filter.<x>.smudge`, `credential.helper`, `core.gitProxy` and `core.sshCommand` in the cache clone are overwritten before git reads them. The per-key `-c` overrides remain as defense in depth. Not covered: `.git/info/attributes` and hooks remain on disk but are inert without the config that gives them a command; the config assumes a SHA-1 object format (no `extensions.objectformat`).

All responses are `application/json`. No authentication at the API layer in the current release (see `docs/SECURITY.md`).

## Go client

`client/go/client.go` provides a typed HTTP client for the above API surface. Import path:

```
github.com/clagentic/clagentic-directory/client/go
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
