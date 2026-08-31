"""Regenerates the lj oracles: what chgksuite's LjExporter would post.

Run from a chgksuite checkout: `uv run python .../gen_lj_oracle.py`.

The XML-RPC half is stubbed out — the oracle is the HTML lj_process builds, not
the posting. Imgur.upload_image is stubbed as it is for the other publishers.
"""

import json
import os
import sys

from chgksuite.common import DefaultNamespace, get_source_dirs
from chgksuite.composer.chgksuite_parser import parse_4s
from chgksuite.composer.composer_common import Imgur

RESOURCES = get_source_dirs()[1]
ROOT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..")
TESTDATA = os.path.join(ROOT, "internal", "chgk", "lj", "testdata")

Imgur.upload_image = lambda self, path, title=None: {
    "data": {"link": "https://img.example/" + os.path.basename(path)}
}

import xmlrpc.client


class _NoServer:
    def __init__(self, *a, **kw):
        pass

    def __getattr__(self, name):
        raise AssertionError("the oracle must not talk to livejournal")


xmlrpc.client.ServerProxy = _NoServer

from chgksuite.composer.lj import LjExporter  # noqa: E402


def make_args(**over):
    args = DefaultNamespace()
    args.labels_file = os.path.join(RESOURCES, "labels_ru.toml")
    args.regexes_file = os.path.join(RESOURCES, "regexes_ru.json")
    args.game = "chgk"
    args.language = "ru"
    args.imgur_client_id = None
    args.nospoilers = False
    args.splittours = False
    args.genimp = False
    args.navigation = False
    args.replace_no_break_spaces = "on"
    args.replace_no_break_hyphens = "on"
    args.only_question_number = False
    args.debug = False
    for k, v in over.items():
        setattr(args, k, v)
    return args


CASES = {
    "plain": {},
    "nospoilers": {"nospoilers": True},
    "splittours": {"splittours": True},
    "genimp": {"splittours": True, "genimp": True},
}


def main():
    out = []
    for name in sorted(os.listdir(TESTDATA)):
        if not name.endswith(".4s"):
            continue
        path = os.path.join(TESTDATA, name)
        with open(path, encoding="utf8") as f:
            source = f.read()
        for tag, over in CASES.items():
            args = make_args(**over)
            exporter = LjExporter(
                parse_4s(source, game="chgk"),
                args,
                {"targetdir": TESTDATA, "tmp_dir": TESTDATA},
            )
            exporter.counter = 1
            if args.splittours:
                posts = [exporter.lj_process(t) for t in exporter.split_into_tours()]
            else:
                posts = [exporter.lj_process(exporter.structure)]
            out.append({"fixture": name[: -len(".4s")], "variant": tag, "posts": posts})
    print(json.dumps(out, ensure_ascii=False, indent=1))


if __name__ == "__main__":
    main()
