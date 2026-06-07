# Contributing to clagentic-directory

## Build and test

```bash
go build ./...                  # build all packages
go test ./...                   # run all tests
go vet ./...                    # vet
```

All PRs must pass `go test ./...` and `go vet ./...` clean before review.

## Module path

The module path is `github.com/clagentic/clagentic-directory`.

## Branch naming

| Prefix | Use for |
|---|---|
| `feat/` | New functionality |
| `fix/` | Bug fixes |
| `chore/` | Maintenance, dependency bumps, tooling |
| `docs/` | Documentation-only changes |
| `refactor/` | Code restructuring without behaviour change |

Include a task ID when one exists, e.g. `feat/add-token-auth`.

## Pull request expectations

- Tests are required for every bug fix and every new code path.
- Do not modify existing tests to make them pass — fix the code.
- `go vet ./...` must be clean.
- No new dependencies without explicit justification.
- No hardcoded paths, hostnames, or secrets.

## Import graph

The internal packages have a strict layered structure:

```
store      -> (stdlib, yaml, fsnotify)
selfbuild  -> store, (stdlib)
api        -> store, (stdlib)
cmd/clagentic-directory -> api, selfbuild, store
```

`api` must never import `selfbuild`. `store` must never import `api` or `selfbuild`.

## Code style

- Match the existing style. Run `gofmt` before committing.
- Comments explain why, not what.
- No bare `error` returns without context — wrap with `fmt.Errorf("...: %w", err)`.
