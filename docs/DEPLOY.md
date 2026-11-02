# Operator Deployment Guide

## Quick start — file source

Build the binary and point `--registry-dir` at a directory of agent YAML files:

```bash
go build -o clagentic-directory ./cmd/clagentic-directory/
./clagentic-directory \
  --registry-source file \
  --registry-dir /path/to/your/registry \
  --vocab-file /path/to/your/vocabulary.v1.yaml \
  --listen :8444
```

`--vocab-file` enables strict vocabulary validation. Start from
`examples/vocabulary.v1.yaml` (see "Vocabulary" below). Omitting it starts the daemon
in ValidateOpen mode, which accepts any vocabulary value and logs an UNSAFE warning.

Verify:

```bash
curl -s localhost:8444/v1/agents | jq '.[].name'
curl -s localhost:8444/readyz
```

The service hot-reloads when files in the registry directory change — no restart required.

> **Note:** `examples/registry/` in this repo contains platform self-test fixtures only.
> Do not use it as a deploy target.

---

## systemd unit example

Replace every `<PLACEHOLDER>` before use:

| Placeholder | Meaning |
|---|---|
| `<SERVICE_USER>` | Unprivileged account that runs the daemon. Needs read access to the registry dir and vocabulary file. |
| `<BIN_PATH>` | Where you installed the `clagentic-directory` binary. |
| `<REGISTRY_DIR>` | Directory of agent YAML files (`--registry-dir`). |
| `<VOCAB_FILE>` | Your vocabulary file (`--vocab-file`), started from `examples/vocabulary.v1.yaml`. |
| `<LISTEN_ADDR>` | Listen address, e.g. `:8444` (`--listen`). |
| `<LOG_LEVEL>` | `debug`, `info`, `warn` or `error` (`--log-level`). |
| `<ENV_FILE>` | Optional environment file, e.g. for `CLAGENTIC_DIRECTORY_TOKEN`. |

```ini
[Unit]
Description=clagentic-directory agent registry
After=network.target

[Service]
Type=simple
User=<SERVICE_USER>
SyslogIdentifier=clagentic-directory
ExecStart=<BIN_PATH> \
  --registry-source file \
  --registry-dir <REGISTRY_DIR> \
  --vocab-file <VOCAB_FILE> \
  --listen <LISTEN_ADDR> \
  --log-level <LOG_LEVEL>
Restart=on-failure
RestartSec=5s
EnvironmentFile=-<ENV_FILE>

[Install]
WantedBy=multi-user.target
```

`--vocab-file` is deliberately included. Without it the daemon starts in ValidateOpen
mode: `schema_version: 2` vocabulary values are not validated and any value is accepted.
Copy `examples/vocabulary.v1.yaml` to `<VOCAB_FILE>` as a starting point and see
`docs/VOCABULARY.md` for how to extend it.

This repository does not ship a unit file. Write the unit above to a file of your choice
(`<UNIT_FILE>`, e.g. `clagentic-directory.service`) under your systemd unit directory
(`<SYSTEMD_UNIT_DIR>`), then install and enable it:

```bash
install -m 0755 clagentic-directory <BIN_PATH>
# write the unit text above to <SYSTEMD_UNIT_DIR>/<UNIT_FILE>
systemctl daemon-reload
systemctl enable --now <UNIT_FILE>
```

---

## Converging a host with `update`

`clagentic-directory update --values <file>` renders the unit above from a
values file, keeps the binary current, retires units you name as superseded,
restarts only when something changed, and verifies `/readyz`. It is idempotent,
so it suits a deployment-config repo's post-merge step.

```bash
clagentic-directory update --values /path/to/values.yaml
```

The values schema, step order and exit codes are in `docs/UPDATE.md`; start
from `examples/update-values.example.yaml`.

---

## docker-compose example

```yaml
services:
  clagentic-directory:
    image: clagentic/clagentic-directory:latest
    restart: unless-stopped
    ports:
      - "8444:8444"
    volumes:
      - ./registry:/registry:ro
      - ./vocabulary.v1.yaml:/vocabulary.v1.yaml:ro
    command:
      - --registry-source=file
      - --registry-dir=/registry
      - --vocab-file=/vocabulary.v1.yaml
      - --listen=:8444
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://localhost:8444/healthz"]
      interval: 30s
      timeout: 5s
      retries: 3
```

