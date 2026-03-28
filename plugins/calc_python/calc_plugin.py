#!/usr/bin/env python3
"""
Calculator plugin for Tarragon.
Evaluates basic math expressions with a safe AST parser.
"""

import argparse
import json
import logging
import math
import os
import re
import socket as sock_mod
import signal
import sys
import ast

PLUGIN_NAME = os.environ.get("TARRAGON_PLUGIN_NAME", "calculator")
logging.basicConfig(
    level=logging.INFO,
    format=f"[PLUGIN: {PLUGIN_NAME}] %(levelname)s %(message)s",
    handlers=[logging.StreamHandler(sys.stderr)],
)
logger = logging.getLogger(__name__)

ALLOWED_FUNCS = {
    "abs": abs,
    "sqrt": math.sqrt,
    "sin": math.sin,
    "cos": math.cos,
    "tan": math.tan,
    "asin": math.asin,
    "acos": math.acos,
    "atan": math.atan,
    "log": math.log,
    "log10": math.log10,
    "floor": math.floor,
    "ceil": math.ceil,
}
ALLOWED_CONSTS = {
    "pi": math.pi,
    "e": math.e,
}

MATH_LIKE = re.compile(r"[0-9+\-*/%^().]|\b(pi|e)\b", re.IGNORECASE)


class SafeEval(ast.NodeVisitor):
    def visit_Expression(self, node):
        return self.visit(node.body)

    def visit_BinOp(self, node):
        left = self.visit(node.left)
        right = self.visit(node.right)
        op = node.op
        if isinstance(op, ast.Add):
            return left + right
        if isinstance(op, ast.Sub):
            return left - right
        if isinstance(op, ast.Mult):
            return left * right
        if isinstance(op, ast.Div):
            return left / right
        if isinstance(op, ast.FloorDiv):
            return left // right
        if isinstance(op, ast.Mod):
            return left % right
        if isinstance(op, ast.Pow):
            return left ** right
        raise ValueError("unsupported operator")

    def visit_UnaryOp(self, node):
        val = self.visit(node.operand)
        if isinstance(node.op, ast.UAdd):
            return +val
        if isinstance(node.op, ast.USub):
            return -val
        raise ValueError("unsupported unary operator")

    def visit_Call(self, node):
        if not isinstance(node.func, ast.Name):
            raise ValueError("unsupported function")
        name = node.func.id
        fn = ALLOWED_FUNCS.get(name)
        if fn is None:
            raise ValueError("unsupported function")
        args = [self.visit(a) for a in node.args]
        return fn(*args)

    def visit_Name(self, node):
        if node.id in ALLOWED_CONSTS:
            return ALLOWED_CONSTS[node.id]
        raise ValueError("unknown identifier")

    def visit_Constant(self, node):
        if isinstance(node.value, (int, float)):
            return node.value
        raise ValueError("unsupported literal")

    def generic_visit(self, node):
        raise ValueError("unsupported expression")


def eval_expr(text: str) -> float:
    tree = ast.parse(text, mode="eval")
    return SafeEval().visit(tree)


def format_value(val: float) -> str:
    if isinstance(val, float):
        if math.isfinite(val) and abs(val - round(val)) < 1e-12:
            return str(int(round(val)))
        return format(val, ".12g")
    return str(val)


def looks_like_math(text: str) -> bool:
    return bool(MATH_LIKE.search(text))


def process(text: str):
    if not looks_like_math(text):
        return []
    try:
        val = eval_expr(text)
    except Exception:
        return []
    out = format_value(val)
    return [
        {
            "id": out,
            "label": f"{text.strip()} = {out}",
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
            token = msg.get("text", "")
            logger.info("selection qid=%s token=%s", qid, token)

    logger.info("exiting")
    return 0


def main(argv=None):
    parser = argparse.ArgumentParser(description="Tarragon Calculator Plugin")
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
