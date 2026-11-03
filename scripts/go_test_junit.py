#!/usr/bin/env python3
"""Run the Go tests through the Makefile and write the result as a JUnit XML file.

`clagentic-loadout test run` reads a JUnit report, and `go test` writes none.
This script is the bridge: it asks the Makefile (the single owner of the `go`
invocation and of the cache fallback for a stripped environment) to run
`go test -json`, then converts that event stream to JUnit.

    go_test_junit.py --junit PATH full
    go_test_junit.py --junit PATH files PATH...

`full` runs every package. `files` maps each changed file to its package
directory: a `.go` file (a `_test.go` included) selects the directory that holds
it, directories are deduplicated, and every other file is skipped. When no Go
file is left the run is an empty, passing suite and `go` is not started.

Exit codes (a loadout run counts any non-zero code as a failed run):

    0    every test passed, or `files` selected no Go package
    2    `go test` failed: a failing test, a package that failed, or a build
         failure. This is the code `make` returns for a failed recipe, passed
         through unchanged. The JUnit report holds at least one failing case.
    1    `make` returned success but the event stream holds a failing case
    64   usage error: a path that cannot be passed to `go test` safely
    other  the code `make` returned, when it is neither 0 nor 2 (for example
         127 when `make` is not installed); the report holds a failing case

The report is written on every path except exit 64.
"""

from __future__ import annotations

import argparse
import json
import os
import posixpath
import re
import subprocess
import sys
import tempfile
import xml.etree.ElementTree as ET
from dataclasses import dataclass, field
from pathlib import Path
from typing import Iterable, Sequence

EXIT_OK = 0
EXIT_STREAM_FAILURE = 1
EXIT_USAGE = 64

# Directory names are handed to make as a package list and split by the shell
# there; this alphabet has no character the shell or make treats specially, so
# the split cannot change what the list means.
_SAFE_DIR = re.compile(r"\A[A-Za-z0-9_./-]+\Z")
_XML_ILLEGAL = re.compile("[\x00-\x08\x0b\x0c\x0e-\x1f￾￿]")
_MAX_CASE_OUTPUT = 16 * 1024

SYNTHETIC_PACKAGE = "[package]"
SYNTHETIC_BUILD = "[build]"
SYNTHETIC_RUN = "[go test]"


class UsageError(ValueError):
    """A changed path that cannot be turned into a safe package argument."""


def packages_for_files(paths: Iterable[str]) -> list[str]:
    """Map changed files to sorted, unique `./dir` package arguments.

    Only `.go` files select a package. A file under `testdata`, or under a
    directory whose name starts with `_` or `.`, is not part of any package the
    go tool builds, so it is skipped like a non-Go file.
    """
    selected: set[str] = set()
    for raw in paths:
        path = posixpath.normpath(raw.replace(os.sep, "/"))
        if not path.endswith(".go"):
            continue
        if path.startswith("/") or path == ".." or path.startswith("../"):
            raise UsageError(f"{raw!r} is outside the repository")
        directory = posixpath.dirname(path) or "."
        parts = [p for p in directory.split("/") if p != "."]
        if any(p == "testdata" or p.startswith(("_", ".")) for p in parts):
            continue
        if not _SAFE_DIR.match(directory):
            raise UsageError(
                f"{raw!r}: directory {directory!r} has a character outside "
                "[A-Za-z0-9_./-] and cannot be passed to go test safely"
            )
        selected.add("." if directory == "." else f"./{directory}")
    return sorted(selected)


@dataclass
class _Case:
    package: str
    name: str
    output: list[str] = field(default_factory=list)
    action: str | None = None
    elapsed: float = 0.0


@dataclass
class _Package:
    name: str
    output: list[str] = field(default_factory=list)
    action: str | None = None
    failed_build: str | None = None
    elapsed: float = 0.0


@dataclass
class Events:
    packages: dict[str, _Package] = field(default_factory=dict)
    cases: dict[tuple[str, str], _Case] = field(default_factory=dict)
    build_output: dict[str, list[str]] = field(default_factory=dict)
    build_failed: list[str] = field(default_factory=list)
    stray: list[str] = field(default_factory=list)


