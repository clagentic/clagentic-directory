# Security and Auth Notes

## Current auth mode: none

The MVP release has no authentication on the HTTP API. The service is intended to run on an internal network segment accessible only to trusted platform components. Do not expose port 8444 to the public internet without adding a reverse proxy with access controls.

## Planned: token auth mode

A future release will add bearer token authentication. The design is:

- Operator configures a shared secret (token) via a flag or environment variable.
- Callers include `Authorization: Bearer <token>` on every request.
- The service validates the token before processing any request.
- `/healthz` remains unauthenticated (required by load balancers).
- `/readyz` is optionally unauthenticated (configurable).

## Do not ship your config in this repo

The `examples/registry/` directory contains illustrative agent entries only. It does not contain deployment configuration, tokens, hostnames, or key material. Do not commit:

- SSH deploy keys or their public counterparts
- API tokens or bearer secrets
- Production agent registry files
- Internal hostnames or IP addresses

Keep production agent YAML files in a private repository or a directory outside version control. Point `--registry-dir` or `--registry-git-url` at that location.

## Sensitive data in agent entries

Agent YAML files may contain information about internal agent topology, sequencing, and trust relationships. Treat your production registry as a confidential operational artifact even though individual entries contain no credentials.

## Git source: deploy key hygiene

When using `--registry-source git`:

- Use a read-only deploy key scoped to the registry repository only.
- Store the key at a path accessible only to the service user (mode `0600`).
- Rotate the key if the service host is compromised.
- The cache directory (`--registry-cache-dir`) contains a full (shallow) clone. Apply the same access controls as the deploy key.

## TLS

The service binds plain HTTP. Terminate TLS at a reverse proxy (nginx, Caddy, Envoy) in front of the service. Do not expose the plain HTTP port beyond the local network.
