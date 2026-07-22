# Session Hooks

`scripts/dispatch-discipline-hook.py` is an optional Claude Code `SessionStart`
hook shipped alongside the clagentic-directory service. It is not part of the
Go daemon; it is a standalone script an operator wires into their own harness
config.

## dispatch-discipline-hook.py

Purpose: on session start, inject a standing directive reminding the model to
dispatch build/implement/review/research/debug work to a crew agent rather
than doing it inline, and point the session at a running clagentic-directory
instance (`CLAGENTIC_DIRECTORY_URL`, default `http://localhost:8444`) as the
live source of truth for which agent handles which intent.

### Gating: when the directive fires

The hook only makes sense for sessions that are actually operating under
agent/crew dispatch — a plain chat session should not be nagged to dispatch
work it was never going to delegate. Gating is controlled by the
`CLAGENTIC_DISPATCH_MODE` environment variable:

| Mode | Behavior |
|---|---|
| `auto` (default, unset also means this) | Reads the hook's own `SessionStart` JSON payload from stdin and checks the `agent_type` field. Any non-empty value is treated as an agent session and the directive fires (healthy/degraded/error branches, as before). Empty/absent `agent_type` — or stdin that is empty or not valid JSON — is treated as a vanilla session: the hook emits nothing and exits 0. No directory probe happens in this case either. |
| `always` | Fires the directive on every session regardless of payload — the original, pre-gating behavior. Use this if your harness doesn't populate `agent_type`, or you want the directive everywhere. |
| `off` | Never fires, regardless of payload. Full opt-out. |

Any other value for `CLAGENTIC_DISPATCH_MODE` falls back to `auto`.

`agent_type` is read directly from the hook's own invocation payload — the
built-in signal a compatible harness sets when it starts a session as a named
agent. The hook does **not** validate the value against a roster, a `.crew/`
directory, or any other filesystem convention: "non-empty `agent_type`" is the
entire test. This keeps the hook portable across installs with different crew
layouts — baking one deployment's directory structure into a shared, released
tool is out of scope for this hook (see `CONTRIBUTING.md` for build-to-share
conventions).

### Wiring it up

Add it as a `SessionStart` hook in your harness config, pointing at the
script path in your clagentic-directory checkout/install, e.g.:

```json
{
  "hooks": {
    "SessionStart": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "python3 /path/to/clagentic-directory/scripts/dispatch-discipline-hook.py"
          }
        ]
      }
    ]
  }
}
```

Environment (optional):

```bash
CLAGENTIC_DIRECTORY_URL=http://localhost:8444   # directory instance to point at
CLAGENTIC_DIRECTORY_TOKEN=<bearer-token>         # if the directory requires auth
CLAGENTIC_DISPATCH_MODE=auto                     # auto (default) | always | off
```

### Fail-open contract

The hook never blocks or aborts a session (`sys.exit(0)` on every path,
including hook errors). When it does decide to fire the directive, it never
fails open silently — a degraded or unreachable directory still starts the
session but with a loud notice explaining why and what to check
(`systemctl status clagentic-directory`, `curl -s $CLAGENTIC_DIRECTORY_URL/healthz`).
A vanilla session gated to silence under `auto` is the intended no-op, not a
failure.

### Tests

`scripts/test_dispatch_discipline_hook.py` covers the gating matrix. Run with:

```bash
python3 -m unittest discover -s scripts -p "test_dispatch_discipline_hook.py" -v
```