def parse_events(lines: Iterable[str]) -> Events:
    """Fold a `go test -json` stream into packages, test cases and build output."""
    ev = Events()
    for line in lines:
        text = line.rstrip("\n")
        if not text.strip():
            continue
        try:
            obj = json.loads(text)
        except json.JSONDecodeError:
            ev.stray.append(text)
            continue
        if not isinstance(obj, dict):
            ev.stray.append(text)
            continue
        action = obj.get("Action")
        output = obj.get("Output", "")
        import_path = obj.get("ImportPath")
        if import_path is not None:
            if action == "build-output":
                ev.build_output.setdefault(import_path, []).append(output)
            elif action == "build-fail" and import_path not in ev.build_failed:
                ev.build_failed.append(import_path)
            continue
        package = obj.get("Package")
        if not package:
            ev.stray.append(text)
            continue
        pkg = ev.packages.setdefault(package, _Package(package))
        test = obj.get("Test")
        if test:
            case = ev.cases.setdefault((package, test), _Case(package, test))
            target: _Case | _Package = case
        else:
            target = pkg
        if action == "output":
            target.output.append(output)
        elif action in ("pass", "fail", "skip"):
            target.action = action
            target.elapsed = float(obj.get("Elapsed") or 0.0)
            if not test and obj.get("FailedBuild"):
                pkg.failed_build = obj["FailedBuild"]
    return ev


def _clean(text: str) -> str:
    text = _XML_ILLEGAL.sub("?", text)
    if len(text) > _MAX_CASE_OUTPUT:
        text = "...[truncated]\n" + text[-_MAX_CASE_OUTPUT:]
    return text


def _add_case(
    suite: ET.Element, classname: str, name: str, elapsed: float, outcome: str, message: str, output: str
) -> None:
    case = ET.SubElement(
        suite, "testcase", classname=classname, name=name, time=f"{elapsed:.3f}"
    )
    if outcome == "skip":
        ET.SubElement(case, "skipped")
    elif outcome == "fail":
        failure = ET.SubElement(case, "failure", message=_clean(message))
        failure.text = _clean(output)


def build_junit(ev: Events, extra_failure: str | None = None) -> tuple[ET.ElementTree, int]:
    """JUnit for the events; returns the tree and the number of failing cases.

    Failures that go test reports only above the test level become cases of
    their own, so a report never reads as clean when the run was not: a build
    failure, a package that failed with no failing test (a panic outside a test,
    TestMain exiting non-zero), and any package or test with no terminal event.
    """
    suite = ET.Element("testsuite", name="go test")
    failing = 0

    def fail(classname: str, name: str, elapsed: float, message: str, output: str) -> None:
        nonlocal failing
        failing += 1
        _add_case(suite, classname, name, elapsed, "fail", message, output)

    failing_packages: set[str] = set()
    for (package, test), case in ev.cases.items():
        text = "".join(case.output)
        if case.action == "pass":
            _add_case(suite, package, test, case.elapsed, "pass", "", "")
        elif case.action == "skip":
            _add_case(suite, package, test, case.elapsed, "skip", "", "")
        elif case.action == "fail":
            failing_packages.add(package)
            fail(package, test, case.elapsed, "test failed", text)
        else:
            failing_packages.add(package)
            fail(package, test, case.elapsed, "no terminal event: the test did not finish", text)

    claimed_builds: set[str] = set()
    for name, pkg in ev.packages.items():
        if pkg.failed_build:
            claimed_builds.add(pkg.failed_build)
            built = "".join(ev.build_output.get(pkg.failed_build, []))
            fail(name, SYNTHETIC_BUILD, pkg.elapsed, "build failed", built or "".join(pkg.output))
        elif pkg.action is None:
            fail(
                name, SYNTHETIC_PACKAGE, pkg.elapsed,
                "no terminal event: the package did not finish", "".join(pkg.output),
            )
        elif pkg.action == "fail" and name not in failing_packages:
            fail(name, SYNTHETIC_PACKAGE, pkg.elapsed, "package failed", "".join(pkg.output))
    for import_path in ev.build_failed:
        if import_path not in claimed_builds:
            fail(
                import_path, SYNTHETIC_BUILD, 0.0, "build failed",
                "".join(ev.build_output.get(import_path, [])),
            )
    if extra_failure is not None and failing == 0:
        fail("go test", SYNTHETIC_RUN, 0.0, "go test failed without a failing case", extra_failure)

    suite.set("tests", str(len(suite)))
    suite.set("failures", str(failing))
    return ET.ElementTree(_wrap(suite)), failing


