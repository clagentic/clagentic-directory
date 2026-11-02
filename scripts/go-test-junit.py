#!/usr/bin/env python3
"""Run `go test -json` and write a JUnit XML report.

Usage: go-test-junit.py JUNIT_PATH [go test args...]   (default package arg: ./...)

The toolchain has no JUnit output of its own and the repo takes no extra
dependency for one, so this converts the JSON event stream. The exit code is
go test's own, so a failing suite still fails the run.
"""
import json
import subprocess
import sys
import xml.etree.ElementTree as ET


def convert(events):
    """Return {(package, test): {"status", "output"}} from go test -json events."""
    results = {}
    for ev in events:
        test = ev.get("Test")
        if not test:
            continue
        rec = results.setdefault((ev.get("Package", ""), test), {"status": None, "output": []})
        action = ev.get("Action")
        if action == "output":
            rec["output"].append(ev.get("Output", ""))
        elif action in ("pass", "fail", "skip"):
            rec["status"] = action
    return results


def build_xml(results):
    root = ET.Element("testsuites")
    suites = {}
    for (pkg, test), rec in sorted(results.items()):
        if rec["status"] is None:
            continue
        suite = suites.get(pkg)
        if suite is None:
            suite = suites[pkg] = ET.SubElement(root, "testsuite", name=pkg)
        case = ET.SubElement(suite, "testcase", classname=pkg, name=test)
        if rec["status"] == "fail":
            failure = ET.SubElement(case, "failure", message="test failed")
            failure.text = "".join(rec["output"])
        elif rec["status"] == "skip":
            ET.SubElement(case, "skipped")
    for suite in suites.values():
        cases = list(suite)
        suite.set("tests", str(len(cases)))
        suite.set("failures", str(sum(1 for c in cases if c.find("failure") is not None)))
        suite.set("skipped", str(sum(1 for c in cases if c.find("skipped") is not None)))
    return ET.ElementTree(root)


def main(argv):
    if len(argv) < 2:
        print(__doc__, file=sys.stderr)
        return 2
    junit_path, go_args = argv[1], argv[2:] or ["./..."]
    proc = subprocess.run(
        ["go", "test", "-json", *go_args], stdout=subprocess.PIPE, text=True, check=False
    )
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
