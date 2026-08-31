"""Regenerates the pptx oracles: chgksuite's own presentation, unpacked.

Run from a chgksuite checkout: `uv run python .../gen_pptx_oracles.py`.

Only the parts the Go export writes are kept — the slides, presentation.xml and
[Content_Types].xml. Everything else is the template's, byte for byte, and
comparing it would only be testing the copy.
"""

import os
import shutil
import sys
import zipfile

from chgksuite.common import DefaultNamespace, get_source_dirs
from chgksuite.composer.chgksuite_parser import parse_4s
from chgksuite.composer.pptx import PptxExporter

RESOURCES = get_source_dirs()[1]
ROOT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..")
TESTDATA = os.path.join(ROOT, "internal", "chgk", "pptx", "testdata")

KEEP = ("ppt/slides/", "ppt/presentation.xml", "[Content_Types].xml")


def make_args(**over):
    args = DefaultNamespace()
    args.labels_file = os.path.join(RESOURCES, "labels_ru.toml")
    args.regexes_file = os.path.join(RESOURCES, "regexes_ru.json")
    args.pptx_config = os.path.join(RESOURCES, "pptx_config.toml")
    args.game = "chgk"
    args.language = "ru"
    args.disable_numbers = False
    args.do_not_remove_accents = False
    args.replace_no_break_spaces = "on"
    args.replace_no_break_hyphens = "on"
    args.optimize_size = "off"
    args.font = None
    args.numbers_handling = "default"
    for k, v in over.items():
        setattr(args, k, v)
    return args


def run(src, out_dir):
    with open(src, encoding="utf8") as f:
        structure = parse_4s(f.read(), game="chgk")
    targetdir = os.path.dirname(src)
    exporter = PptxExporter(
        structure, make_args(), {"targetdir": targetdir, "tmp_dir": targetdir}
    )
    pptx_path = out_dir + ".pptx"
    exporter.export(pptx_path)

    shutil.rmtree(out_dir, ignore_errors=True)
    with zipfile.ZipFile(pptx_path) as z:
        for name in z.namelist():
            if not name.startswith(KEEP):
                continue
            dest = os.path.join(out_dir, name)
            os.makedirs(os.path.dirname(dest), exist_ok=True)
            with open(dest, "wb") as f:
                f.write(z.read(name))
    os.remove(pptx_path)


def main():
    for name in sorted(os.listdir(TESTDATA)):
        if not name.endswith(".4s"):
            continue
        src = os.path.join(TESTDATA, name)
        out = os.path.join(TESTDATA, f"{name[:-3]}__pptx")
        run(src, out)
        sys.stderr.write(f"wrote {out}\n")


if __name__ == "__main__":
    main()
