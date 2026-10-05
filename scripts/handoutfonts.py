# /// script
# requires-python = ">=3.11"
# dependencies = ["fonttools>=4.50", "brotli", "uharfbuzz", "numpy", "joblib", "scikit-learn==1.9.0"]
# ///
"""Build the four Noto Sans faces xy embeds in its PDF export and handouts.

Each face is Noto Sans 2.015 as notofonts released it, the hinted static build,
with two things done to it:

  · the symbols Noto Sans lacks are merged in from Noto Sans Symbols 2
    (symbolfonts.py), since typst sees no font but these and an author's ⏸
    would otherwise come out as tofu;
  · fonts-fixing's spacing models are run over it, the same three passes
    bodyfonts.py runs over the web faces. Stock Noto sets v and y close to
    their neighbours: e|v stands 31 units apart against 96 for o|o, and
    "believe" reads cramped in a handout. The passes open e|v to 57.

The family keeps its name, since a handout's font_family names it. The faces
are static, so each weight is judged on its own; the hinting stays, and the
spacing passes move outlines whole, which leaves the instructions valid.

The upstream zip and the fonts-fixing commit are pinned, so a rerun is
byte-stable. Run from the repo root:

    uv run scripts/handoutfonts.py

The .docx template embeds the same four faces: after a rebuild, run
docxfonts.py to put them there too.
"""

from __future__ import annotations

import io

from fontsource import ROOT, fetch, fixing, load, member, models
from symbolfonts import donor_subset, fetch_donor, merge

OUT = ROOT / "xy/internal/chgk/handout/assets"
STYLES = ("Regular", "Bold", "Italic", "BoldItalic")

UPSTREAM = (
    "NotoSans-v2.015.zip",
    "https://github.com/notofonts/latin-greek-cyrillic/releases/download/NotoSans-v2.015/NotoSans-v2.015.zip",
    "0c34df072a3fa7efbb7cbf34950e1f971a4447cffe365d3a359e2d4089b958f5",
)


def build(style: str, upstream: bytes, piece: bytes, model, pairs) -> None:
    from respacing import space, summary

    source = member(upstream, UPSTREAM[0], f"/hinted/ttf/NotoSans-{style}.ttf")
    buffer = io.BytesIO()
    merge(source, piece).save(buffer)
    font = load(buffer.getvalue())
    stats = space(font, model, pairs)
    path = OUT / f"NotoSans-{style}.ttf"
    font.save(path)
    print(f"+ {path.relative_to(ROOT)}\n        {summary(stats)}", flush=True)


def main() -> None:
    upstream = fetch(*UPSTREAM)
    piece = donor_subset(fetch_donor(), flavor=None)
    fixing()
    model, pairs = models()
    for style in STYLES:
        build(style, upstream, piece, model, pairs)


if __name__ == "__main__":
    main()
