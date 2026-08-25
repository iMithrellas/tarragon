#!/usr/bin/env python3
"""System power and session controls for Tarragon."""

import argparse
import json
import subprocess
import sys


COMMANDS = [
    {
        "id": "lock",
        "label": "Lock Session",
        "description": "Lock the current desktop session",
        "keywords": "screen secure",
        "icon": "system-lock-screen",
        "args": ["loginctl", "lock-session"],
    },
    {
        "id": "suspend",
        "label": "Suspend Computer",
        "description": "Suspend the computer to RAM",
        "keywords": "sleep standby",
        "icon": "system-suspend",
        "args": ["systemctl", "--no-block", "suspend"],
    },
    {
        "id": "hibernate",
        "label": "Hibernate Computer",
        "description": "Save the session to disk and power off",
        "keywords": "sleep disk",
        "icon": "system-suspend-hibernate",
        "args": ["systemctl", "--no-block", "hibernate"],
    },
    {
        "id": "suspend-then-hibernate",
        "label": "Suspend, Then Hibernate",
        "description": "Suspend now and hibernate later",
        "keywords": "sleep battery",
        "icon": "system-suspend-hibernate",
        "args": ["systemctl", "--no-block", "suspend-then-hibernate"],
    },
    {
        "id": "reboot",
        "label": "Restart Computer",
        "description": "Reboot the operating system",
        "keywords": "restart",
        "icon": "system-reboot",
        "args": ["systemctl", "--no-block", "reboot"],
    },
    {
        "id": "poweroff",
        "label": "Power Off Computer",
        "description": "Shut down and turn off the computer",
        "keywords": "shutdown halt",
        "icon": "system-shutdown",
        "args": ["systemctl", "--no-block", "poweroff"],
    },
]

COMMANDS_BY_ID = {command["id"]: command for command in COMMANDS}


def _result_for(command: dict) -> dict:
    return {
        "id": command["id"],
        "label": command["label"],
        "description": command["description"],
        "icon": command["icon"],
        "category": "System",
        "actions": [
            {
                "name": "run",
                "default": True,
                "description": command["label"],
            }
        ],
    }


def process(text: str) -> list[dict]:
    query = text.strip().lower()
    if not query:
        return [_result_for(command) for command in COMMANDS]

    results = []
    for command in COMMANDS:
        searchable = " ".join(
            (
                command["id"],
                command["label"],
                command["description"],
                command["keywords"],
            )
        ).lower()
        if query in searchable:
            results.append(_result_for(command))
    return results


def run_command(result_id: str, action: str = "") -> tuple[bool, str]:
    if action and action != "run":
        return False, f"unsupported action: {action}"

    command = COMMANDS_BY_ID.get(result_id)
    if command is None:
        return False, f"unknown command: {result_id}"

    try:
        proc = subprocess.run(
            command["args"],
            capture_output=True,
            text=True,
            timeout=10,
        )
    except FileNotFoundError:
        return False, f"{command['args'][0]}: not found on PATH"
    except subprocess.TimeoutExpired:
        return False, f"{command['label']}: command timed out"

    if proc.returncode != 0:
        return False, proc.stderr.strip() or proc.stdout.strip() or command["label"]
    return True, proc.stdout.strip() or f"{command['label']} requested"


def main(argv=None) -> int:
    if argv is None:
        argv = sys.argv[1:]

    if len(argv) >= 2 and argv[:2] == ["tarragon", "query"]:
        text = " ".join(argv[2:])
        print(json.dumps({"results": process(text)}))
        return 0

    if len(argv) >= 2 and argv[:2] == ["tarragon", "select"]:
        if len(argv) < 3:
            print("missing result id", file=sys.stderr)
            return 2
        action = argv[3] if len(argv) > 3 else ""
        success, message = run_command(argv[2], action)
        print(json.dumps({"success": success, "message": message}))
        if not success:
            print(message, file=sys.stderr)
        return 0 if success else 1

    parser = argparse.ArgumentParser(description="Tarragon System Control Plugin")
    parser.add_argument("--once", metavar="TEXT", help="Process once and print JSON")
    args = parser.parse_args(argv)

    if args.once is not None:
        print(json.dumps({"results": process(args.once)}))
        return 0

    parser.print_help()
    return 2


if __name__ == "__main__":
    raise SystemExit(main())
