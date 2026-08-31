"""Extracts the SI/троика text cases from chgksuite's own test suite.

Its unit tests hold the awkward packages as string literals — a theme written
after a source, a source list numbered exactly like the questions, the
«Мультифора» variant. This pulls each literal out and records what chgksuite
parses it into, so the Go parsers can be held to the same output.

    uv run --project ~/chgksuite/chgksuite python xy/scripts/gen_si_cases.py
"""

import ast
import os
import sys

sys.path.insert(0, os.path.expanduser("~/chgksuite/chgksuite"))

from chgksuite.common import DefaultArgs, compose_4s  # noqa: E402
from chgksuite.parser import si_parse_text, troika_parse_text  # noqa: E402

TESTS = os.path.expanduser("~/chgksuite/chgksuite/tests/chgksuite_test.py")
OUT = os.path.join(
    os.path.dirname(os.path.abspath(__file__)), "..", "internal", "chgk",
    "textparse", "testdata", "si_cases",
)
PARSERS = {"si_parse_text": ("si", si_parse_text), "troika_parse_text": ("troika", troika_parse_text)}


def cases():
    tree = ast.parse(open(TESTS, encoding="utf8").read())
    for fn in tree.body:
        if not isinstance(fn, ast.FunctionDef) or not fn.name.startswith("test_"):
            continue
        for node in ast.walk(fn):
            if not isinstance(node, ast.Call) or not isinstance(node.func, ast.Name):
                continue
            if node.func.id not in PARSERS or not node.args:
                continue
            if not isinstance(node.args[0], ast.Constant) or not isinstance(node.args[0].value, str):
                continue
            yield fn.name, node.func.id, node.args[0].value
            break


def main():
    os.makedirs(OUT, exist_ok=True)
    written = 0
    for name, parser_name, text in cases():
        game, parse = PARSERS[parser_name]
        args = DefaultArgs(game=game, numbers_handling="all")
        parsed = parse(text, args=args)
        base = os.path.join(OUT, f"{name.removeprefix('test_')}.{game}")
        with open(base + ".txt", "w", encoding="utf8") as f:
            f.write(text)
        with open(base + ".canon", "w", encoding="utf8") as f:
            f.write(compose_4s(parsed, args=args))
        written += 1
    print(f"wrote {written} cases to {OUT}", file=sys.stderr)


if __name__ == "__main__":
    main()
