#!/usr/bin/env python3
"""Forward go test -json output and require named acceptance tests to pass."""
import json
import sys


def verify(lines, required, output):
    required = set(required)
    passed = set()
    packages = set()
    completed = set()
    failures = []
    for line in lines:
        try:
            event = json.loads(line)
            if not isinstance(event, dict):
                raise ValueError("expected an event object")
        except ValueError as error:
            failures.append(f"invalid Go test event: {error}")
            continue
        if event.get("Output"):
            output.write(event["Output"])
            output.flush()
        action, package, test = (event.get(key, "") for key in ("Action", "Package", "Test"))
        if package:
            packages.add(package)
        if action == "fail":
            failures.append(f"failed: {package}/{test}")
        if test:
            selected = any(test == name or test.startswith(name + "/") for name in required)
            if selected and action == "skip":
                failures.append(f"required case skipped: {package}/{test}")
            if test in required and action == "pass":
                passed.add(test)
        elif action == "pass":
            completed.add(package)
    for test in sorted(required - passed):
        failures.append(f"required test did not pass: {test}")
    for package in sorted(packages - completed):
        failures.append(f"package did not finish successfully: {package}")
    if not packages:
        failures.append("no Go test package events received")
    return failures


if __name__ == "__main__":
    if not sys.argv[1:]:
        sys.exit("usage: check-go-test-results.py TestRequired [TestRequired ...]")
    failures = verify(sys.stdin, sys.argv[1:], sys.stdout)
    for failure in failures:
        print(f"acceptance: {failure}", file=sys.stderr)
    sys.exit(bool(failures))
