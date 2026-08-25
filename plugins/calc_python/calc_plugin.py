#!/usr/bin/env python3
"""Calculator and equation solver plugin for Tarragon."""

import argparse
import ast
import json
import logging
import math
import os
import re
import socket as sock_mod
import signal
import subprocess
import sys

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
    tree = ast.parse(normalize_expr(text), mode="eval")
    return SafeEval().visit(tree)


def normalize_expr(text: str, *, implicit_multiplication: bool = False) -> str:
    text = text.strip().replace("^", "**").replace("X", "x")
    if not implicit_multiplication:
        return text
    text = re.sub(
        r"(?<![\w.])(\d+(?:\.\d*)?|\.\d+)(?=[x(])",
        r"\1*",
        text,
    )
    text = re.sub(r"(?<=[x)])(?=[x(])", "*", text)
    return re.sub(r"(?<=[x)])(?=\d)", "*", text)


def _add_polynomials(left, right):
    return tuple(a + b for a, b in zip(left, right))


def _subtract_polynomials(left, right):
    return tuple(a - b for a, b in zip(left, right))


def _multiply_polynomials(left, right):
    result = [0, 0, 0]
    for left_power, left_coefficient in enumerate(left):
        for right_power, right_coefficient in enumerate(right):
            coefficient = left_coefficient * right_coefficient
            power = left_power + right_power
            if power > 2:
                if coefficient != 0:
                    raise ValueError("equation degree exceeds two")
                continue
            result[power] += coefficient
    return tuple(result)


class PolynomialEval(ast.NodeVisitor):
    """Evaluate an AST as coefficients for c0 + c1*x + c2*x^2."""

    def visit_Expression(self, node):
        return self.visit(node.body)

    def visit_BinOp(self, node):
        left = self.visit(node.left)
        right = self.visit(node.right)
        if isinstance(node.op, ast.Add):
            return _add_polynomials(left, right)
        if isinstance(node.op, ast.Sub):
            return _subtract_polynomials(left, right)
        if isinstance(node.op, ast.Mult):
            return _multiply_polynomials(left, right)
        if isinstance(node.op, ast.Div):
            if right[1:] != (0, 0):
                raise ValueError("cannot divide by x")
            return tuple(coefficient / right[0] for coefficient in left)
        if isinstance(node.op, ast.Pow):
            if right[1:] != (0, 0):
                raise ValueError("variable exponent is unsupported")
            exponent = right[0]
            if left[1:] == (0, 0):
                return (left[0] ** exponent, 0, 0)
            if not isinstance(exponent, int) or not 0 <= exponent <= 2:
                raise ValueError("equation degree exceeds two")
            result = (1, 0, 0)
            for _ in range(exponent):
                result = _multiply_polynomials(result, left)
            return result
        raise ValueError("unsupported operator in equation")

    def visit_UnaryOp(self, node):
        value = self.visit(node.operand)
        if isinstance(node.op, ast.UAdd):
            return value
        if isinstance(node.op, ast.USub):
            return tuple(-coefficient for coefficient in value)
        raise ValueError("unsupported unary operator")

    def visit_Call(self, node):
        if not isinstance(node.func, ast.Name):
            raise ValueError("unsupported function")
        fn = ALLOWED_FUNCS.get(node.func.id)
        if fn is None:
            raise ValueError("unsupported function")
        args = [self.visit(arg) for arg in node.args]
        if any(arg[1:] != (0, 0) for arg in args):
            raise ValueError("functions of x are unsupported")
        return (fn(*(arg[0] for arg in args)), 0, 0)

    def visit_Name(self, node):
        if node.id == "x":
            return (0, 1, 0)
        if node.id in ALLOWED_CONSTS:
            return (ALLOWED_CONSTS[node.id], 0, 0)
        raise ValueError("unknown identifier")

    def visit_Constant(self, node):
        if isinstance(node.value, (int, float)):
            return (node.value, 0, 0)
        raise ValueError("unsupported literal")

    def generic_visit(self, node):
        raise ValueError("unsupported equation")


def solve_equation(text: str) -> list[float]:
    if text.count("=") != 1:
        raise ValueError("equation must contain one equals sign")

    left_text, right_text = text.split("=", 1)
    if not left_text.strip() or not right_text.strip():
        raise ValueError("equation side is empty")

    evaluator = PolynomialEval()
    left = evaluator.visit(
        ast.parse(normalize_expr(left_text, implicit_multiplication=True), mode="eval")
    )
    right = evaluator.visit(
        ast.parse(normalize_expr(right_text, implicit_multiplication=True), mode="eval")
    )
    constant, linear, quadratic = _subtract_polynomials(left, right)

    if not math.isclose(quadratic, 0, abs_tol=1e-12):
        discriminant = linear * linear - 4 * quadratic * constant
        if discriminant < 0 and not math.isclose(discriminant, 0, abs_tol=1e-12):
            return []
        discriminant = max(discriminant, 0)
        root = math.sqrt(discriminant)
        solutions = [
            (-linear - root) / (2 * quadratic),
            (-linear + root) / (2 * quadratic),
        ]
        if math.isclose(solutions[0], solutions[1], abs_tol=1e-12):
            return [solutions[0]]
        return sorted(solutions)

    if math.isclose(linear, 0, abs_tol=1e-12):
        return []
    return [-constant / linear]


def format_value(val: float) -> str:
    if isinstance(val, float):
        if math.isfinite(val) and abs(val - round(val)) < 1e-12:
            return str(int(round(val)))
        return format(val, ".12g")
    return str(val)


def looks_like_math(text: str) -> bool:
    return bool(MATH_LIKE.search(text))


def process(text: str):
    if "=" in text:
        try:
            solutions = solve_equation(text)
        except Exception:
            return []
        results = []
        for solution in solutions:
            out = format_value(solution)
            results.append(
                {
                    "id": out,
                    "label": f"{text.strip()} -> x = {out}",
                    "description": "Copy to clipboard",
                    "icon": "accessories-calculator",
                    "category": "Calculator",
                    "actions": [
                        {
                            "name": "copy",
                            "default": True,
                            "description": "Copy to clipboard",
                        }
                    ],
                }
            )
        return results

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
            "description": "Copy to clipboard",
            "icon": "accessories-calculator",
            "category": "Calculator",
            "actions": [
                {
                    "name": "copy",
                    "default": True,
                    "description": "Copy to clipboard",
                }
            ],
        }
    ]


def _copy_to_clipboard(text: str) -> tuple[bool, str]:
    try:
        subprocess.run(["wl-copy", text], check=True, timeout=5)
        return True, "Copied to clipboard"
    except (FileNotFoundError, subprocess.CalledProcessError):
        try:
            subprocess.run(
                ["xclip", "-selection", "clipboard"],
                input=text.encode(),
                check=True,
                timeout=5,
            )
            return True, "Copied to clipboard"
        except Exception as err:
            return False, str(err)
    except Exception as err:
        return False, str(err)


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
            result_id = msg.get("result_id", "")
            action = msg.get("action", "")
            logger.info("select qid=%s result_id=%s action=%s", qid, result_id, action)
            try:
                if action and action != "copy":
                    raise ValueError(f"unsupported action: {action}")
                success, message = _copy_to_clipboard(result_id)
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
