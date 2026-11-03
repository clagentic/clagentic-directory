#!/usr/bin/env python3
"""Tests for go_test_junit.py: file-to-package mapping, JUnit completeness and
the exit-code contract.

Run with: python3 -m unittest discover -s scripts -p 'test_*.py'

scripts/testdata/go-test-events.jsonl is a real `go test -json` capture of five
packages: a build failure, a failing subtest, a skip, a panic inside a test, and
a TestMain that exits 3.
"""

from __future__ import annotations

import importlib.util
import os
import stat
import sys
import tempfile
import unittest
from unittest import mock
import xml.etree.ElementTree as ET
from pathlib import Path

_HERE = Path(__file__).resolve().parent
_FIXTURE = _HERE / "testdata" / "go-test-events.jsonl"


def _load():
    spec = importlib.util.spec_from_file_location("go_test_junit", _HERE / "go_test_junit.py")
    module = importlib.util.module_from_spec(spec)
    sys.modules["go_test_junit"] = module
    spec.loader.exec_module(module)
    return module


g = _load()


def _failing(tree: ET.ElementTree) -> set[tuple[str, str]]:
    return {
        (c.get("classname"), c.get("name"))
        for c in tree.getroot().iter("testcase")
        if c.find("failure") is not None
    }


class PackagesForFilesTest(unittest.TestCase):
    def test_test_file_alone_selects_its_package(self):
        self.assertEqual(g.packages_for_files(["internal/update/git_test.go"]), ["./internal/update"])

    def test_directories_are_deduplicated_and_sorted(self):
        got = g.packages_for_files(
            ["internal/update/git_test.go", "internal/update/values.go", "cmd/x/main.go", "internal/api/a.go"]
        )
        self.assertEqual(got, ["./cmd/x", "./internal/api", "./internal/update"])

    def test_non_go_files_are_skipped(self):
        self.assertEqual(g.packages_for_files(["README.md", "go.mod", "Makefile", "docs/a.yaml"]), [])

    def test_root_package(self):
        self.assertEqual(g.packages_for_files(["main.go"]), ["."])

    def test_testdata_and_hidden_directories_are_skipped(self):
        self.assertEqual(g.packages_for_files(["internal/x/testdata/a.go", "_x/a.go", ".hid/a.go"]), [])

    def test_unsafe_directory_is_refused_not_split(self):
        with self.assertRaises(g.UsageError):
            g.packages_for_files(["my dir/a.go"])
        with self.assertRaises(g.UsageError):
            g.packages_for_files(["a;b/a.go"])

    def test_path_outside_repository_is_refused(self):
        with self.assertRaises(g.UsageError):
            g.packages_for_files(["../x/a.go"])


class BuildJunitTest(unittest.TestCase):
    def _tree(self, lines, extra=None):
        return g.build_junit(g.parse_events(lines), extra)

    def test_real_capture(self):
        tree, failing = self._tree(_FIXTURE.read_text().splitlines())
        self.assertEqual(
            _failing(tree),
            {
                ("cap/a", "[build]"),
                ("cap/b", "TestB"),
                ("cap/b", "TestB/sub"),
                ("cap/c", "TestC"),
                ("cap/d", "[package]"),
            },
        )
        self.assertEqual(failing, 5)
        cases = {(c.get("classname"), c.get("name")): c for c in tree.getroot().iter("testcase")}
        self.assertIsNotNone(cases[("cap/b", "TestS")].find("skipped"))
        self.assertIsNone(cases[("cap/b", "TestP")].find("failure"))
        build = cases[("cap/a", "[build]")].find("failure")
        self.assertIn("undefined: undefinedX", build.text)
        self.assertIn("exit status 3", cases[("cap/d", "[package]")].find("failure").text)

    def test_test_without_terminal_event_fails(self):
        lines = [
            '{"Action":"start","Package":"p"}',
            '{"Action":"run","Package":"p","Test":"TestHang"}',
            '{"Action":"output","Package":"p","Test":"TestHang","Output":"=== RUN   TestHang\\n"}',
        ]
        tree, failing = self._tree(lines)
        self.assertIn(("p", "TestHang"), _failing(tree))
        self.assertIn(("p", "[package]"), _failing(tree))
        self.assertEqual(failing, 2)

    def test_build_failure_without_package_event_fails(self):
        lines = [
            '{"ImportPath":"p [p.test]","Action":"build-output","Output":"bad\\n"}',
            '{"ImportPath":"p [p.test]","Action":"build-fail"}',
        ]
        tree, _ = self._tree(lines)
        self.assertEqual(_failing(tree), {("p [p.test]", "[build]")})

    def test_passing_run_has_no_failures(self):
        lines = [
            '{"Action":"run","Package":"p","Test":"T"}',
            '{"Action":"pass","Package":"p","Test":"T","Elapsed":0.1}',
            '{"Action":"pass","Package":"p","Elapsed":0.2}',
        ]
        tree, failing = self._tree(lines)
        self.assertEqual(failing, 0)
        self.assertEqual(len(list(tree.getroot().iter("testcase"))), 1)

    def test_nonzero_make_with_no_case_adds_failing_case(self):
        tree, failing = self._tree(["go: cannot find main module"], extra="make exited 2")
        self.assertEqual(_failing(tree), {("go test", "[go test]")})
        self.assertEqual(failing, 1)

    def test_control_characters_do_not_break_the_xml(self):
        lines = [
            '{"Action":"run","Package":"p","Test":"T"}',
            '{"Action":"output","Package":"p","Test":"T","Output":"a\\u0001b\\n"}',
            '{"Action":"fail","Package":"p","Test":"T"}',
            '{"Action":"fail","Package":"p"}',
        ]
        tree, _ = self._tree(lines)
        with tempfile.TemporaryDirectory() as d:
            out = Path(d) / "j.xml"
            g.write_junit(tree, out)
            ET.parse(out)


