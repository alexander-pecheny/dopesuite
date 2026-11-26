# /// script
# requires-python = ">=3.11"
# dependencies = ["fonttools>=4.50", "brotli"]
# ///
"""Build the fallback faces typst reaches for when Noto Sans lacks a character.

typst sees no font but the ones chgksuite hands it, and Noto Sans covers Latin,
Greek and Cyrillic only. An Armenian or Georgian word in a comment, a Hebrew
quote or a Chinese character came out of the PDF export and the handouts as
tofu. typst falls back on its own to any loaded face that has the glyph, so
loading these is the whole fix: the document still names Noto Sans alone.

Each face is the Noto family for one script, from one google/fonts commit and
checksummed, so a rerun is byte-stable. google/fonts ships them variable; typst
would set a variable face at its default instance only, so this pins a static
Regular and Bold out of each. A script added later is one line in FACES.

CJK is Noto Sans SC, Regular alone: it is 17 MB variable and ~10 MB per static
weight, and every typst instance in xy's pool holds its own copy. SC carries
the kana too, so it covers Japanese as well as Chinese.

Run from the repo root:  uv run scripts/fallbackfonts.py
"""

from __future__ import annotations

import io
import sys

from fontTools.varLib import instancer

from fontsource import ROOT, fetch, load

COMMIT = "3b1480ea4b6e15fed70a42f4cb29216476a044ed"  # google/fonts main, 2026-08
OUT = ROOT / "chgksuite/handout/assets/fallback"

REGULAR, BOLD = 400, 700
BOTH = (REGULAR, BOLD)

# (google/fonts path under ofl/, sha256, weights to pin, a character it must cover)
FACES = [
    ("notosansarmenian/NotoSansArmenian[wdth,wght].ttf",
     "0870908d8318435a5daf1cd280ae15063f990cd9ad60f3e94c734ce9e1ffef71", BOTH, "օ"),
    ("notosansgeorgian/NotoSansGeorgian[wdth,wght].ttf",
     "dc591156f36842d38996c4a7a17fee9bb58e45da3e2cac7a31b7d33de700adb9", BOTH, "ა"),
    ("notosanshebrew/NotoSansHebrew[wdth,wght].ttf",
     "7ef36a2c3593758cdb622e1bdef4f84523e92fbc3ccc667438dd80ff54c2de88", BOTH, "ש"),
    ("notosansarabic/NotoSansArabic[wdth,wght].ttf",
     "63111b5b2e074dd48cc67692e0a2726d86ee94c1c37fe8598257b7b4e87e869e", BOTH, "ع"),
    ("notosansdevanagari/NotoSansDevanagari[wdth,wght].ttf",
     "9ce7b04f60e363d8870e5997744cf85cf69d38a4d7d129d364d92a3b14b461d7", BOTH, "क"),
    ("notosansbengali/NotoSansBengali[wdth,wght].ttf",
     "dcd42978094e584a849c84a51450eeac40c8826057d566ea6d4b9627a403a05a", BOTH, "ক"),
    ("notosanstamil/NotoSansTamil[wdth,wght].ttf",
     "aa3a9b321f4b0bb2c40203ffbde9af89713227866e0e13f76e5b9eeea727cf88", BOTH, "த"),
    ("notosansthai/NotoSansThai[wdth,wght].ttf",
     "5a1c559bb539583c8a1fd99d1c5b9491e5e14478c9cd2bd0970d5c3096cc9ef8", BOTH, "ก"),
    ("notosansethiopic/NotoSansEthiopic[wdth,wght].ttf",
     "0dbccc00b22d180ebd6a4bd8a733918a29e709fa6798023adc3e6cd40da65077", BOTH, "ሀ"),
    ("notosanssymbols/NotoSansSymbols[wght].ttf",
     "f7e7e04b4a24b6c78893d50cbfd2b2f6cae49617ab047bfef668d252adb128f7", BOTH, "♈"),
    ("notosanssymbols2/NotoSansSymbols2-Regular.ttf",
     "7d5fb73b7ca67a6798101741f5d280a3d016a56a197afcd4199dbb57b4b82a21", (REGULAR,), "♔"),
    ("notosansmath/NotoSansMath-Regular.ttf",
     "3f495fe933c06786e4d5f6d86b8ee70b6753a68ee3b9d87528726de0f6e2c47d", (REGULAR,), "∮"),
    ("notosanssc/NotoSansSC[wght].ttf",
     "a3041811a78c361b1de50f953c805e0244951c21c5bd412f7232ef0d899af0da", (REGULAR,), "中"),
]

STYLE = {REGULAR: "Regular", BOLD: "Bold"}


def family_file(path: str) -> str:
    """NotoSansArmenian out of notosansarmenian/NotoSansArmenian[wdth,wght].ttf."""
    name = path.split("/")[1]
    return name.split("[")[0].split("-")[0].removesuffix(".ttf")


def build(path: str, digest: str, weight: int, probe: str) -> bytes:
    name = path.split("/")[1]
    data = fetch(f"fallback/{name}", f"https://raw.githubusercontent.com/google/fonts/{COMMIT}/ofl/{path}", digest)
    font = load(data)
    if "fvar" in font:
        axes = {a.axisTag: a for a in font["fvar"].axes}
        pin = {"wght": weight}
        if "wdth" in axes:
            pin["wdth"] = 100
        font = instancer.instantiateVariableFont(font, pin, updateFontNames=True)
        font.recalcTimestamp = False
    if ord(probe) not in font.getBestCmap():
        sys.exit(f"{name} lacks {probe!r}")
    if font["OS/2"].usWeightClass != weight:
        sys.exit(f"{name}: weight {font['OS/2'].usWeightClass}, wanted {weight}")
    out = io.BytesIO()
    font.save(out)
    return out.getvalue()


def main() -> None:
    OUT.mkdir(parents=True, exist_ok=True)
    wanted = set()
    for path, digest, weights, probe in FACES:
        for w in weights:
            target = OUT / f"{family_file(path)}-{STYLE[w]}.ttf"
            target.write_bytes(build(path, digest, w, probe))
            wanted.add(target.name)
            print(f"+ {target.relative_to(ROOT)} ({target.stat().st_size} bytes)")
    for stale in OUT.glob("*.ttf"):
        if stale.name not in wanted:
            stale.unlink()
            print(f"- {stale.relative_to(ROOT)}")


if __name__ == "__main__":
    main()
