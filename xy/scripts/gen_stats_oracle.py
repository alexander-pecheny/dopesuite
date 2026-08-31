"""Regenerates the add_stats oracle: chgksuite's own `compose add_stats` output.

Run from a chgksuite checkout: `uv run python .../gen_stats_oracle.py`.
"""

import os
import sys

from chgksuite.common import DefaultNamespace, get_source_dirs
from chgksuite.composer.chgksuite_parser import parse_4s
from chgksuite.composer.stats import StatsAdder

RESOURCES = get_source_dirs()[1]
ROOT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..")
TESTDATA = os.path.join(ROOT, "internal", "chgk", "stats", "testdata")

CASES = {
    "tour_csv": {"custom_csv": "stats_tour.csv"},
    "tour2_csv": {"custom_csv": "stats_tour2.csv"},
    "tour_xlsx": {"custom_csv": "stats_tour.xlsx"},
    "full_xlsx": {"custom_csv": "stats_full.xlsx"},
    "range": {"custom_csv": "stats_tour.csv", "question_range": "13-24"},
    "threshold": {"custom_csv": "stats_tour.csv", "team_naming_threshold": 8},
}


def make_args(**over):
    args = DefaultNamespace()
    args.labels_file = os.path.join(RESOURCES, "labels_ru.toml")
    args.regexes_file = os.path.join(RESOURCES, "regexes_ru.json")
    args.language = "ru"
    args.debug = False
    args.rating_ids = None
    args.custom_csv = None
    args.custom_csv_args = "{}"
    args.question_range = None
    args.team_naming_threshold = 2
    args.numbers_handling = "default"
    for k, v in over.items():
        setattr(args, k, v)
    return args


def main():
    src = os.path.join(TESTDATA, "package.4s")
    with open(src, encoding="utf8") as f:
        text = f.read()
    for name, over in CASES.items():
        over = dict(over)
        over["custom_csv"] = os.path.join(TESTDATA, over["custom_csv"])
        args = make_args(**over)
        adder = StatsAdder(
            parse_4s(text, game="chgk"), args, {"targetdir": TESTDATA, "tmp_dir": TESTDATA}
        )
        out = os.path.join(TESTDATA, f"{name}.4s.oracle")
        adder.export(out)
        sys.stderr.write(f"wrote {out}\n")


if __name__ == "__main__":
    main()
