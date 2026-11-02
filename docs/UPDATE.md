# `clagentic-directory update`

Converges a host's install from a deployment values file. The tool ships no
values: keep your values file in your own deployment-config repo and call
`update` from its post-merge step, which is the primary caller. A manual run
with the same argv is also a supported caller shape.

```bash
clagentic-directory update --values /path/to/values.yaml
```

Start from `examples/update-values.example.yaml`.

## What it does

In order:

1. Validate the whole values file and check privilege. Nothing is written
   before both pass.
2. Export the reviewed commit (see Binary currency) into a private temp
   directory and build it into a staged file beside `binary.install_path`
   (`<install_path>.update-new`). A fetch, ref or build failure leaves the
   host untouched. The built commit is printed as `source: built commit <sha>`.
3. Render the unit. If it differs from the installed one, write it through a
   temp file and rename, then run `systemctl daemon-reload`.
4. If the built binary's SHA-256 differs from the installed one, rename the
   staged file over `binary.install_path` (atomic; safe while the daemon runs).
5. For each unit in `supersedes`: stop it if active, disable it if enabled.
6. Enable the unit if it is not enabled.
7. Restart the unit if the unit file or binary changed, or if it is not
   running (a restart starts it).
8. Poll `/readyz` until HTTP 200 or `readyz.timeout`.

A converged host is a no-op: nothing is written, no `systemctl` command that
changes state runs, and the second run of any change prints
`converged: no changes`. The staged build file is always removed.

Update does not roll back. If a step fails it exits non-zero and prints what
had already changed; fix the cause and run it again.

## Binary currency

One mode: rebuild from a reviewed ref. Update never builds from the working
tree of `binary.source_dir`, so the branch, uncommitted changes or untracked
files a builder left there cannot reach the installed binary.

1. Read the `origin` URL from `binary.source_dir` (the only thing read from
   it; its working tree, branch and index are never read or modified).
2. In a private temporary repository, fetch `binary.source_ref` from that URL;
   when unset, fetch the remote's default branch (`HEAD`, resolved by the
   remote, not assumed to be `main`).
3. Export that exact commit into a private temporary directory and run
   `go build -C <export> -o <staged> <binary.package>` there. The directory is
   removed afterwards, success or not.

The fetch goes to a private repository rather than into `source_dir`: that
checkout is usually owned by another account, and a root-run fetch would leave
root-owned objects in it. Option (b) (refuse when `source_dir` is dirty or
behind) was rejected because the host's correctness would still depend on
someone keeping that tree on the ref, and a refusal would block a deployment
repo's post-merge step.

Requirements: the `git` binary and the Go toolchain on `PATH`, a usable `HOME`
(or `GOCACHE`) for the invoking user, and credentials for the `origin` remote
as the invoking user. `GIT_DIR`, `GIT_WORK_TREE` and the other
repository-location variables are dropped from git's environment, so a caller
running inside a git hook is unaffected. The Makefile is not used.

If `source_ref` names a ref the remote does not have, or the fetch fails, update
exits 4 before writing anything. If `source_dir` is not a git checkout with an
`origin` remote, it exits 2 before writing anything.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Converged, with or without changes |
| 2 | Invalid values file, unreadable values, bad usage, or `binary.source_dir` is not a git checkout with an `origin` remote |
| 3 | Insufficient privilege (not root) |
| 4 | Apply failure (fetch or unknown `source_ref`, build, file write, systemctl) |
| 5 | `/readyz` did not pass in time; inspect with `journalctl -u <unit>` |

## Values schema (`schema_version: 1`)

Unknown keys are an error. Paths must be absolute. Durations use Go syntax
(`60s`, `5m`). Unset daemon keys are left off the command line so the daemon's
own defaults apply.

### `unit`

| Key | Required | Meaning |
|---|---|---|
| `dir` | yes | Directory the unit file is written to. |
| `user` | yes | Account the daemon runs as. |
| `name` | no | Unit file name, default `clagentic-directory.service`. Must end in `.service`. |
| `env_file` | no | Rendered as `EnvironmentFile=<path>` (no `-` prefix: once set, the file must exist or the unit fails to start rather than running without its token). The path must be absolute with no whitespace. Put `CLAGENTIC_DIRECTORY_TOKEN` here. |

### `binary`

| Key | Required | Meaning |
|---|---|---|
| `install_path` | yes | Where the binary is installed; the unit's `ExecStart`. Its directory must exist. |
| `source_dir` | yes | Git checkout of this repository; only its `origin` URL is used. Its state does not affect the build. |
| `source_ref` | no | Ref to build, fetched from `origin` (branch, tag or full ref name). Default: the remote's default branch. Must not start with `-` or contain whitespace. Adding it keeps `schema_version: 1`. |
| `package` | no | Go package to build, default `./cmd/clagentic-directory`. |

### `daemon`

Each key is the daemon flag of the same name with `-` for `_`.

| Key | Flag | Notes |
|---|---|---|
| `registry_source` | `--registry-source` | Required: `file` or `git`. |
| `registry_dir` | `--registry-dir` | File mode only; required there. |
| `vocab_file` | `--vocab-file` | File mode only. |
| `registry_git_url` | `--registry-git-url` | Git mode only; required there. |
| `registry_git_ref` | `--registry-git-ref` | Git mode only. |
| `registry_git_subpath` | `--registry-git-subpath` | Git mode only; relative to the repo. |
| `registry_git_vocab` | `--registry-git-vocab` | Git mode only; relative to the repo. |
| `registry_git_poll` | `--registry-git-poll` | Git mode only. |
| `registry_cache_dir` | `--registry-cache-dir` | Git mode only. The daemon keeps its own clone here and polls origin. |
| `registry_secret_keyfile` | `--registry-secret-keyfile` | Git mode only. |
| `listen` | `--listen` | Default `:8444`. |
| `log_level` | `--log-level` | `debug`, `info`, `warn` or `error`. |
| `self_build_base_dir` | `--self-build-base-dir` | Required when any self-build mechanism is on; rejected otherwise. |
| `self_build_mcp_discovery` | `--self-build-mcp-discovery` | Boolean. |
| `self_build_source_watch_url` | `--self-build-source-watch-url` | http or https. |
| `self_build_source_watch_interval` | `--self-build-source-watch-interval` | Needs the URL. |
| `self_build_source_watch_window` | `--self-build-source-watch-window` | Needs the URL. |
| `self_build_usage_event_store_url` | `--self-build-usage-event-store-url` | http or https. |
| `self_build_usage_window` | `--self-build-usage-window` | Needs the URL. |

Mode-specific keys used in the wrong mode are rejected, because the daemon
would silently ignore them. `--auth-token` and the deprecated
`--vocabulary-extensions` have no key: pass the token through `unit.env_file`
and use `vocab_file` / `registry_git_vocab`.

### `readyz`

| Key | Meaning |
|---|---|
| `url` | Probe URL. Default: derived from `daemon.listen` against `127.0.0.1`. |
| `token_file` | File holding the bearer token (whitespace trimmed). Needed when the daemon enforces auth, because `/readyz` is authenticated. |
| `timeout` | How long to wait for readiness. Default `30s`. |

### `supersedes`

A list of unit names (`.service`, `.timer`, `.socket` or `.path`) that this
deployment replaces. Update stops and disables them; stopping an already
stopped or disabled unit is a no-op. Names are validated against a strict
systemd unit-name pattern. The tool carries no names of its own, and never
stops or disables its own unit even if it is listed.