class ExitContractTest(unittest.TestCase):
    """Drives main() with a fake make that serves the captured event stream."""

    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.dir = Path(self._tmp.name)
        self.junit = self.dir / "out.xml"

    def _fake_make(self, events: Path | None, code: int) -> str:
        script = self.dir / "make"
        copy = f'cp "{events}" "$out"' if events else ":"
        script.write_text(
            "#!/bin/sh\n"
            'for a in "$@"; do case "$a" in GOTEST_JSON=*) out="${a#GOTEST_JSON=}";; '
            'PKGS=*) echo "$a" > "$0.pkgs";; esac; done\n'
            f"{copy}\nexit {code}\n"
        )
        script.chmod(script.stat().st_mode | stat.S_IXUSR)
        return str(script)

    def _run(self, make: str, *mode: str) -> int:
        return g.main(["--junit", str(self.junit), "--make", make, *mode])

    def test_failure_exits_2_and_reports_failing_cases(self):
        code = self._run(self._fake_make(_FIXTURE, 2), "full")
        self.assertEqual(code, 2)
        self.assertEqual(len(_failing(ET.parse(self.junit))), 5)

    def test_success_exits_0(self):
        events = self.dir / "ok.jsonl"
        events.write_text('{"Action":"pass","Package":"p","Elapsed":0.1}\n')
        self.assertEqual(self._run(self._fake_make(events, 0), "full"), 0)
        self.assertEqual(_failing(ET.parse(self.junit)), set())

    def test_make_failure_with_no_events_still_reports_a_failing_case(self):
        code = self._run(self._fake_make(None, 2), "full")
        self.assertEqual(code, 2)
        self.assertEqual(_failing(ET.parse(self.junit)), {("go test", "[go test]")})

    def test_make_not_installed_exits_127_with_report(self):
        code = self._run(str(self.dir / "absent-make"), "full")
        self.assertEqual(code, 127)
        self.assertEqual(len(_failing(ET.parse(self.junit))), 1)

    def test_success_exit_with_failing_event_exits_1(self):
        self.assertEqual(self._run(self._fake_make(_FIXTURE, 0), "full"), 1)

    def test_files_passes_unique_packages_to_make(self):
        events = self.dir / "ok.jsonl"
        events.write_text('{"Action":"pass","Package":"p","Elapsed":0.1}\n')
        make = self._fake_make(events, 0)
        code = self._run(make, "files", "internal/update/a_test.go", "internal/update/b.go", "README.md")
        self.assertEqual(code, 0)
        self.assertEqual((self.dir / "make.pkgs").read_text().strip(), "PKGS=./internal/update")

    def test_full_passes_all_packages_despite_inherited_pkgs(self):
        events = self.dir / "ok.jsonl"
        events.write_text('{"Action":"pass","Package":"p","Elapsed":0.1}\n')
        make = self._fake_make(events, 0)
        with mock.patch.dict(os.environ, {"PKGS": "./internal/api"}):
            self.assertEqual(self._run(make, "full"), 0)
        self.assertEqual((self.dir / "make.pkgs").read_text().strip(), "PKGS=./...")

    def test_files_with_only_non_go_paths_is_a_passing_noop(self):
        make = self._fake_make(_FIXTURE, 2)
        self.assertEqual(self._run(make, "files", "README.md"), 0)
        self.assertFalse((self.dir / "make.pkgs").exists())
        self.assertEqual(list(ET.parse(self.junit).getroot().iter("testcase")), [])

    def test_unsafe_path_exits_64_without_running_make(self):
        make = self._fake_make(_FIXTURE, 2)
        self.assertEqual(self._run(make, "files", "a b/x.go"), 64)
        self.assertFalse(self.junit.exists())

    def test_usage_error_exits_64_not_argparse_2(self):
        self.assertEqual(g.main(["--junit", str(self.junit)]), 64)


if __name__ == "__main__":
    unittest.main()
