# `clagentic-directory update`

Converges a host's install from a deployment values file. The tool ships no
values: keep your values file in your own deployment-config repo and call
`update` from its post-merge step, which is the primary caller. A manual run
with the same argv is also a supported caller shape.

```bash
clagentic-directory update --values /path/to/values.yaml
clagentic-directory update          # reads the default values file
```

Start from `examples/update-values.example.yaml`.

## Values file location

With no `--values`, update reads
`<config root>/clagentic/directory/update-values.yaml`, where the config root
is `$XDG_CONFIG_HOME`, else `$HOME/.config` (for root, normally
`/root/.config/clagentic/directory/update-values.yaml`). The deployment-config
repo installs that file; this repo ships none. If the root cannot be resolved
(neither variable set, or a relative `$XDG_CONFIG_HOME`) or the file is
missing, update exits 2 naming the path. There is no other fallback.

Every run first prints `values: <path> (default|explicit)` on stdout, before
reading the file, so a log shows which file was used. The default depends on
the runner's `XDG_CONFIG_HOME` and `HOME`.

The values file, default or explicit, must be a regular file (a symlink is
refused), owned by the user running update, and not group- or world-writable
(mode 0600 or 0644). Otherwise update exits 2 naming the path and the rule:
the file decides what root builds and installs.

## Post-merge model

This repo's `.clagentic/loadout/config.yaml` holds one post-merge step,
`clagentic-directory update`, with no arguments and no host value. A merge here
runs the already-installed binary, which converges the host from its own values
file. The first merge after this default was introduced runs a binary that
predates it and exits 2; run `clagentic-directory update --values <file>` once
by hand to install a binary that has it.

## What it does

In order:

1. Validate the whole values file and check privilege. Nothing is written
   before both pass.
2. Export the reviewed commit (see Binary currency) into a private temp
   directory. When the installed binary is already the build of that commit,
   skip the build. Otherwise build into a staged file beside
   `binary.install_path` (`<install_path>.update-new`). A fetch, ref or build
   failure leaves the host untouched. The commit is printed as
   `source: built commit <sha>`.
3. Render the unit. If it differs from the installed one, write it through a
   temp file and rename, then run `systemctl daemon-reload`.
4. If a build ran and its SHA-256 differs from the installed binary's, rename
   the staged file over `binary.install_path` (atomic; safe while the daemon
   runs).
5. For each unit in `supersedes`: stop it if active, disable it if enabled.
6. Enable the unit if it is not enabled.
7. Restart the unit if the unit file or binary changed, or if it is not
   running (a restart starts it).
   After the restart, if a build ran, write the install record (see Binary
   currency). A failed record write fails the run, after the restart has
   already happened.
8. Poll `/readyz` until HTTP 200 or `readyz.timeout`.

The service restarts only on change: a new unit file, a new binary, or a unit
that is not running. A converged host is a no-op: nothing is built or written,
no `systemctl` command that changes state runs, and the second run of any
change prints `converged: no changes`. A changed unit with the same commit
rewrites the unit and restarts without rebuilding. The staged build file is
always removed.

Update does not roll back. If a step fails it exits non-zero and prints what
had already changed; fix the cause and run it again.

## Binary currency

One mode: rebuild from a reviewed ref. Update never builds from the working
tree of `binary.source_dir`, so the branch, uncommitted changes or untracked
files a builder left there cannot reach the installed binary.

1. Take the fetch URL: `binary.source_url` when set (then `source_dir` is not
   read at all). When unset, read the `origin` URL from `binary.source_dir`
   with `git config --get remote.origin.url` only (no hooks, no fsmonitor);
   its working tree, branch and index are never read or modified.
2. In a private temporary repository, fetch `binary.source_ref` from that URL;
   when unset, fetch the remote's default branch (`HEAD`, resolved by the
   remote, not assumed to be `main`).
3. Export that exact commit into a private temporary directory and run
   `go build -C <export> -trimpath -buildvcs=false -ldflags "-X main.buildRevision=<commit>" -o <staged> -- <binary.package>`
   there. The directory is removed afterwards, success or not.

### What is compared

Builds are compared on the resolved source commit, not on binary bytes. An
export carries no VCS metadata and sits at a fresh temporary path, and without
`-trimpath` that path is embedded in the binary and its build id, so two builds
of one commit differ byte for byte. `-trimpath` now makes them identical, and
`-buildvcs=false` (on `update` and on `make build`) stops Go embedding
`vcs.revision`, `vcs.time` and `vcs.modified` when building inside a checkout,
so `make build` and `update` produce the same bytes at one commit (a test builds
both and compares SHA-256). Currency does not depend on that: it only means the
first `update` after a `make build` deploy, which has no install record,
rebuilds, finds identical bytes, writes the record and does not restart.

After installing, update writes `<install_path>.source` (root, mode 0644) with
the commit, the Go package and the SHA-256 of the installed binary. On the next
run the binary counts as current when all of these hold:

