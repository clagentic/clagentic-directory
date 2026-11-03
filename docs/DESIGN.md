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

`internal/store/gitstore.go` shallow-clones a remote git repository at startup and polls it at a configurable interval (default 60s). Each poll runs `git ls-remote <url> refs/heads/<ref>`; when the SHA differs from the served one it runs `git clone --depth=1 --branch <ref>` into a fresh directory (`os.MkdirTemp` under the cache dir, mode 0700), checks that the clone's HEAD equals the ls-remote SHA, loads and validates the registry and vocabulary from that directory, swaps the served snapshot, and deletes the previous directory. SSH deploy keys are supported via `GIT_SSH_COMMAND`.

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

In git mode every git subprocess (ls-remote, clone, rev-parse) runs under a bounded timeout, `store.DefaultGitTimeout` (2 minutes, overridable with `GitStoreConfig.GitTimeout`). A timeout counts as a failed sync, so a hung git call (stalled network, blocked ssh) increments `sync.consecutive_failures` and trips the `registry stale` threshold above like any other failure. `Close()` cancels the in-flight git call, so shutdown is not held up by a hung call.

In git mode the daemon never runs git inside a directory that existed before the sync created it. There is no persistent clone to fetch into: `ls-remote` runs in a scratch directory the daemon creates for the call, and the clone goes into a new directory the daemon creates for that sync. Nothing a writer to the cache dir plants in an older directory (`.git/config`, `.git/commondir`, `info/attributes`, hooks) is ever read by git. At startup the daemon always clones fresh and removes leftover `clone-*` and `probe-*` directories and the former per-URL clone directory, without loading them; because no stale copy is trusted, a failed first sync is fatal at startup. A sync that fails at any step (ls-remote, clone, HEAD mismatch because origin moved mid-sync, registry or vocabulary validation) removes its new directory, keeps the previous snapshot and counts as a failed sync. An unchanged origin SHA makes no git call beyond ls-remote.

Every git subprocess runs with `GIT_CONFIG_NOSYSTEM=1`, `GIT_CONFIG_GLOBAL=/dev/null` and `GIT_CEILING_DIRECTORIES` set to the cache dir, with `-c core.hooksPath=/dev/null -c core.fsmonitor=false` and `protocol.allow=never` plus `protocol.<scheme>.allow=always` only for the scheme of the configured URL (http, https, ssh or file; any other scheme, or a URL starting with `-`, is refused). Remaining residuals: the object format is assumed to be SHA-1, so a SHA-256 remote fails loudly; the operator-supplied environment of the service process (for example `GIT_CONFIG_COUNT`) is inherited on the non-KeyFile path and is part of the deployment's trust boundary; and the cache dir itself must be writable only by the service user (a writer replacing it wholesale controls the daemon's working area).

Credentials: operator git config is not honored. `~/.gitconfig` and `/etc/gitconfig` are ignored, so `credential.helper`, `http.proxy` and `http.sslCAInfo` set there have no effect on the daemon. A private remote gets credentials from an SSH deploy key (`KeyFile`, `git@` URLs only) or from curl-level `~/.netrc` of the service user. Verified under the isolation on git 2.43.0: libcurl still consults `$HOME/.netrc` (with `GIT_CONFIG_NOSYSTEM=1` and `GIT_CONFIG_GLOBAL=/dev/null` set, a `HOME` without a matching entry logs "Couldn't find host ... in the .netrc file", a `HOME` with one does not). Whether the stored login is then sent depends on the server challenging with 401; this was observed only against a remote that is readable anonymously, where git sends no `Authorization` header. A remote that is readable anonymously, such as `http://git.example.invalid/org/registry.git`, needs no credentials under the isolation; `ls-remote` and a shallow `clone --depth=1 --branch` were verified against such a remote with only the configured scheme allowed.

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
