# Contributing to clagentic-directory

## Build and test

```bash
go build ./...                  # build all packages
go test ./...                   # run all tests
go vet ./...                    # vet
```

All PRs must pass `go test ./...` and `go vet ./...` clean before review.

## Crew test tier

`clagentic-loadout test run full` and `clagentic-loadout test run files PATH...` run the
tests declared in `.clagentic/loadout/profile.yaml`. Go writes no JUnit report, so both go
through `scripts/go_test_junit.py`. The script runs `make test-json` (the Makefile alone
owns the `go test` invocation and the build-cache fallback for a stripped environment),
then converts the `go test -json` events to the JUnit file loadout reads.

- `full` tests every package.
- `files` maps each given `.go` file (a `_test.go` alone is enough) to its package
  directory and tests each directory once. Files that are not `.go` are skipped; if none
  is left the run is an empty passing suite and `go` is not started. A directory name
  with a character outside `[A-Za-z0-9_./-]` is refused, because the package list passes
  through make's shell.
- Failing tests, packages that fail with no failing test (a panic outside a test,
  `TestMain` exiting non-zero), build failures, and tests or packages that never reach a
  terminal event all appear as failing JUnit cases.

Exit codes of `go_test_junit.py` (loadout counts any non-zero as a failed run, exit 70):

| Code | Meaning |
|---|---|
| 0 | Every test passed, or `files` selected no Go package. |
| 2 | `go test` failed, by a failing test or a build failure. This is make's code for a failed recipe, passed through (go itself exits 1); the two are told apart by the case names in the report (`[build]`, `[package]`). |
| 1 | make succeeded but the event stream holds a failing case. |
| 64 | Usage error, or a path that cannot be passed to `go test` safely. No report is written. |
| other | make's own code, for example 127 when make is not installed. |

`make test-scripts` (part of `make check`) runs the Python tests for the script.

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