- the freshly fetched `source_ref` resolves to the recorded commit;
- `binary.package` equals the recorded package;
- the installed file's SHA-256 equals the recorded digest.

The commit always comes from the fresh fetch, never from the record, so a
tampered record can force a rebuild but cannot make update skip one for a
different commit. A binary replaced behind update's back fails the digest check
and is rebuilt. Anything able to write the install directory can replace the
binary and the record together, so keep `install_path`'s directory root-owned.
A host with no record (first run after upgrading to this version) is rebuilt
once; the service restarts only if the rebuilt bytes differ from the installed
ones, and the record is written either way.

The unit file is compared by its rendered bytes, as before.

### Revision stamp

Every build sets `main.buildRevision` to the commit it was built from:
`update` (with `source_url` or `source_dir`; both build an exported commit, so
the stamp is never dirty) and `make build` (the commit of the working tree,
with a `-dirty` suffix when tracked files have uncommitted changes or an
untracked `.go`, `go.mod` or `go.sum` file exists, since `go build` compiles
those; other untracked files such as `attempts/` do not count). Read it
with `clagentic-directory --version`; the daemon also logs it at start as
`revision=<sha>`.

### Fetch URL trust

`source_dir/.git/config` is writable by whoever owns that checkout, and update
fetches and builds as root. With no `source_url`, that account can point
`origin` at any repository and root will build and install it. **Set
`binary.source_url` whenever `source_dir` is writable by any account other than
the one running update.** The values file comes from your reviewed config repo,
so the URL is then reviewed too.

Whichever way the URL is found, the fetch runs with `protocol.allow=never` and
only https and ssh allowed. A URL with another scheme (`file://`, `ext::`,
`git://`, `http://`, a local path, or the scp-like `user@host:path` form) is
refused: exit 2 for `source_url` (checked with the whole values file, before any
write), exit 4 for an `origin` URL. The URL is never printed. Use
`ssh://user@host/path` in place of the scp-like form.

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

If `source_ref` names a ref the remote does not have, the fetch fails, or the
`origin` URL uses a disallowed transport, update exits 4 before writing
anything. If `source_dir` has no `origin` remote or is
not a git repository, it exits 2 before writing anything and names
`source_dir`. If reading the remote fails for another reason (no `git` binary,
a permission error), it exits 4 with git's error text; the URL is never printed.

### Upgrading from the working-tree build

Earlier versions built whatever was checked out in `binary.source_dir`. This
version fetches `origin` instead, which changes behaviour for existing
`schema_version: 1` values files:

- Update fetches as the account that runs it (normally root, via the
  post-merge step), so that account must be able to read `source_dir`'s
  `origin`, not just the account that owns the checkout.
- For a private SSH or HTTPS origin, configure credentials for that account
  (an SSH key or credential helper), or point `origin` at a URL it can read.
- An unreadable origin fails with exit 4 before any host write. The previous
  version would have built the local tree in the same situation.
- Local commits and uncommitted changes in `source_dir` no longer reach the
  binary: push and merge what you want deployed, or set `source_ref`.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Converged, with or without changes |
| 2 | Invalid values file (including a `source_url` that is not https or ssh), an unsafe values file (symlink, wrong owner, group- or world-writable), a missing default values file, unreadable values, bad usage, or `binary.source_dir` is not a git checkout with an `origin` remote |
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
| `source_dir` | yes | Git checkout of this repository; only its `origin` URL is used, and only when `source_url` is unset. Its state does not affect the build. |
| `source_url` | no | `https://` or `ssh://` URL to fetch from. When set, `source_dir`'s git config is never read. Default: `source_dir`'s `origin`. Recommended whenever `source_dir` is writable by another account (see Fetch URL trust). Adding it keeps `schema_version: 1`. |
| `source_ref` | no | Ref to build, fetched from `origin` (branch, tag or full ref name). Default: the remote's default branch. Must not start with `-` or contain whitespace. Adding it keeps `schema_version: 1`. |
| `package` | no | Go package to build, default `./cmd/clagentic-directory`. Must not start with `-` or contain whitespace. Passed to `go build` after `--`. |

### `daemon`

Each key is the daemon flag of the same name with `-` for `_`.

| Key | Flag | Notes |
|---|---|---|
| `registry_source` | `--registry-source` | Required: `file` or `git`. |
| `registry_dir` | `--registry-dir` | File mode only; required there. |
| `vocab_file` | `--vocab-file` | File mode only. The daemon refuses to start if `--vocab-file` is given with `--registry-source=git`. |
| `registry_git_url` | `--registry-git-url` | Git mode only; required there. |
| `registry_git_ref` | `--registry-git-ref` | Git mode only. |
| `registry_git_subpath` | `--registry-git-subpath` | Git mode only; relative to the repo. |
| `registry_git_vocab` | `--registry-git-vocab` | Git mode only; relative to the repo. Loaded from each synced clone and enforced strictly; a missing or invalid file fails the sync. The daemon logs the vocabulary path, snapshot SHA and `validation_mode` on every load, and warns `ValidateOpen (UNSAFE)` when it is unset. |
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
