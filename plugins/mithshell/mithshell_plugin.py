#!/usr/bin/env python3
"""
Mithshell control plugin for Tarragon.

Shells out to the `mithshell` CLI for a fixed set of safe control subcommands
(dashboard, weather, notifications, lock screen, theme). Does not reimplement
mithshell's own IPC protocol, and never invokes subcommands that start a
daemon, query the search frontend, or are dev-only.
"""

import argparse
import json
import logging
import os
import signal
import socket as sock_mod
import subprocess
import sys

PLUGIN_NAME = os.environ.get("TARRAGON_PLUGIN_NAME", "Mithshell Control")
logging.basicConfig(
    level=logging.INFO,
    format=f"[PLUGIN: {PLUGIN_NAME}] %(levelname)s %(message)s",
    handlers=[logging.StreamHandler(sys.stderr)],
)
logger = logging.getLogger(__name__)

# Fixed command table. Deliberately excludes commands that are unsafe,
# self-referential, diagnostic-only, or redundant when leaving Tarragon.
COMMANDS = [
    {
        "id": "toggle",
        "label": "Toggle Dashboard",
        "description": "Toggle the mithshell dashboard",
        "args": ["toggle"],
    },
    {
        "id": "open",
        "label": "Open Dashboard",
        "description": "Open the mithshell dashboard",
        "args": ["open"],
    },
    {
        "id": "weather",
        "label": "Open Weather",
        "description": "Open the weather forecast on the focused monitor",
        "args": ["weather"],
    },
    {
        "id": "lock",
        "label": "Lock Session",
        "description": "Lock the session behind a PAM password prompt",
        "args": ["lock"],
    },
    {
        "id": "inhibit",
        "label": "Toggle Do Not Disturb",
        "description": "Silence or restore notifications",
        "args": ["inhibit"],
        "keep_open": True,
    },
    {
        "id": "inhibit-1h",
        "label": "Do Not Disturb for 1 Hour",
        "description": "Silence notifications for one hour",
        "args": ["inhibit", "1h"],
        "keep_open": True,
    },
    {
        "id": "reload",
        "label": "Reload Config",
        "description": "Reload the mithshell TOML configuration",
        "args": ["reload"],
        "keep_open": True,
    },
    {
        "id": "theme-dark",
        "label": "Dark Theme",
        "description": "Switch mithshell to dark mode",
        "args": ["theme", "mode", "dark"],
        "keep_open": True,
    },
    {
        "id": "theme-light",
        "label": "Light Theme",
        "description": "Switch mithshell to light mode",
        "args": ["theme", "mode", "light"],
        "keep_open": True,
    },
    {
        "id": "theme-reset",
        "label": "Reset Theme",
        "description": "Remove the persisted theme override",
        "args": ["theme", "reset"],
        "keep_open": True,
    },
]

COMMANDS_BY_ID = {cmd["id"]: cmd for cmd in COMMANDS}


def _result_for(cmd: dict) -> dict:
    action = {
        "name": "run",
        "default": True,
        "description": cmd["label"],
    }
    if cmd.get("keep_open"):
        action["type"] = "keep_open"

    return {
        "id": cmd["id"],
        "label": cmd["label"],
        "description": cmd["description"],
        "category": "mithshell",
        "actions": [action],
    }


def process(text: str):
    query = text.strip().lower()
    if not query:
        # Prefix entered with no further text yet (e.g. just "@ms"): show
        # every available command immediately instead of waiting for the
        # user to start typing a filter.
        return [_result_for(cmd) for cmd in COMMANDS]

    results = []
    for cmd in COMMANDS:
        haystack = f"{cmd['id']} {cmd['label']} {cmd['description']}".lower()
        if query in haystack:
            results.append(_result_for(cmd))
    return results


def run_command(result_id: str):
    cmd = COMMANDS_BY_ID.get(result_id)
    if cmd is None:
        return False, f"unknown command: {result_id}"

    try:
        proc = subprocess.run(
            ["mithshell", *cmd["args"]],
            capture_output=True,
            text=True,
            timeout=10,
        )
    except FileNotFoundError:
        return False, "mithshell: not found on PATH"
    except subprocess.TimeoutExpired:
        return False, "mithshell: command timed out"

    success = proc.returncode == 0
    if success:
        message = proc.stdout.strip() or f"{cmd['label']} requested"
    else:
        message = proc.stderr.strip() or proc.stdout.strip()
    return success, message


def run_daemon():
    logger.info("initializing")
    endpoint = os.environ.get("TARRAGON_PLUGINS_ENDPOINT")

    if not endpoint:
        logger.info("started successfully; idle mode")
        signal.pause()
        return 0

    s = sock_mod.socket(sock_mod.AF_UNIX, sock_mod.SOCK_STREAM)
    import time as _time

    for attempt in range(20):
        try:
            s.connect(endpoint)
            break
        except (ConnectionRefusedError, FileNotFoundError):
            if attempt == 19:
                logger.error("could not connect to %s after retries", endpoint)
                return 1
            _time.sleep(0.1)

    s.sendall(json.dumps({"type": "hello", "name": PLUGIN_NAME}).encode() + b"\n")
    logger.info("connected to %s", endpoint)

    f = s.makefile("r")
    stop = False
    signal.signal(signal.SIGTERM, lambda *_: globals().update(stop=True))
    signal.signal(signal.SIGINT, lambda *_: globals().update(stop=True))

    while not stop:
        try:
            line = f.readline()
            if not line:
                break
            msg = json.loads(line)
        except Exception as e:
            logger.error("recv error: %s", e)
            break

        qid = msg.get("query_id", "")
        typ = msg.get("type")

        if typ == "request":
            text = msg.get("text", "")
            logger.info("request qid=%s: %s", qid, text)
            results = process(text)
            resp = {
                "type": "response",
                "query_id": qid,
                "data": {
                    "results": results,
                },
            }
            s.sendall(json.dumps(resp).encode() + b"\n")
            logger.info("response sent qid=%s", qid)
        elif typ == "select":
            result_id = msg.get("result_id", "")
            action = msg.get("action", "")
            logger.info("select qid=%s result_id=%s action=%s", qid, result_id, action)
            if action and action != "run":
                success, message = False, f"unsupported action: {action}"
            else:
                success, message = run_command(result_id)
            resp = {
                "type": "select_response",
                "success": success,
                "message": message,
            }
            s.sendall(json.dumps(resp).encode() + b"\n")

    logger.info("exiting")
    return 0


def main(argv=None):
    parser = argparse.ArgumentParser(description="Tarragon Mithshell Control Plugin")
    parser.add_argument("--once", metavar="TEXT", help="Process once and print JSON")
    args = parser.parse_args(argv)

    if args.once is not None:
        logger.info("request: %s", args.once)
        results = process(args.once)
        print(json.dumps({"results": results}))
        return 0

    return run_daemon()


if __name__ == "__main__":
    raise SystemExit(main())
