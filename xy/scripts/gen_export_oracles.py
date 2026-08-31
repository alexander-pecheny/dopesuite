"""Regenerates the markdown / redditmd / openquiz / base oracles from chgksuite.

Run from a chgksuite checkout: `uv run python .../gen_export_oracles.py`.

These three exporters publish text, so a picture has to become a URL, and
chgksuite's way is an imgur upload. Imgur.upload_image is stubbed with a link
derived from the file's name, which is what the Go tests hand their exporters
too — the oracle is what the exporter does with the link, not the upload.
"""

import os
import sys

from chgksuite.common import DefaultNamespace, get_source_dirs
from chgksuite.composer.composer_common import BaseExporter, Imgur
from chgksuite.composer.chgksuite_parser import parse_4s
from chgksuite.composer.markdown import MarkdownExporter
from chgksuite.composer.openquiz import OpenquizExporter
from chgksuite.composer.db import DbExporter

RESOURCES = get_source_dirs()[1]
ROOT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..")

Imgur.upload_image = lambda self, path, title=None: {
    "data": {"link": "https://img.example/" + os.path.basename(path)}
}


def make_args(**over):
    args = DefaultNamespace()
    args.labels_file = os.path.join(RESOURCES, "labels_ru.toml")
    args.regexes_file = os.path.join(RESOURCES, "regexes_ru.json")
    args.game = "chgk"
    args.imgur_client_id = None
    args.filetype = "markdown"
    args.replace_no_break_spaces = "on"
    args.replace_no_break_hyphens = "on"
    args.remove_accents = False
    args.numbers_handling = "default"
    args.clipboard = False
    for k, v in over.items():
        setattr(args, k, v)
    return args


def run(cls, src, out, args):
    with open(src, encoding="utf8") as f:
        structure = parse_4s(f.read(), game=args.game)
    targetdir = os.path.dirname(src)
    exporter = cls(structure, args, {"targetdir": targetdir, "tmp_dir": targetdir})
    exporter.export(out)


def main():
    for pkg, cls, filetype, ext in (
        ("markdown", MarkdownExporter, "markdown", "md"),
        ("markdown", MarkdownExporter, "redditmd", "md"),
        ("openquiz", OpenquizExporter, "openquiz", "json"),
        ("dbtext", DbExporter, "base", "txt"),
    ):
        testdata = os.path.join(ROOT, "internal", "chgk", pkg, "testdata")
        if not os.path.isdir(testdata):
            continue
        for name in sorted(os.listdir(testdata)):
            if not name.endswith(".4s"):
                continue
            src = os.path.join(testdata, name)
            out = os.path.join(testdata, f"{name[:-3]}__{filetype}.canon")
            run(cls, src, out, make_args(filetype=filetype))
            sys.stderr.write(f"wrote {out}\n")


if __name__ == "__main__":
    main()
