#!/usr/bin/env python3
"""Desktop files launcher plugin for Tarragon."""

import argparse
import configparser
import json
import logging
import os
from pathlib import Path
import shlex
import signal
import socket as sock_mod
import subprocess
import sys
import threading
import time

PLUGIN_NAME = os.environ.get("TARRAGON_PLUGIN_NAME", "desktop_files")
logging.basicConfig(
    level=logging.INFO,
    format=f"[PLUGIN: {PLUGIN_NAME}] %(levelname)s %(message)s",
    handlers=[logging.StreamHandler(sys.stderr)],
)
logger = logging.getLogger(__name__)

RESCAN_INTERVAL_SECONDS = 300
MAX_RESULTS = 10


def _to_bool(value: str) -> bool:
    return str(value).strip().lower() in {"1", "true", "yes", "on"}


def _split_keywords(raw_keywords: str) -> list[str]:
    if not raw_keywords:
        return []
    return [part.strip() for part in raw_keywords.replace(",", ";").split(";") if part.strip()]


def _get_option(entry: configparser.SectionProxy, key: str) -> str:
    if key in entry:
        return entry.get(key, "").strip()

    needle = f"{key.lower()}["
    for option_name in entry:
        if option_name.lower().startswith(needle):
            return entry.get(option_name, "").strip()
    return ""


def _desktop_dirs() -> list[Path]:
    dirs = [
        Path.home() / ".local/share/applications",
        Path("/usr/share/applications"),
        Path.home() / ".local/share/flatpak/exports/share/applications",
        Path("/var/lib/flatpak/exports/share/applications"),
    ]
    existing = []
    for directory in dirs:
        if directory.exists() and directory.is_dir():
            existing.append(directory)
    return existing


def scan_desktop_entries() -> list[dict]:
    entries: list[dict] = []

    for directory in _desktop_dirs():
        for file_path in directory.rglob("*.desktop"):
            parser = configparser.ConfigParser(strict=False, interpolation=None)
            try:
                with open(file_path, "r", encoding="utf-8", errors="replace") as f:
                    parser.read_file(f)
            except (OSError, configparser.Error) as err:
                logger.debug("skipping malformed desktop file %s: %s", file_path, err)
                continue

            if not parser.has_section("Desktop Entry"):
                continue

            desktop_entry = parser["Desktop Entry"]
            item_type = _get_option(desktop_entry, "Type")
            if item_type and item_type != "Application":
                continue
            if _to_bool(_get_option(desktop_entry, "NoDisplay")):
                continue
            if _to_bool(_get_option(desktop_entry, "Hidden")):
                continue

            name = _get_option(desktop_entry, "Name")
            comment = _get_option(desktop_entry, "Comment")
            keywords = _split_keywords(_get_option(desktop_entry, "Keywords"))
            exec_cmd = _get_option(desktop_entry, "Exec")
            icon = _get_option(desktop_entry, "Icon")

            if not name or not exec_cmd:
                continue

            entries.append(
                {
                    "name": name,
                    "comment": comment,
                    "keywords": keywords,
                    "exec": exec_cmd,
                    "icon": icon,
                    "desktop_file": str(file_path),
                }
            )

    return entries


class DesktopEntryCache:
    def __init__(self):
        self._lock = threading.Lock()
        self._entries: list[dict] = []

    def refresh(self):
        new_entries = scan_desktop_entries()
        with self._lock:
            self._entries = new_entries
        logger.info("desktop cache refreshed: %d entries", len(new_entries))

    def snapshot(self) -> list[dict]:
        with self._lock:
            return list(self._entries)


def _tokenize(text: str) -> list[str]:
    return [part for part in text.lower().strip().split() if part]


