#!/usr/bin/env python3
"""
Template Python plugin for Tarragon.

- Logs initialization, startup success/failure.
- Exposes a process(text) function returning 3 randomly transposed variants.
- Provides a simple CLI for testing: --once "text".
"""

from __future__ import annotations

import argparse
import json
import logging
import os
import random
import signal
import sys
import time
from typing import Callable, List

try:
    import zmq  # pyzmq
except Exception:  # noqa: BLE001
    zmq = None


def _ensure_log_dir() -> str:
    root = os.environ.get("XDG_CACHE_HOME") or os.path.join(
        os.path.expanduser("~"), ".cache"
    )
    log_dir = os.path.join(root, "tarragon", "plugins", "template_python")
    os.makedirs(log_dir, exist_ok=True)
    return log_dir


def _setup_logging() -> logging.Logger:
    logger = logging.getLogger("tarragon.plugin.template_python")
    logger.setLevel(logging.INFO)
    if logger.handlers:
        return logger
    file_path = os.path.join(_ensure_log_dir(), "plugin.log")
    fmt = logging.Formatter("%(asctime)s %(levelname)s %(message)s")
    fh = logging.FileHandler(file_path)
    fh.setLevel(logging.INFO)
    fh.setFormatter(fmt)
    logger.addHandler(fh)
    sh = logging.StreamHandler(sys.stderr)
    sh.setLevel(logging.INFO)
    sh.setFormatter(fmt)
    logger.addHandler(sh)
    return logger


def _reverse(s: str) -> str:
    return s[::-1]


def _upper(s: str) -> str:
    return s.upper()


def _title(s: str) -> str:
    return s.title()


def _rot13(s: str) -> str:
    a = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
    b = "NOPQRSTUVWXYZABCDEFGHIJKLMnopqrstuvwxyzabcdefghijklm"
    return s.translate(str.maketrans(a, b))


def _sorted_letters(s: str) -> str:
    return "".join(sorted(s))


def _shuffle_letters(s: str) -> str:
    chars = list(s)
    random.shuffle(chars)
    return "".join(chars)


def _alternating_caps(s: str) -> str:
    out = []
    t = False
    for ch in s:
        if ch.isalpha():
            out.append(ch.upper() if t else ch.lower())
            t = not t
        else:
            out.append(ch)
    return "".join(out)


def _strip_vowels(s: str) -> str:
    return "".join(ch for ch in s if ch.lower() not in "aeiou")


TRANSFORMS: List[Callable[[str], str]] = [
    _reverse,
    _upper,
    _title,
    _rot13,
    _sorted_letters,
    _shuffle_letters,
    _alternating_caps,
    _strip_vowels,
]


def process(text: str) -> List[str]:
    if not isinstance(text, str):
        raise TypeError("process(text) expects a string")
    funcs = random.sample(TRANSFORMS, k=3)
    time.sleep(3)
    return [fn(text) for fn in funcs]


def _cli_once(logger: logging.Logger, text: str) -> int:
    logger.info("request received: %s", text)
    try:
        resp = process(text)
        logger.info("response generated: %s", resp)
        print(json.dumps({"input": text, "variants": resp}, ensure_ascii=False))
        return 0
    except Exception as e:
        logger.exception("processing failed: %s", e)
        print(json.dumps({"error": str(e)}))
        return 1


def _run_daemon(logger: logging.Logger) -> int:
    logger.info("initializing template_python plugin")
    endpoint = os.environ.get("TARRAGON_PLUGINS_ENDPOINT")
    name = os.environ.get("TARRAGON_PLUGIN_NAME", "template_python")
    if endpoint and zmq is not None:
        logger.info("connecting to ROUTER endpoint: %s as %s", endpoint, name)
        try:
            ctx = zmq.Context.instance()
            sock = ctx.socket(zmq.DEALER)
            sock.setsockopt(zmq.IDENTITY, name.encode("utf-8", "ignore"))
            sock.connect(endpoint)
            hello = json.dumps({"type": "hello", "name": name}).encode()
            sock.send(hello)
            logger.info("hello sent; entering recv loop")
        except Exception as e:  # noqa: BLE001
            logger.exception("zmq setup failure: %s", e)
            return 1

        stop = False

        def _sigterm(_sig, _frm):
            nonlocal stop
            logger.info("shutdown signal received")
            stop = True

        signal.signal(signal.SIGTERM, _sigterm)
        signal.signal(signal.SIGINT, _sigterm)

        while not stop:
            try:
                data = sock.recv(flags=0)
            except Exception as e:  # noqa: BLE001
                logger.exception("recv error: %s", e)
                break
            try:
                msg = json.loads(data.decode("utf-8", "ignore"))
            except Exception as e:  # noqa: BLE001
                logger.exception("invalid json: %s", e)
                continue
            if msg.get("type") != "request":
                continue
            qid = msg.get("query_id", "")
            text = msg.get("text", "")
            logger.info("request received qid=%s: %s", qid, text)
            try:
                variants = process(text)
                resp = {
                    "type": "response",
                    "query_id": qid,
                    "data": {"input": text, "variants": variants},
                }
            except Exception as e:  # noqa: BLE001
                resp = {"type": "response", "query_id": qid, "data": {"error": str(e)}}
            try:
                sock.send(json.dumps(resp).encode("utf-8"))
                logger.info("response sent qid=%s", qid)
            except Exception as e:  # noqa: BLE001
                logger.exception("send error: %s", e)
                break
        logger.info("plugin exiting")
        return 0

    # Fallback idle mode if no endpoint or pyzmq missing
    if endpoint and zmq is None:
        logger.error("pyzmq not available; install with 'pip install pyzmq'")
    logger.info("plugin started successfully; awaiting work (idle mode)")
    stop = False

    def _sigterm(_sig, _frm):
        nonlocal stop
        logger.info("shutdown signal received")
        stop = True

    signal.signal(signal.SIGTERM, _sigterm)
    signal.signal(signal.SIGINT, _sigterm)
    while not stop:
        time.sleep(5.0)
        logger.info("heartbeat: running")
    logger.info("plugin exiting")
    return 0


def main(argv: List[str] | None = None) -> int:
    logger = _setup_logging()
    parser = argparse.ArgumentParser(description="Tarragon Template Python Plugin")
    parser.add_argument(
        "--once", metavar="TEXT", help="Process once and print JSON response"
    )
    args = parser.parse_args(argv)
    if args.once:
        return _cli_once(logger, args.once)
    return _run_daemon(logger)


if __name__ == "__main__":
    raise SystemExit(main())
