# clagentic-directory

Agent capability registry. Tier 1 infrastructure daemon.

## CLI Naming

This project follows the clagentic CLI Naming Standard:
`clagentic-brand/docs/CLI-NAMING-STANDARD.md`

Binary names, env vars, syslog identifiers, and config paths are governed by that doc.
Violations are a review blocker.

Summary for this product:

| Item | Value |
|---|---|
| Binary | `clagentic-directory` |
| Tier | 1 (infrastructure daemon) |
| SyslogIdentifier | `clagentic-directory` |
| Config | `~/.config/clagentic/directory/directory.yaml` |
| Env vars | `CLAGENTIC_DIRECTORY_TOKEN`, `CLAGENTIC_DIRECTORY_LOG_LEVEL` |
| GitHub topic | `clagentic-platform` |

## Rules

- No direct writes to the live registry at runtime — all self-build output goes to `proposed_changes/`.
- Module path migrates from `forgejo.akuehner.com/clagentic/clagentic-directory` to
  `github.com/clagentic/clagentic-directory` at GitHub release time.
- All self-build URLs (`--self-build-engram-watch-url`, `--self-build-usage-relay-url`,
  `inspect --mcp-url`) must pass `validateSelfBuildURL()` — http/https only.
