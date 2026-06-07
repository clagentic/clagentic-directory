# Operator Deployment Guide

## Quick start — file source

Build the binary and point `--registry-dir` at a directory of agent YAML files:

```bash
go build -o clagentic-directory ./cmd/clagentic-directory/
./clagentic-directory \
  --registry-source file \
  --registry-dir /path/to/your/registry \
  --listen :8444
```

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

```ini
[Unit]
Description=clagentic-directory agent registry
After=network.target

[Service]
Type=simple
User=clagentic
SyslogIdentifier=clagentic-directory
ExecStart=/usr/local/bin/clagentic-directory \
  --registry-source file \
  --registry-dir /etc/clagentic/registry \
  --listen :8444
Restart=on-failure
RestartSec=5s
EnvironmentFile=-/etc/clagentic/directory/env

[Install]
WantedBy=multi-user.target
```

Install:

```bash
cp clagentic-directory /usr/local/bin/
cp deploy/clagentic-directory.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now clagentic-directory
```

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
    command:
      - --registry-source=file
      - --registry-dir=/registry
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
  --registry-cache-dir /var/cache/clagentic-directory/registry \
  --registry-secret-keyfile /etc/clagentic/registry-deploy-key \
  --listen :8444
```

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

## Vocabulary extensions

The base vocabulary is a closed set. To allow `schema_version: 2` entries that use
platform-specific values (custom trust labels, formats, intents, or conversation kinds),
supply a vocabulary extensions file:

```bash
./clagentic-directory \
  --registry-source file \
  --registry-dir /path/to/your/registry \
  --vocabulary-extensions /etc/clagentic/vocab-extensions.yaml \
  --listen :8444
```

The extensions file is a YAML file with four optional lists. See
`examples/vocabulary-extensions.yaml` for the format and `docs/VOCABULARY.md` for a
description of when to use extensions vs. proposing additions to the base vocabulary.

---

## Environment notes

- **Port**: Defaults to `:8444`. Override with `--listen`.
- **Log level**: `--log-level debug|info|warn|error`. Defaults to `info`. Output is structured text on stderr.
- **Cache dir** (git source): Must be writable by the service user. The directory is created automatically if absent.
- **HTTPS token auth** (git source): If using HTTPS instead of SSH, embed credentials in the URL (`https://token@host/repo.git`). Token-file auth via `--registry-secret-keyfile` applies to SSH only.
- **No runtime config hot-reload**: Flag values are read once at startup. To change flags, restart the service. The registry directory or git contents hot-reload without a restart.
