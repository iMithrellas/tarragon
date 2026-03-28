#!/usr/bin/env python3
"""
Template Python plugin for Tarragon.
Exposes process(text) returning 3 randomly transposed variants.
CLI: --once "text" for testing.
"""

import argparse
import json
import logging
import os
import random
import socket as sock_mod
import signal
import sys

# Setup logging
PLUGIN_NAME = os.environ.get("TARRAGON_PLUGIN_NAME", "template_python")
logging.basicConfig(
    level=logging.INFO,
    format=f"[PLUGIN: {PLUGIN_NAME}] %(levelname)s %(message)s",
    handlers=[logging.StreamHandler(sys.stderr)],
)
logger = logging.getLogger(__name__)

# Transform functions, just for demonstration
TRANSFORMS = [
    lambda s: s[::-1],  # reverse
    lambda s: s.upper(),
    lambda s: s.title(),
    lambda s: s.translate(
        str.maketrans(
            "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz",
            "NOPQRSTUVWXYZABCDEFGHIJKLMnopqrstuvwxyzabcdefghijklm",
        )
    ),  # rot13
    lambda s: "".join(sorted(s)),
    lambda s: "".join(random.sample(s, len(s))),
    lambda s: "".join(
        c.upper() if i % 2 else c.lower() for i, c in enumerate(s) if c.isalpha()
    ),
    lambda s: "".join(c for c in s if c.lower() not in "aeiou"),
]


def process(text: str) -> list[str]:
    """Generate 3 random text transformations."""
    return [fn(text) for fn in random.sample(TRANSFORMS, k=3)]


def run_daemon():
    """Run as daemon over Unix socket or idle mode."""
    logger.info("initializing")
    endpoint = os.environ.get("TARRAGON_PLUGINS_ENDPOINT")

    if not endpoint:
        logger.info("started successfully; idle mode")
        signal.pause()
        return 0

    s = sock_mod.socket(sock_mod.AF_UNIX, sock_mod.SOCK_STREAM)
    # Retry connection in case the daemon listener isn't ready yet.
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

    # Track selections
    variants_map = {}

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
            try:
                variants = process(text)
                variants_map[qid] = variants
                resp = {
                    "type": "response",
                    "query_id": qid,
                    "data": {
                        "input": text,
                        "variants": [
                            {"id": str(i + 1), "label": v}
                            for i, v in enumerate(variants)
                        ],
                    },
                }
            except Exception as e:
                resp = {"type": "response", "query_id": qid, "data": {"error": str(e)}}

            s.sendall(json.dumps(resp).encode() + b"\n")
            logger.info("response sent qid=%s", qid)

        elif typ == "select":
            token = msg.get("text", "")
            try:
                idx = int(token) - 1
                val = variants_map.get(qid, [])[idx]
            except (ValueError, IndexError):
                val = None
            logger.info("selection qid=%s token=%s value=%s", qid, token, val)

    logger.info("exiting")
    return 0


def main(argv=None):
    parser = argparse.ArgumentParser(description="Tarragon Template Python Plugin")
    parser.add_argument("--once", metavar="TEXT", help="Process once and print JSON")
    args = parser.parse_args(argv)

    if args.once:
        logger.info("request: %s", args.once)
        try:
            resp = process(args.once)
            print(json.dumps({"input": args.once, "variants": resp}))
            return 0
        except Exception as e:
            logger.exception("processing failed")
            print(json.dumps({"error": str(e)}))
            return 1

    return run_daemon()


if __name__ == "__main__":
    raise SystemExit(main())
