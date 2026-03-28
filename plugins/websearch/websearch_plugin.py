#!/usr/bin/env python3
"""
Websearch plugin for Tarragon.
Generates search URLs from prefix-based shortcuts.
"""

import argparse
import json
import logging
import os
import signal
import socket as sock_mod
import subprocess
import sys
from urllib.parse import quote_plus

PLUGIN_NAME = os.environ.get("TARRAGON_PLUGIN_NAME", "websearch")
logging.basicConfig(
    level=logging.INFO,
    format=f"[PLUGIN: {PLUGIN_NAME}] %(levelname)s %(message)s",
    handlers=[logging.StreamHandler(sys.stderr)],
)
logger = logging.getLogger(__name__)

ENGINES = {
    "g": ("Google", "https://www.google.com/search?q={query}"),
    "yt": ("YouTube", "https://www.youtube.com/results?search_query={query}"),
    "ddg": ("DuckDuckGo", "https://duckduckgo.com/?q={query}"),
    "w": ("Wikipedia", "https://en.wikipedia.org/wiki/Special:Search?search={query}"),
    "gh": ("GitHub", "https://github.com/search?q={query}"),
}


def process(text: str):
    prefix, _, query = text.partition(" ")
    prefix = prefix.strip().lower()

    engine = ENGINES.get(prefix)
    if not engine:
        return []

    if not query.strip():
        name = engine[0]
        return [
            {
                "id": "websearch-hint",
                "label": f"Type '{prefix} <query>' to search {name}",
                "score": 0.3,
            }
        ]

    name, template = engine
    clean_query = query.strip()
    encoded_query = quote_plus(clean_query)
    url = template.format(query=encoded_query)
    return [
        {
            "id": url,
            "label": f"{name}: {clean_query}",
            "score": 1.0,
            "actions": [
                {
                    "name": "open",
                    "default": True,
                    "description": "Open in browser",
                }
            ],
        }
    ]


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
            try:
                if action and action != "open":
                    raise ValueError(f"unsupported action: {action}")
                if result_id == "websearch-hint" or not result_id:
                    raise ValueError("no URL to open")
                subprocess.Popen(
                    ["xdg-open", result_id],
                    start_new_session=True,
                    stdout=subprocess.DEVNULL,
                    stderr=subprocess.DEVNULL,
                )
                success, message = True, "Opened in browser"
            except Exception as err:
                success, message = False, str(err)
            resp = {
                "type": "select_response",
                "success": success,
                "message": message,
            }
            s.sendall(json.dumps(resp).encode() + b"\n")

    logger.info("exiting")
    return 0


def main(argv=None):
    parser = argparse.ArgumentParser(description="Tarragon Websearch Plugin")
    parser.add_argument("--once", metavar="TEXT", help="Process once and print JSON")
    args = parser.parse_args(argv)

    if args.once:
        logger.info("request: %s", args.once)
        results = process(args.once)
        print(json.dumps({"results": results}))
        return 0

    return run_daemon()


if __name__ == "__main__":
    raise SystemExit(main())
