"""Enforce the assignment's test count and statement coverage requirements."""
import json
from pathlib import Path

passed = set()
for line in Path("test-results.jsonl").read_text(encoding="utf-8").splitlines():
    event = json.loads(line)
    name = event.get("Test", "")
    if event.get("Action") == "pass" and name and "/" not in name:
        passed.add((event["Package"], name))

covered = statements = 0
for line in Path("coverage.out").read_text(encoding="utf-8").splitlines()[1:]:
    location, count, hits = line.split()
    if "/internal/domain/" in location:
        statements += int(count)
        if int(hits) > 0:
            covered += int(count)

percent = 100 * covered / statements if statements else 0
print(f"Top-level tests passed: {len(passed)} (required >= 40)")
print(f"Domain statement coverage: {percent:.1f}% ({covered}/{statements}; required >= 70%)")
if len(passed) < 40 or percent < 70:
    raise SystemExit(1)
