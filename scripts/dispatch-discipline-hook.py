#!/usr/bin/env python3
"""SessionStart hook: dispatch-discipline.

Injects the standing dispatch default into every session and points the
session at the clagentic-directory as the live source of truth for agent
roles — so the roster is never hardcoded in the hook.

DISPATCH DEFAULT (the process this hook enforces):
  - USE AGENTS TO DO THE WORK. Build/implement/review/research/debug is
    dispatched to a crew agent via the `Agent` tool — the session does NOT
    do the build work itself. Session orchestrates; agents execute.
  - WHICH agent = query the clagentic-directory (`/v1/find`, `/v1/agents`),
    the live source of truth for what each agent does. NOT a hardcoded list.
  - Default dispatch is in-session single-agent (operator in the loop).
  - Teammates (Agent Teams) are an explicit opt-in, not the default.
  - A2A is future / gated.

FAIL-OPEN, VERBOSE — by contract (operator directive 2026-06-15):
  - This hook NEVER fails closed: it never blocks or aborts a session.
  - This hook NEVER fails open SILENTLY: every run states which branch it
    took. If the directory is unreachable, the session still starts AND a
    loud notice says so, why, and what to check — so agent selection is
    never operating blind without the operator knowing.

Both outcomes are emitted as SessionStart additionalContext so they are
visible to the model (and, via the directive, to the operator who is told
to surface them).

Parameterized (build-to-share): no hardcoded host beyond the documented
default. Override with CLAGENTIC_DIRECTORY_URL. Optional bearer token via
CLAGENTIC_DIRECTORY_TOKEN (auth is disabled on local installs).
"""

from __future__ import annotations

import json
import os
import sys
import urllib.error
import urllib.request

# Documented default (clagentic-directory docs/DEPLOY.md: --listen :8444).
_DEFAULT_URL = "http://localhost:8444"
_HEALTH_TIMEOUT_S = 2.0


def _directory_base_url() -> str:
    return os.environ.get("CLAGENTIC_DIRECTORY_URL", _DEFAULT_URL).rstrip("/")


def _probe_health(base_url: str) -> tuple[bool, str]:
    """Probe GET /healthz. Returns (ok, detail). Never raises."""
    url = f"{base_url}/healthz"
    try:
        req = urllib.request.Request(url, method="GET")
        with urllib.request.urlopen(req, timeout=_HEALTH_TIMEOUT_S) as resp:
            body = resp.read(256).decode("utf-8", "replace").strip()
            if resp.status == 200 and '"status"' in body and "ok" in body:
                return True, body
            return False, f"HTTP {resp.status}: {body[:120]}"
    except urllib.error.URLError as e:
        return False, f"unreachable: {getattr(e, 'reason', e)}"
    except (TimeoutError, OSError) as e:
        return False, f"unreachable: {e}"
    except Exception as e:  # last-resort: still fail open
        return False, f"probe error: {type(e).__name__}: {e}"


def _healthy_directive(base_url: str) -> str:
    return (
        "DISPATCH DISCIPLINE · USE AGENTS TO DO THE WORK. Build, implement, review, "
        "research, debug — dispatch it to a crew agent via the `Agent` tool; do NOT "
        "do the build work yourself in this session. The session ORCHESTRATES; agents "
        "EXECUTE. Default to in-session single-agent dispatch (operator in the loop); "
        "teammates (Agent Teams) opt-in; A2A future. "
        f"To pick WHICH agent, consult the clagentic-directory at {base_url} — it is the "
        f"live source of truth for what each agent does: `{base_url}/v1/find?intent=<intent>` "
        f"or `{base_url}/v1/agents`. Do NOT hardcode or guess the roster. "
        "Directory healthy this session."
    )


def _degraded_directive(base_url: str, detail: str) -> str:
    return (
        "DISPATCH DISCIPLINE (DEGRADED) · USE AGENTS TO DO THE WORK still applies — "
        "dispatch build/implement/review/research/debug to a crew agent via the `Agent` "
        "tool; do NOT do the build work yourself in this session. BUT the clagentic-directory "
        f"could NOT be consulted ({base_url} {detail}), so WHICH agent does what is operating "
        "BLIND — the live roster/triggers are unavailable; do not claim directory-backed "
        "routing. Surface this to the operator. Check: `systemctl status clagentic-directory` "
        f"and `curl -s {base_url}/healthz`. (Hook failed OPEN by design — session continues.)"
    )


def _emit(content: str) -> None:
    print(json.dumps({
        "hookSpecificOutput": {
            "hookEventName": "SessionStart",
            "additionalContext": content,
        }
    }))


def main() -> None:
    base_url = _directory_base_url()
    try:
        ok, detail = _probe_health(base_url)
        if ok:
            _emit(_healthy_directive(base_url))
            # Verbose success breadcrumb to stderr (shows in hook logs).
            print(f"[dispatch-discipline] directory healthy at {base_url}", file=sys.stderr)
        else:
            _emit(_degraded_directive(base_url, detail))
            print(
                f"[dispatch-discipline] directory DEGRADED at {base_url}: {detail} "
                "— failing open, session continues",
                file=sys.stderr,
            )
    except Exception as e:
        # Absolute last resort: never fail closed, never fail open silently.
        _emit(
            "DISPATCH DISCIPLINE (HOOK ERROR) · Default to in-session single-agent "
            "dispatch still applies, but this hook errored and could not consult the "
            f"directory: {type(e).__name__}: {e}. Surface to operator."
        )
        print(f"[dispatch-discipline] hook error (failed open): {e}", file=sys.stderr)
    # Always exit 0 — never block a session.
    sys.exit(0)


if __name__ == "__main__":
    main()