def process(text: str, cache: DesktopEntryCache) -> list[dict]:
    query = text.strip().lower()
    if not query:
        return []

    tokens = _tokenize(query)
    if not tokens:
        return []

    scored: list[dict] = []
    for entry in cache.snapshot():
        name = entry.get("name", "")
        comment = entry.get("comment", "")
        keywords_list = entry.get("keywords", [])

        name_l = name.lower()
        comment_l = comment.lower()
        keywords_l = " ".join(keywords_list).lower()
        searchable = f"{name_l} {comment_l} {keywords_l}"

        if not all(token in searchable for token in tokens):
            continue

        if name_l.startswith(query):
            score = 1.0
        elif all(token in name_l for token in tokens):
            score = 0.8
        elif all(token in f"{comment_l} {keywords_l}" for token in tokens):
            score = 0.6
        else:
            score = 0.6

        scored.append(
            {
                "id": entry.get("exec", ""),
                "label": name,
                "description": comment,
                "icon": entry.get("icon", ""),
                "category": "Applications",
                "score": score,
                "actions": [
                    {
                        "name": "open",
                        "default": True,
                        "description": "Launch application",
                    }
                ],
            }
        )

    scored.sort(key=lambda item: item.get("score", 0.0), reverse=True)
    return scored[:MAX_RESULTS]


def _start_refresh_thread(cache: DesktopEntryCache, stop_event: threading.Event) -> threading.Thread:
    def _loop():
        while not stop_event.wait(RESCAN_INTERVAL_SECONDS):
            try:
                cache.refresh()
            except Exception as err:  # pragma: no cover - defensive logging in daemon loop
                logger.error("background refresh failed: %s", err)

    t = threading.Thread(target=_loop, daemon=True)
    t.start()
    return t


def run_daemon() -> int:
    logger.info("initializing")
    endpoint = os.environ.get("TARRAGON_PLUGINS_ENDPOINT")

    cache = DesktopEntryCache()
    cache.refresh()
    stop_event = threading.Event()
    _start_refresh_thread(cache, stop_event)

    def _handle_stop(*_args):
        stop_event.set()

    signal.signal(signal.SIGTERM, _handle_stop)
    signal.signal(signal.SIGINT, _handle_stop)

    if not endpoint:
        logger.info("started successfully; idle mode")
        while not stop_event.is_set():
            signal.pause()
        return 0

    s = sock_mod.socket(sock_mod.AF_UNIX, sock_mod.SOCK_STREAM)
    for attempt in range(20):
        try:
            s.connect(endpoint)
            break
        except (ConnectionRefusedError, FileNotFoundError):
            if attempt == 19:
                logger.error("could not connect to %s after retries", endpoint)
                return 1
            time.sleep(0.1)

    s.sendall(json.dumps({"type": "hello", "name": PLUGIN_NAME}).encode() + b"\n")
    logger.info("connected to %s", endpoint)

    f = s.makefile("r")

    while not stop_event.is_set():
        try:
            line = f.readline()
            if not line:
                break
            msg = json.loads(line)
        except Exception as err:
            logger.error("recv error: %s", err)
            break

        qid = msg.get("query_id", "")
        typ = msg.get("type")

        if typ == "request":
            text = msg.get("text", "")
            logger.info("request qid=%s: %s", qid, text)
            results = process(text, cache)
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
                cmd = shlex.split(result_id)
                desktop_tokens = {"%u", "%U", "%f", "%F", "%d", "%D", "%n", "%N", "%i", "%c", "%k"}
                cmd = [part for part in cmd if part not in desktop_tokens]
                if not cmd:
                    raise ValueError("empty command")
                subprocess.Popen(
                    cmd,
                    start_new_session=True,
                    stdout=subprocess.DEVNULL,
                    stderr=subprocess.DEVNULL,
                )
                success, message = True, f"Launched {cmd[0]}"
            except Exception as err:
                success, message = False, str(err)
            resp = {
                "type": "select_response",
                "success": success,
                "message": message,
            }
            s.sendall(json.dumps(resp).encode() + b"\n")

    stop_event.set()
    logger.info("exiting")
    return 0


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(description="Tarragon Desktop Files Plugin")
    parser.add_argument("--once", metavar="TEXT", help="Process once and print JSON")
    args = parser.parse_args(argv)

    cache = DesktopEntryCache()
    cache.refresh()

    if args.once is not None:
        logger.info("request: %s", args.once)
        print(json.dumps({"results": process(args.once, cache)}))
        return 0

    return run_daemon()


if __name__ == "__main__":
    raise SystemExit(main())
