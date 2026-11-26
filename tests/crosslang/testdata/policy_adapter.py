#!/usr/bin/env python3
"""A non-Go browser adapter fixture for the policy.export wire contract."""

import json
import sys


if sys.argv[1:] != ["share", "default", "--", "policy", "export"]:
    sys.exit(2)

try:
    request = json.load(sys.stdin)
except (ValueError, UnicodeError):
    sys.exit(2)

if request != {"version": 2}:
    sys.exit(2)

json.dump(
    {
        "version": 2,
        "entries": [
            {
                "location": "python-adapter:managed",
                "level": "managed",
                "format": "json",
                "content": '{"HomepageLocation":"https://example.test"}',
            }
        ],
    },
    sys.stdout,
)
sys.stdout.write("\n")
