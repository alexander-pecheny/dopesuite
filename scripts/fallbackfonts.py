# /// script
# requires-python = ">=3.11"
# dependencies = ["fonttools>=4.50", "brotli"]
# ///
"""Build the fallback faces typst reaches for when Noto Sans lacks a character.

typst sees no font but the ones chgksuite hands it, and Noto Sans covers Latin,
Greek and Cyrillic only. A browser always finds some font for a character, so
an author may paste in anything and expect to see it in the PDF too. This
builds a face for every script Unicode has, in two tiers:

  · every Noto family google/fonts carries for a script Noto Sans lacks,
    listed with its checksum in fallbackfonts.json. Where a script has both,
    the sans is taken and the serif left out; the UI, Display, Mono and
    looped variants and the extra CJK regions (HK, TC, JP) are left out too,
    since SC has the Han and the kana. Most are Regular only; a script a
    ЧГК author plausibly sets in bold has "weights": [400, 700]. Korean is
    cut down to Hangul ("keep": "hangul"), as SC already has the hanja;
  · GNU Unifont and Unifont Upper as the last resort, cut down to the
    characters no Noto face has. They are bitmap-style glyphs, plain but
    legible, which beats a box. The ~70,000 rare CJK ideographs of the
    extension blocks are the one thing left out on purpose: no Noto face
    has them and a font that does costs 30-70 MB.

google/fonts ships most Noto families variable. typst would set a variable
face at its default instance only, so this pins static instances. Every
download is pinned and checksummed, so a rerun is byte-stable.

The faces cost binary size only: xy's typst pool loads a family into an
instance only when a source has a character of it (chgksuite/fallbackfont).

Run from the repo root:  uv run scripts/fallbackfonts.py
"""

from __future__ import annotations

import io
import json
import re
import sys
from pathlib import PurePosixPath

from fontTools import subset
from fontTools.ttLib import TTFont
from fontTools.varLib import instancer

from fontsource import ROOT, fetch, load

COMMIT = "3b1480ea4b6e15fed70a42f4cb29216476a044ed"  # google/fonts main, 2026-08
MANIFEST = ROOT / "scripts/fallbackfonts.json"
OUT = ROOT / "chgksuite/fallbackfont/fonts"
COVERAGE_TS = ROOT / "chgksuite/web/ts/fontcoverage_gen.ts"
BASE = [ROOT / "chgksuite/handout/assets" / f"NotoSans-{s}.ttf"
        for s in ("Regular", "Bold", "Italic", "BoldItalic")]

REGULAR = 400
BOLD = 700
STYLE = {REGULAR: "Regular", BOLD: "Bold"}

# Hangul: the jamo, the compatibility jamo, extended A and B, the syllables.
HANGUL_BLOCKS = ((0x1100, 0x1200), (0x3130, 0x3190), (0xA960, 0xA980), (0xAC00, 0xD7B0), (0xD7B0, 0xD800))
KEEP = {"hangul": [cp for first, end in HANGUL_BLOCKS for cp in range(first, end)]}

# The Private Use Areas: the BMP's, and planes 15 and 16 whole.
PUA_FIRST = 0xE000
PUA_LAST = 0xF8FF
PUA_PLANES = 0xF0000

UNIFONT_VERSION = "17.0.05"
UNIFONT_URL = f"https://unifoundry.com/pub/unifont/unifont-{UNIFONT_VERSION}/font-builds/"
# (file, sha256, the name it is written under). Unifont sorts after every Noto
# face, which is the order fallbackfont tries them in.
LAST_RESORT = [
    (f"unifont-{UNIFONT_VERSION}.otf",
     "85701ab9b1e251ee16f4df00b13f22eac311d72b7dab427a7d975fe7f5064702", "Unifont-Regular.otf"),
    (f"unifont_upper-{UNIFONT_VERSION}.otf",
     "f4fd6d5d752726d384feef175bb780c9f29382cd4941c9e1e6990d7c3822a090", "UnifontUpper-Regular.otf"),
]


def private(cp: int) -> bool:
    """The Private Use Areas, whose characters mean whatever one font says:
    Unifont's glyphs there would stand for nothing an author typed."""
    return PUA_FIRST <= cp <= PUA_LAST or cp >= PUA_PLANES


def family_file(path: str) -> str:
    """NotoSansArmenian out of notosansarmenian/NotoSansArmenian[wdth,wght].ttf."""
    return re.match(r"[A-Za-z0-9]+", PurePosixPath(path).name).group()