---

## Git source with SSH deploy key

Generate a deploy key (read-only):

```bash
ssh-keygen -t ed25519 -f /etc/clagentic/registry-deploy-key -N ""
# Add the public key to your git host as a read-only deploy key.
```

Run with git source:

```bash
./clagentic-directory \
  --registry-source git \
  --registry-git-url git@forgejo.example.com:myorg/agent-registry.git \
  --registry-git-ref main \
  --registry-git-poll 60s \
  --registry-git-subpath registry/ \
  --registry-git-vocab vocabulary.v1.yaml \
  --registry-cache-dir /var/cache/clagentic-directory/registry \
  --registry-secret-keyfile /etc/clagentic/registry-deploy-key \
  --listen :8444
```

`--registry-git-vocab` is the path of the vocabulary file inside the git repository.
Use `--vocab-file` instead if the vocabulary file lives on local disk. Omitting both
starts the daemon in ValidateOpen mode (UNSAFE).

The service clones once at startup and re-fetches on the poll interval. The local cache survives restarts; a restart with the cache present skips the initial clone.

### Key rotation

The deploy key is long-lived and stored plaintext on disk. Rotate it periodically or
on any suspected compromise:

```bash
# 1. Generate a new key
ssh-keygen -t ed25519 -f /etc/clagentic/registry-deploy-key-new -N ""

# 2. Add the new public key to your git host as a read-only deploy key
#    (do this BEFORE removing the old key — avoids downtime)

# 3. Replace the key file
mv /etc/clagentic/registry-deploy-key-new /etc/clagentic/registry-deploy-key
mv /etc/clagentic/registry-deploy-key-new.pub /etc/clagentic/registry-deploy-key.pub

# 4. Restart the service (it re-reads the key file on startup)
systemctl restart clagentic-directory

# 5. Remove the old public key from your git host
```

Keep the key file mode `0600` and owned by the service user. The cache directory
(`--registry-cache-dir`) should be `0700` for the same user — the service enforces
this on first creation, but verify after manual interventions.

---

## Vocabulary

The base vocabulary is a closed set. Pass a vocabulary file with `--vocab-file` to
enable strict validation of `schema_version: 2` entries (ValidateStrict mode):

```bash
./clagentic-directory \
  --registry-source file \
  --registry-dir /path/to/your/registry \
  --vocab-file /path/to/your/vocabulary.v1.yaml \
  --listen :8444
```

Start from `examples/vocabulary.v1.yaml`. Add platform-specific trust labels, formats,
intents or conversation kinds directly to your copy. `docs/VOCABULARY.md` describes the
vocabulary and when to extend your file versus proposing additions to the base set.

Without `--vocab-file` the daemon starts in ValidateOpen mode and accepts any
vocabulary value; it logs an UNSAFE warning at startup. Do not run production this way.
For a git source, `--registry-git-vocab` names the vocabulary file inside the repo.

### Deprecated: `--vocabulary-extensions`

`--vocabulary-extensions <file>` merges extra values into the base enums from a
separate YAML file. It is deprecated: the daemon logs a warning when it is used.
Migrate to `--vocab-file`. See `examples/vocabulary-extensions.yaml` for the old format.

---

## Environment notes

- **Port**: Defaults to `:8444`. Override with `--listen`.
- **Log level**: `--log-level debug|info|warn|error`. Defaults to `info`. Output is structured text on stderr.
- **Cache dir** (git source): Must be writable by the service user. The directory is created automatically if absent.
- **HTTPS token auth** (git source): If using HTTPS instead of SSH, embed credentials in the URL (`https://token@host/repo.git`). Token-file auth via `--registry-secret-keyfile` applies to SSH only.
- **No runtime config hot-reload**: Flag values are read once at startup. To change flags, restart the service. The registry directory or git contents hot-reload without a restart.
