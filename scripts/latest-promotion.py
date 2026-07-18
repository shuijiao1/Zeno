#!/usr/bin/env python3
"""Decide whether a completed image tag may atomically promote `latest`.

Only exact, stable vMAJOR.MINOR.PATCH tags participate. Tags are read from
stdin so the workflow can re-fetch the complete repository tag set immediately
before promotion. Output is compatible with GitHub step outputs.
"""

import re
import sys

STABLE = re.compile(r"^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$")


def parse(tag: str):
    match = STABLE.fullmatch(tag.strip())
    if match is None:
        return None
    return tuple(map(int, match.groups()))


def decision(current: str, tags: list[str]) -> tuple[bool, str]:
    current_version = parse(current)
    stable = [(version, tag.strip()) for tag in tags if (version := parse(tag)) is not None]
    if current_version is None or not stable:
        return False, max(stable, default=((0, 0, 0), ""))[1]
    highest_version, highest_tag = max(stable)
    return current_version == highest_version, highest_tag


def main() -> int:
    if len(sys.argv) != 2:
        print("usage: latest-promotion.py <current-tag>", file=sys.stderr)
        return 2
    promote, highest = decision(sys.argv[1], sys.stdin.read().splitlines())
    print(f"promote={'true' if promote else 'false'}")
    print(f"highest={highest}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
