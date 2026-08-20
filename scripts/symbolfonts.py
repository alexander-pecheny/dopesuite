# /// script
# requires-python = ">=3.11"
# dependencies = ["fonttools>=4.50", "brotli"]
# ///
"""Fill the symbol gap in the suite's Noto Sans fonts.

Noto Sans has no media-control block: an author who types a pause sign into a
question gets a platform fallback — a text glyph on desktop, Apple Color Emoji
on iOS — and tofu in the PDF export, whose embedded fonts are the only fonts
typst sees. The donor is Noto Sans Symbols 2 (same family, same OFL licence),
pinned to one google/fonts commit and checksummed, so a rerun is byte-stable.

Two outputs, one glyph list:
  - the four TTFs embedded in xy's PDF/handout pipeline get the glyphs MERGED
    in, so typst finds them in the face it is already using;
  - the kit gets a tiny standalone woff2 subset, declared in core.css as a
    same-family face scoped by unicode-range — the variable webfonts stay the
    deliberately-cut files their header comment documents.

Run from the repo root:  uv run scripts/symbolfonts.py
Idempotent: a target that already has every codepoint is skipped.
"""

import hashlib
import io
import sys
import urllib.request
from pathlib import Path

from fontTools import subset
from fontTools.merge import Merger
from fontTools.ttLib import TTFont

DONOR_COMMIT = "3b1480ea4b6e15fed70a42f4cb29216476a044ed"  # google/fonts main, 2026-08
DONOR_URL = (
    "https://raw.githubusercontent.com/google/fonts/"
    f"{DONOR_COMMIT}/ofl/notosanssymbols2/NotoSansSymbols2-Regular.ttf"
)
DONOR_SHA256 = "7d5fb73b7ca67a6798101741f5d280a3d016a56a197afcd4199dbb57b4b82a21"

# The curated set (ADR discussion, 2026-08-20): media controls a ЧГК author
# plausibly types, the check/cross marks that already appear in answers, and
# the clocks that fall into the same iOS-emoji trap. Extend by adding a line
# and rerunning.
CODEPOINTS = [
    0x23F8, 0x23F9, 0x23FA, 0x23EF,  # ⏸ ⏹ ⏺ ⏯
    0x23E9, 0x23EA, 0x23ED, 0x23EE,  # ⏩ ⏪ ⏭ ⏮
    0x25B6, 0x25C0,                  # ▶ ◀
    0x2713, 0x2714, 0x2716, 0x2717,  # ✓ ✔ ✖ ✗
    0x231A, 0x231B, 0x23F1, 0x23F2, 0x23F3,  # ⌚ ⌛ ⏱ ⏲ ⏳
]

ROOT = Path(__file__).resolve().parent.parent
PDF_FONTS = [
    ROOT / "xy/internal/chgk/handout/assets" / n
    for n in ("NotoSans-Regular.ttf", "NotoSans-Bold.ttf", "NotoSans-Italic.ttf", "NotoSans-BoldItalic.ttf")
]
WEB_SUBSET = ROOT / "dopeuikit/assets/fonts/noto-sans-symbols.woff2"
# The web subset is declared in core.css as a "Noto Sans" face, so a line that
# shows one of its glyphs unions its metrics into the line box. It must carry
# the body webfont's vertical metrics, not the donor's.
WEB_BODY = ROOT / "dopeuikit/assets/fonts/noto-sans-var.woff2"


def fetch_donor() -> bytes:
    cache = ROOT / ".tmp" / "NotoSansSymbols2-Regular.ttf"
    if cache.exists():
        data = cache.read_bytes()
    else:
        data = urllib.request.urlopen(DONOR_URL, timeout=60).read()
        cache.parent.mkdir(exist_ok=True)
        cache.write_bytes(data)
    got = hashlib.sha256(data).hexdigest()
    if got != DONOR_SHA256:
        sys.exit(f"donor checksum mismatch: {got} (delete {cache} if the pin moved)")
    return data


def donor_subset(data: bytes, flavor: str | None, metrics: dict | None = None) -> bytes:
    font = TTFont(io.BytesIO(data))
    missing = [hex(cp) for cp in CODEPOINTS if cp not in font.getBestCmap()]
    if missing:
        sys.exit(f"donor lacks {missing}")
    opts = subset.Options()
    # The merged-into faces own their layout; a leftover GSUB or vhea from the
    # donor either collides or trips the merger, for no gain on plain symbol
    # glyphs.
    opts.drop_tables += ["GSUB", "GPOS", "GDEF", "vhea", "vmtx", "VORG"]
    opts.flavor = flavor
    subsetter = subset.Subsetter(opts)
    subsetter.populate(unicodes=CODEPOINTS)
    subsetter.subset(font)
    if metrics is not None:
        _stamp(font, metrics)
    out = io.BytesIO()
    font.save(out)
    return out.getvalue()


def covered(path: Path) -> bool:
    cmap = TTFont(path).getBestCmap()
    return all(cp in cmap for cp in CODEPOINTS)


# The donor's vertical metrics (descender -630) are deeper than Noto Sans's
# (-293). Left in place they bloat every line box — the PDF faces via the
# merger, the web subset by being a same-family face — so both outputs carry
# the target family's metrics instead. The donor's symbol glyphs all fit
# inside Noto Sans's -293..1069 box, so this clips nothing.
_METRIC_FIELDS = {
    "hhea": ("ascender", "descender", "lineGap"),
    "OS/2": ("sTypoAscender", "sTypoDescender", "sTypoLineGap",
              "usWinAscent", "usWinDescent"),
}


def _snapshot(font: TTFont) -> dict:
    return {tbl: {f: getattr(font[tbl], f) for f in fields}
            for tbl, fields in _METRIC_FIELDS.items()}


def _stamp(font: TTFont, keep: dict) -> None:
    for tbl, fields in _METRIC_FIELDS.items():
        for f in fields:
            setattr(font[tbl], f, keep[tbl][f])


def merge_into(target: Path, piece: bytes) -> None:
    keep = _snapshot(TTFont(str(target)))
    tmp = target.with_suffix(".donor.ttf")
    tmp.write_bytes(piece)
    try:
        merged = Merger().merge([str(target), str(tmp)])
        # The merger unions cmap and glyphs but keeps the first font's
        # identity; nothing of the donor's name table survives.
        _stamp(merged, keep)
        merged.save(str(target))
    finally:
        tmp.unlink()


def main() -> None:
    data = fetch_donor()
    piece = donor_subset(data, flavor=None)
    for path in PDF_FONTS:
        if covered(path):
            print(f"= {path.relative_to(ROOT)} (already covered)")
            continue
        merge_into(path, piece)
        assert covered(path)
        print(f"+ {path.relative_to(ROOT)}")
    web_metrics = _snapshot(TTFont(WEB_BODY, lazy=True))
    WEB_SUBSET.write_bytes(donor_subset(data, flavor="woff2", metrics=web_metrics))
    print(f"+ {WEB_SUBSET.relative_to(ROOT)} ({WEB_SUBSET.stat().st_size} bytes)")


if __name__ == "__main__":
    main()