def _wrap(suite: ET.Element) -> ET.Element:
    root = ET.Element("testsuites")
    root.append(suite)
    return root


def write_junit(tree: ET.ElementTree, path: Path) -> None:
    descriptor = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o644)
    with os.fdopen(descriptor, "wb") as handle:
        tree.write(handle, encoding="utf-8", xml_declaration=True)


ALL_PACKAGES = ["./..."]


def run_make(
    make: str, root: Path, json_path: Path, packages: Sequence[str]
) -> tuple[int, str]:
    """Run the Makefile's test-json target; returns (make exit code, stderr text)."""
    # PKGS is always passed: a command-line variable overrides both the
    # environment and the Makefile's `?=`, so an inherited PKGS cannot narrow a run.
    argv = [
        make, "-s", "-C", str(root), "test-json",
        f"GOTEST_JSON={json_path}", "PKGS=" + " ".join(packages),
    ]
    try:
        proc = subprocess.run(
            argv, stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL,
            stderr=subprocess.PIPE, check=False,
        )
    except OSError as exc:
        return 127, f"cannot run {make!r}: {exc}"
    stderr = proc.stderr.decode("utf-8", errors="replace")
    sys.stderr.write(stderr)
    return proc.returncode, stderr


def run(make: str, root: Path, junit_path: Path, packages: Sequence[str] | None) -> int:
    """Run go test via make and write the report. `packages` None means all."""
    if packages is not None and not packages:
        write_junit(ET.ElementTree(_wrap(ET.Element("testsuite", name="go test", tests="0"))), junit_path)
        print("no Go package selected; nothing to run", file=sys.stderr)
        return EXIT_OK
    with tempfile.TemporaryDirectory(prefix="go-test-json-") as scratch:
        json_path = Path(scratch) / "events.jsonl"
        code, stderr = run_make(make, root, json_path, ALL_PACKAGES if packages is None else packages)
        try:
            with open(json_path, encoding="utf-8", errors="replace") as handle:
                ev = parse_events(handle)
        except OSError:
            ev = Events()
    extra = None
    if code != 0:
        tail = "\n".join((ev.stray + stderr.splitlines())[-40:])
        extra = f"make exited {code}\n{tail}"
    tree, failing = build_junit(ev, extra)
    write_junit(tree, junit_path)
    if code != 0:
        return code
    return EXIT_STREAM_FAILURE if failing else EXIT_OK


def main(argv: Sequence[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--junit", required=True, type=Path, help="where to write the JUnit XML report")
    parser.add_argument("--make", default="make", help="the make program (default: make)")
    modes = parser.add_subparsers(dest="mode", required=True)
    modes.add_parser("full", help="test every package")
    files = modes.add_parser("files", help="test the packages holding the given files")
    files.add_argument("paths", nargs="+", metavar="PATH")
    try:
        args = parser.parse_args(argv)
    except SystemExit as exc:
        # argparse exits 2 on a usage error; 2 is reserved here for a go test failure.
        return EXIT_OK if exc.code == 0 else EXIT_USAGE
    root = Path(__file__).resolve().parent.parent
    if args.mode == "full":
        return run(args.make, root, args.junit, None)
    try:
        packages = packages_for_files(args.paths)
    except UsageError as exc:
        print(f"go_test_junit: {exc}", file=sys.stderr)
        return EXIT_USAGE
    return run(args.make, root, args.junit, packages)


if __name__ == "__main__":
    sys.exit(main())
