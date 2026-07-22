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

GATING — three states, so the directive fires only for sessions actually
operating under agent/crew dispatch, and stays silent for plain chat
sessions (build-to-share: no workspace-specific detection heuristic):

  1. auto (default, CLAGENTIC_DISPATCH_MODE unset or "auto"):
     The hook reads its OWN SessionStart hook payload from stdin (JSON) and
     checks the `agent_type` field. This is the harness's own built-in
     signal for "this session was started as a named agent," emitted by
     the same harness mechanism regardless of which crew/deployment is
     running on top of it. ANY non-empty `agent_type` value counts as an
     agent session — the hook does NOT validate it against a roster, a
     `.crew/` directory, or any other workspace-specific convention. That
     is a deliberate portability choice: this is a released shared tool,
     and baking one install's crew-registry layout into it would break
     every other install. Empty/absent `agent_type`, or stdin that is
     empty/unparseable JSON, is treated as a vanilla session: emit
     NOTHING, exit 0. No directive, no degraded notice, no directory probe
     — a plain chat session should never be nagged or even see this hook
     do work.
  2. always (CLAGENTIC_DISPATCH_MODE=always):
     Ignore the payload; fire the directive on every session, exactly like
     the hook's original unconditional behavior. For installs that want
     the directive everywhere regardless of session type.
  3. off (CLAGENTIC_DISPATCH_MODE=off):
     Never fire the directive, regardless of payload. Full opt-out.

  Any other CLAGENTIC_DISPATCH_MODE value falls back to "auto".

FAIL-OPEN, VERBOSE — by contract (operator directive 2026-06-15):
  - This hook NEVER fails closed: it never blocks or aborts a session.
  - This hook NEVER fails open SILENTLY when it decides to emit the
    directive: every such run states which branch it took. If the
    directory is unreachable, the session still starts AND a loud notice
    says so, why, and what to check — so agent selection is never
    operating blind without the operator knowing. (A vanilla session that
    is gated to silence is not a failure — it is the intended no-op.)

Both directive outcomes (healthy/degraded) are emitted as SessionStart
additionalContext so they are visible to the model (and, via the
directive, to the operator who is told to surface them).

Parameterized (build-to-share): no hardcoded host beyond the documented
default. Override with CLAGENTIC_DIRECTORY_URL. Optional bearer token via
CLAGENTIC_DIRECTORY_TOKEN (auth is disabled on local installs). Dispatch
gating override via CLAGENTIC_DISPATCH_MODE (auto|always|off, see above).
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

# CLAGENTIC_DISPATCH_MODE values (see module docstring "GATING").
_MODE_AUTO = "auto"
_MODE_ALWAYS = "always"
_MODE_OFF = "off"
_VALID_MODES = frozenset({_MODE_AUTO, _MODE_ALWAYS, _MODE_OFF})


def _dispatch_mode() -> str:
    """Read CLAGENTIC_DISPATCH_MODE, defaulting to 'auto' for unset/unknown values."""
    raw = os.environ.get("CLAGENTIC_DISPATCH_MODE", "").strip().lower()
    return raw if raw in _VALID_MODES else _MODE_AUTO


def _read_payload_agent_type(stream) -> str:
    """Read the SessionStart hook JSON payload from stream, return agent_type.

    Fail-open: empty/unparseable stdin (or any other read error) yields ''
    so the caller treats it as a vanilla (silent) session rather than
    raising or falling back to firing the directive.
    """
    try:
        raw = stream.read()
        if not raw or not raw.strip():
            return ""
        payload = json.loads(raw)
        return str(payload.get("agent_type") or "").strip()
    except Exception:
        return ""


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


def _should_fire(mode: str, agent_type: str) -> bool:
    """Decide whether the dispatch directive should fire, per CLAGENTIC_DISPATCH_MODE.

    auto (default): fire only when agent_type (from the hook's own payload) is
      non-empty — i.e. this is an agent/crew-dispatched session, not a plain one.
    always: fire unconditionally (today's original, pre-gating behavior).
    off: never fire.
    """
    if mode == _MODE_ALWAYS:
        return True
    if mode == _MODE_OFF:
        return False
    return bool(agent_type)


def _run_directive() -> None:
    """Emit the healthy/degraded/error directive. Always exits 0 (fail-open)."""
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


def main() -> None:
    mode = _dispatch_mode()
    agent_type = _read_payload_agent_type(sys.stdin) if mode == _MODE_AUTO else ""
    if _should_fire(mode, agent_type):
        _run_directive()
    else:
        print(
            f"[dispatch-discipline] mode={mode} agent_type={agent_type!r} — vanilla "
            "session, staying silent",
            file=sys.stderr,
        )
    # Always exit 0 — never block a session.
    sys.exit(0)


if __name__ == "__main__":
    main()
