#!/usr/bin/env python3
"""Run the Makefile `test-json` target (`go test -json`) and write a JUnit XML report.

Usage: go-test-junit.py JUNIT_PATH [go test args...]   (default package arg: ./...)

The toolchain has no JUnit output of its own and the repo takes no extra
dependency for one, so this converts the JSON event stream. The exit code is
go test's own, so a failing suite still fails the run.
"""
import json
import subprocess
import sys
import xml.etree.ElementTree as ET
from pathlib import Path


PACKAGE_CASE = "[package]"


def convert(events):
    """Return {(package, test): {"status", "output"}} from go test -json events.

    Package-level events (no test name) are recorded under PACKAGE_CASE so a
    build failure or a package that failed outside any test is not lost.
    """
    results = {}
    for ev in events:
        pkg = ev.get("Package") or ev.get("ImportPath") or ""
        test = ev.get("Test") or PACKAGE_CASE
        rec = results.setdefault((pkg, test), {"status": None, "output": []})
        action = ev.get("Action")
        if action in ("output", "build-output"):
            rec["output"].append(ev.get("Output", ""))
        elif action in ("pass", "fail", "skip"):
            rec["status"] = action
        elif action == "build-fail":
            rec["status"] = "fail"
    return results


def _failure(case, message, output):
    failure = ET.SubElement(case, "failure", message=message)
    failure.text = "".join(output)


def build_xml(results):
    root = ET.Element("testsuites")
    suites = {}
    for (pkg, test), rec in sorted(results.items()):
        status = rec["status"]
        if test == PACKAGE_CASE and status != "fail":
            # A package that passed or was skipped needs no case of its own.
            continue
        suite = suites.get(pkg)
        if suite is None:
            suite = suites[pkg] = ET.SubElement(root, "testsuite", name=pkg)
        case = ET.SubElement(suite, "testcase", classname=pkg, name=test)
        if status == "fail":
            message = "package failed" if test == PACKAGE_CASE else "test failed"
            _failure(case, message, rec["output"])
        elif status is None:
            _failure(case, "no terminal action (test started but never finished)", rec["output"])
        elif status == "skip":
            ET.SubElement(case, "skipped")
    for suite in suites.values():
        cases = list(suite)
        suite.set("tests", str(len(cases)))
        suite.set("failures", str(sum(1 for c in cases if c.find("failure") is not None)))
        suite.set("skipped", str(sum(1 for c in cases if c.find("skipped") is not None)))
    return ET.ElementTree(root)


def run_make_test_json(go_args):
    """Run the Makefile's test-json target, the single owner of the go cache fallback."""
    repo_root = Path(__file__).resolve().parent.parent
    return subprocess.run(
        ["make", "-s", "test-json", "PKGS=" + " ".join(go_args)],
        cwd=repo_root,
        stdout=subprocess.PIPE,
        text=True,
        check=False,
    )


def main(argv, runner=run_make_test_json):
    if len(argv) < 2:
        print(__doc__, file=sys.stderr)
        return 2
    junit_path, go_args = argv[1], argv[2:] or ["./..."]
    proc = runner(go_args)
    events = []
    for line in proc.stdout.splitlines():
        try:
            events.append(json.loads(line))
        except json.JSONDecodeError:
            sys.stderr.write(line + "\n")
    build_xml(convert(events)).write(junit_path, encoding="utf-8", xml_declaration=True)
    return proc.returncode


if __name__ == "__main__":
    sys.exit(main(sys.argv))
