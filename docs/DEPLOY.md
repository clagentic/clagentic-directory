# Operator Deployment Guide

## Quick start — file source

Build the binary and point it at a directory of agent YAML files:

```bash
go build -o clagentic-directory ./cmd/clagentic-directory/
./clagentic-directory \
  --registry-source file \
  --registry-dir ./examples/registry/ \
  --listen :8444
```

Verify:

```bash
curl -s localhost:8444/v1/agents | jq '.[].name'
curl -s localhost:8444/readyz
```

The service hot-reloads when files in the registry directory change — no restart required.

---

## systemd unit example

```ini
[Unit]
Description=clagentic-directory agent registry
After=network.target

[Service]
Type=simple
User=clagentic
ExecStart=/usr/local/bin/clagentic-directory \
  --registry-source file \
  --registry-dir /etc/clagentic/registry \
  --listen :8444 \
  --log-level info
Restart=on-failure
RestartSec=5s

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

---

## Environment notes

- **Port**: Defaults to `:8444`. Override with `--listen`.
- **Log level**: `--log-level debug|info|warn|error`. Defaults to `info`. Output is structured text on stderr.
- **Cache dir** (git source): Must be writable by the service user. The directory is created automatically if absent.
- **HTTPS token auth** (git source): If using HTTPS instead of SSH, embed credentials in the URL (`https://token@host/repo.git`). Token-file auth via `--registry-secret-keyfile` applies to SSH only.
- **No runtime config hot-reload**: Flag values are read once at startup. To change flags, restart the service. The registry directory or git contents hot-reload without a restart.