def static(font: TTFont, weight: int) -> TTFont:
    """The instance at `weight`, every other axis at its default."""
    pin = {a.axisTag: a.defaultValue for a in font["fvar"].axes}
    if "wght" in pin:
        pin["wght"] = weight
    try:
        font = instancer.instantiateVariableFont(font, pin, updateFontNames=True)
    except ValueError:
        # No STAT values to name the instance by. The names stay the variable
        # font's, whose default is the Regular this pins anyway.
        font = instancer.instantiateVariableFont(font, pin)
    font.recalcTimestamp = False
    return font


def cut(font: TTFont, unicodes) -> None:
    opts = subset.Options()
    opts.layout_features = ["*"]
    opts.name_IDs = ["*"]
    opts.notdef_outline = True
    subsetter = subset.Subsetter(opts)
    subsetter.populate(unicodes=unicodes)
    subsetter.subset(font)


def build(entry: dict, weight: int) -> TTFont:
    path = entry["path"]
    name = PurePosixPath(path).name
    data = fetch(f"fallback/{name}", f"https://raw.githubusercontent.com/google/fonts/{COMMIT}/ofl/{path}",
                 entry["sha256"])
    font = load(data)
    if "fvar" in font:
        font = static(font, weight)
    if "keep" in entry:
        cut(font, KEEP[entry["keep"]])
    probe = entry.get("probe")
    if probe and ord(probe) not in font.getBestCmap():
        sys.exit(f"{name} lacks {probe!r}")
    if BOLD in entry.get("weights", ()) and font["OS/2"].usWeightClass != weight:
        sys.exit(f"{name}: weight {font['OS/2'].usWeightClass}, wanted {weight}")
    return font


def save(font: TTFont, target) -> None:
    out = io.BytesIO()
    font.save(out)
    target.write_bytes(out.getvalue())
    print(f"+ {target.relative_to(ROOT)} ({target.stat().st_size} bytes)")


def write_coverage(covered: set[int]) -> None:
    """Every character some face has, as ranges, for the editor in the browser:
    it warns about a character the PDF cannot show while the author types it.
    fallbackfont's tests hold the table to the embedded faces."""
    firsts = [cp for cp in sorted(covered) if cp - 1 not in covered]
    lasts = [cp for cp in sorted(covered) if cp + 1 not in covered]
    lines = [f"  0x{a:X}, 0x{b:X}," for a, b in zip(firsts, lasts)]
    COVERAGE_TS.write_text(
        "// Code generated by scripts/fallbackfonts.py. DO NOT EDIT.\n\n"
        "// The characters the PDF export has a face for (Noto Sans and the fallback\n"
        "// faces), as inclusive [first, last] pairs in ascending order.\n"
        "export const COVERED = [\n" + "\n".join(lines) + "\n];\n")
    print(f"+ {COVERAGE_TS.relative_to(ROOT)} ({len(lines)} ranges)")


def build_noto(covered: set[int]) -> set[str]:
    """The Noto faces of the manifest; adds what they cover to `covered`."""
    written = set()
    for entry in json.loads(MANIFEST.read_text()):
        for w in entry.get("weights", [REGULAR]):
            font = build(entry, w)
            covered |= set(font.getBestCmap())
            target = OUT / f"{family_file(entry['path'])}-{STYLE[w]}.ttf"
            save(font, target)
            written.add(target.name)
    return written


def build_last_resort(covered: set[int]) -> set[str]:
    """Unifont, cut down to what nothing before it covers."""
    written = set()
    for name, digest, out_name in LAST_RESORT:
        font = load(fetch(f"fallback/{name}", UNIFONT_URL + name, digest))
        rest = [cp for cp in font.getBestCmap() if cp not in covered and not private(cp)]
        cut(font, rest)
        covered |= set(rest)
        save(font, OUT / out_name)
        written.add(out_name)
    return written


def main() -> None:
    OUT.mkdir(parents=True, exist_ok=True)
    covered = set()
    for p in BASE:
        covered |= set(TTFont(p, lazy=True).getBestCmap())
    wanted = build_noto(covered) | build_last_resort(covered)
    write_coverage(covered)
    for stale in [*OUT.glob("*.ttf"), *OUT.glob("*.otf")]:
        if stale.name not in wanted:
            stale.unlink()
            print(f"- {stale.relative_to(ROOT)}")


if __name__ == "__main__":
    main()
