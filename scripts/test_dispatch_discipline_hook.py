#!/usr/bin/env python3
"""Unit tests for dispatch-discipline-hook.py's CLAGENTIC_DISPATCH_MODE gating.

Run with: python3 -m unittest scripts.test_dispatch_discipline_hook -v
(from the repo root) or python3 scripts/test_dispatch_discipline_hook.py

Covers the three-state gate (lr-c68592):
  - auto (default): agent_type present in payload => directive fires;
    absent/empty, or empty/malformed stdin => silent.
  - always: fires regardless of agent_type.
  - off: silent regardless of agent_type.
"""

from __future__ import annotations

import importlib.util
import io
import os
import sys
import unittest
from pathlib import Path
from unittest import mock

_MODULE_PATH = Path(__file__).resolve().parent / "dispatch-discipline-hook.py"


def _load_hook_module():
    """Import dispatch-discipline-hook.py despite its hyphenated filename."""
    spec = importlib.util.spec_from_file_location("dispatch_discipline_hook", _MODULE_PATH)
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    spec.loader.exec_module(module)
    return module


hook = _load_hook_module()


class DispatchModeTests(unittest.TestCase):
    def setUp(self):
        self._env_patch = mock.patch.dict(os.environ, {}, clear=False)
        self._env_patch.start()
        os.environ.pop("CLAGENTIC_DISPATCH_MODE", None)

    def tearDown(self):
        self._env_patch.stop()

    def test_dispatch_mode_defaults_to_auto_when_unset(self):
        self.assertEqual(hook._dispatch_mode(), hook._MODE_AUTO)

    def test_dispatch_mode_unknown_value_falls_back_to_auto(self):
        os.environ["CLAGENTIC_DISPATCH_MODE"] = "bogus"
        self.assertEqual(hook._dispatch_mode(), hook._MODE_AUTO)

    def test_dispatch_mode_reads_always(self):
        os.environ["CLAGENTIC_DISPATCH_MODE"] = "always"
        self.assertEqual(hook._dispatch_mode(), hook._MODE_ALWAYS)

    def test_dispatch_mode_reads_off(self):
        os.environ["CLAGENTIC_DISPATCH_MODE"] = "off"
        self.assertEqual(hook._dispatch_mode(), hook._MODE_OFF)

    def test_dispatch_mode_case_insensitive(self):
        os.environ["CLAGENTIC_DISPATCH_MODE"] = "ALWAYS"
        self.assertEqual(hook._dispatch_mode(), hook._MODE_ALWAYS)


class PayloadAgentTypeTests(unittest.TestCase):
    def test_reads_agent_type_from_valid_payload(self):
        stream = io.StringIO('{"session_id": "abc", "agent_type": "amos"}')
        self.assertEqual(hook._read_payload_agent_type(stream), "amos")

    def test_empty_stdin_yields_empty_string(self):
        stream = io.StringIO("")
        self.assertEqual(hook._read_payload_agent_type(stream), "")

    def test_malformed_json_yields_empty_string(self):
        stream = io.StringIO("not json{{{")
        self.assertEqual(hook._read_payload_agent_type(stream), "")

    def test_missing_agent_type_field_yields_empty_string(self):
        stream = io.StringIO('{"session_id": "abc"}')
        self.assertEqual(hook._read_payload_agent_type(stream), "")

    def test_null_agent_type_field_yields_empty_string(self):
        stream = io.StringIO('{"agent_type": null}')
        self.assertEqual(hook._read_payload_agent_type(stream), "")

    def test_non_string_agent_type_is_coerced_and_read(self):
        # Defensive: harness contract is string, but don't crash on odd input.
        stream = io.StringIO('{"agent_type": 123}')
        self.assertEqual(hook._read_payload_agent_type(stream), "123")


class ShouldFireTests(unittest.TestCase):
    def test_auto_fires_when_agent_type_present(self):
        self.assertTrue(hook._should_fire(hook._MODE_AUTO, "amos"))

    def test_auto_silent_when_agent_type_absent(self):
        self.assertFalse(hook._should_fire(hook._MODE_AUTO, ""))

    def test_always_fires_even_without_agent_type(self):
        self.assertTrue(hook._should_fire(hook._MODE_ALWAYS, ""))

    def test_always_fires_with_agent_type_too(self):
        self.assertTrue(hook._should_fire(hook._MODE_ALWAYS, "amos"))

    def test_off_silent_even_with_agent_type(self):
        self.assertFalse(hook._should_fire(hook._MODE_OFF, "amos"))

    def test_off_silent_without_agent_type(self):
        self.assertFalse(hook._should_fire(hook._MODE_OFF, ""))


class MainIntegrationTests(unittest.TestCase):
    """End-to-end checks of main()'s gating via stdin + env, directive emission mocked out."""

    def setUp(self):
        self._env_patch = mock.patch.dict(os.environ, {}, clear=False)
        self._env_patch.start()
        os.environ.pop("CLAGENTIC_DISPATCH_MODE", None)

    def tearDown(self):
        self._env_patch.stop()

    def _run_main(self, stdin_text: str):
        with mock.patch.object(hook.sys, "stdin", io.StringIO(stdin_text)), \
                mock.patch.object(hook, "_run_directive") as mock_run, \
                self.assertRaises(SystemExit) as exit_ctx:
            hook.main()
        return mock_run, exit_ctx.exception.code

    def test_agent_type_present_fires_directive(self):
        mock_run, code = self._run_main('{"agent_type": "amos"}')
        mock_run.assert_called_once()
        self.assertEqual(code, 0)

    def test_agent_type_absent_stays_silent(self):
        mock_run, code = self._run_main('{"session_id": "abc"}')
        mock_run.assert_not_called()
        self.assertEqual(code, 0)

    def test_malformed_stdin_stays_silent(self):
        mock_run, code = self._run_main("{{{not json")
        mock_run.assert_not_called()
        self.assertEqual(code, 0)

    def test_empty_stdin_stays_silent(self):
        mock_run, code = self._run_main("")
        mock_run.assert_not_called()
        self.assertEqual(code, 0)

    def test_mode_always_fires_with_no_agent_type(self):
        os.environ["CLAGENTIC_DISPATCH_MODE"] = "always"
        mock_run, code = self._run_main("")
        mock_run.assert_called_once()
        self.assertEqual(code, 0)

    def test_mode_off_stays_silent_with_agent_type(self):
        os.environ["CLAGENTIC_DISPATCH_MODE"] = "off"
        mock_run, code = self._run_main('{"agent_type": "amos"}')
        mock_run.assert_not_called()
        self.assertEqual(code, 0)


if __name__ == "__main__":
    unittest.main()
