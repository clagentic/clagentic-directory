#!/usr/bin/env python3
"""Tests for go-test-junit.py: canned `go test -json` streams to JUnit XML.

Run from the repo root: python3 -m unittest scripts.test_go_test_junit -v
"""

from __future__ import annotations

import importlib.util
import json
import os
import subprocess
import tempfile
import unittest
import xml.etree.ElementTree as ET
from pathlib import Path

_MODULE_PATH = Path(__file__).resolve().parent / "go-test-junit.py"


def _load():
    spec = importlib.util.spec_from_file_location("go_test_junit", _MODULE_PATH)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


junit = _load()

EVENTS = [
    # pkg/ok: one pass.
    {"Action": "run", "Package": "pkg/ok", "Test": "TestPass"},
    {"Action": "pass", "Package": "pkg/ok", "Test": "TestPass"},
    {"Action": "pass", "Package": "pkg/ok"},
    # pkg/bad: one fail, one that started and never finished, package fails.
    {"Action": "run", "Package": "pkg/bad", "Test": "TestFail"},
    {"Action": "output", "Package": "pkg/bad", "Test": "TestFail", "Output": "boom\n"},
    {"Action": "fail", "Package": "pkg/bad", "Test": "TestFail"},
    {"Action": "run", "Package": "pkg/bad", "Test": "TestHang"},
    {"Action": "fail", "Package": "pkg/bad"},
    # pkg/broken: build failure, no tests at all.
    {"Action": "build-output", "ImportPath": "pkg/broken", "Output": "undefined: x\n"},
    {"Action": "build-fail", "ImportPath": "pkg/broken"},
    {"Action": "output", "Package": "pkg/broken", "Output": "FAIL pkg/broken [build failed]\n"},
    {"Action": "fail", "Package": "pkg/broken"},
]


def _fake_runner(returncode):
    def runner(_args):
        out = "\n".join(json.dumps(e) for e in EVENTS) + "\nnot json\n"
        return subprocess.CompletedProcess(args=[], returncode=returncode, stdout=out)

    return runner


class ConverterTest(unittest.TestCase):
    def _run(self, returncode):
        with tempfile.TemporaryDirectory() as d:
            path = os.path.join(d, "out.xml")
            rc = junit.main(["x", path], runner=_fake_runner(returncode))
            return rc, ET.parse(path).getroot()

    def _case(self, root, pkg, name):
        for suite in root.findall("testsuite"):
            for case in suite.findall("testcase"):
                if suite.get("name") == pkg and case.get("name") == name:
                    return case
        self.fail("missing testcase %s/%s" % (pkg, name))

    def test_junit_and_exit_code(self):
        rc, root = self._run(1)
        self.assertEqual(rc, 1)
        ok = self._case(root, "pkg/ok", "TestPass")
        self.assertIsNone(ok.find("failure"))
        fail = self._case(root, "pkg/bad", "TestFail")
        self.assertIn("boom", fail.find("failure").text)
        hang = self._case(root, "pkg/bad", "TestHang")
        self.assertIn("no terminal action", hang.find("failure").get("message"))
        self.assertIsNotNone(self._case(root, "pkg/bad", "[package]").find("failure"))
        build = self._case(root, "pkg/broken", "[package]")
        self.assertIn("undefined: x", build.find("failure").text)
        self.assertIsNone(root.find("testsuite[@name='pkg/ok']/testcase[@name='[package]']"))

    def test_exit_code_propagates(self):
        rc, _ = self._run(0)
        self.assertEqual(rc, 0)


if __name__ == "__main__":
    unittest.main()
