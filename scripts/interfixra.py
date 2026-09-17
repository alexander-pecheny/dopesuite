# /// script
# requires-python = ">=3.11"
# dependencies = ["fonttools>=4.50", "brotli", "uharfbuzz", "numpy", "joblib", "scikit-learn==1.9.0"]
# ///
"""Build the web faces of Inter Fix RA, the alternative body font of the kit.

Inter Fix RA is Inter with the Russian faults fixed — the `cyrl` script taught to
run `mark`, the stress anchors Ю and я never had, Raveo's tailed a and flagless 1 —
and then respaced and rekerned by the two models. It is built by
code.pecheny.me/pecheny/fonts-fixing, whose deliverable is a .ttc of 36 STATIC
faces, and those are of no use here: Inter's statics are optimised per weight and
are not interpolation-compatible (o is 32 points at 400 and 28 at 700), so they
cannot be merged back into one variable file, and shipping three of them would
cost 259 KB against the 139 KB the variable face weighs.

So this rebuilds the same transformation on Inter's own VARIABLE sources, which
interpolate by construction, and Raveo's — Raveo ships a variable font over the
same two axes with the same avar, so its a and its 1 transplant whole, with their
gvar, instead of going through cu2qu the way the static build has to.

Three sources, each pinned and checksummed, so a rerun is byte-stable:
  · Inter's release zip (InterVariable.ttf, InterVariable-Italic.ttf);
  · Raveo's RaveoVF.ttf — the a and the 1, romans only, as upstream: Raveo has no
    italics and Inter's italic a is single-storey, so the italic takes only the 1,
    and that through Inter's own cv01 rule;
  · fonts-fixing itself, cloned at a commit, for the fixes and the two models.

Two deviations from the static build, both of them the axis the statics don't have:
  · the sidebearing and kerning models read the DEFAULT instance and their moves
    are written as one translation each, so Regular's spacing carries across the
    weight range instead of each weight being spaced in its own right;
  · U+23F8, drawn from the face's own play and stop, gets no gvar, so the pause
    sign keeps Regular's bar width at every weight. It is one symbol glyph.
  · HVAR is dropped: Inter's gvar carries the phantom points too (instancing with
    and without HVAR gives the same advances to the unit), and with HVAR gone a
    transplanted glyph's advance cannot disagree with its outline.

Run from the repo root:  uv run scripts/interfixra.py
"""

import copy
import hashlib
import io
import subprocess
import sys
import urllib.request
import zipfile
from pathlib import Path

from fontTools import subset
from fontTools.ttLib import TTFont
from fontTools.varLib import instancer

from symbolfonts import CODEPOINTS as SYMBOLS

ROOT = Path(__file__).resolve().parent.parent
TMP = ROOT / ".tmp"
OUT = ROOT / "dopeuikit/assets/fonts"
# The default face the alternative is measured against: its cmap is the coverage
# contract of a kit body font, so the two fonts answer the same text.
REFERENCE = OUT / "noto-sans-var.woff2"

FIXING_URL = "https://code.pecheny.me/pecheny/fonts-fixing.git"
FIXING_COMMIT = "bdc9db137b3646f1d1f57e0a3496e39635755d33"

INTER_URL = "https://github.com/rsms/inter/releases/download/v4.1/Inter-4.1.zip"
INTER_SHA256 = "9883fdd4a49d4fb66bd8177ba6625ef9a64aa45899767dde3d36aa425756b11e"

RAVEO_URL = "https://raw.githubusercontent.com/jakubfoglar/raveo/7f68a005a27123ffdefc9e62fddc6276e41face5/fonts/variable/RaveoVF.ttf"
RAVEO_SHA256 = "d4516d4882881bd824efc66844e8bde4843c466e2cb27fb2fe60277ec1b78feb"

# What the kit asks of a body face: the weights the design system names
# (--fw-regular 400 … --fw-bold 700) and the text optical size. Everything
# outside is interpolation the page can never ask for, and it is the bulk of a
# variable font's deltas — the same cut core.css documents for Noto Sans.
AXES = {"opsz": 14, "wght": (400, 400, 700)}
TAKEN = ("a", "one")  # the two glyphs Raveo draws differently, and Inter Fix RA takes


def fetch(url: str, sha256: str, name: str) -> bytes:
    cache = TMP / name
    if cache.exists():
        data = cache.read_bytes()
    else:
        data = urllib.request.urlopen(url, timeout=120).read()
        cache.parent.mkdir(exist_ok=True)
        cache.write_bytes(data)
    got = hashlib.sha256(data).hexdigest()
    if got != sha256:
        sys.exit(f"{name} checksum mismatch: {got} (delete {cache} if the pin moved)")
    return data


def fixing() -> Path:
    """The fonts-fixing checkout, at the pinned commit, importable."""
    path = TMP / "fonts-fixing"
    if not path.exists():
        subprocess.run(["git", "clone", "--quiet", FIXING_URL, str(path)], check=True)
    subprocess.run(["git", "-C", str(path), "checkout", "--quiet", FIXING_COMMIT], check=True)
    sys.path.insert(0, str(path))
    return path


def sources() -> tuple[bytes, bytes, bytes]:
    archive = zipfile.ZipFile(io.BytesIO(fetch(INTER_URL, INTER_SHA256, "Inter-4.1.zip")))
    return (archive.read("InterVariable.ttf"),
            archive.read("InterVariable-Italic.ttf"),
            fetch(RAVEO_URL, RAVEO_SHA256, "RaveoVF.ttf"))


def reload(font: TTFont) -> TTFont:
    """Round-trip through bytes: the instancer leaves gvar lazy over a glyph order
    it has already rewritten, and the next table to read it trips over a name."""
    buffer = io.BytesIO()
    font.save(buffer)
    buffer.seek(0)
    return TTFont(buffer)


def cut(data: bytes) -> TTFont:
    """Pin the optical size and clamp the weight to the range the kit names."""
    font = instancer.instantiateVariableFont(TTFont(io.BytesIO(data)), AXES, updateFontNames=False)
    if "HVAR" in font:  # Raveo ships without one; Inter's goes, see the module docstring
        del font["HVAR"]
    return reload(font)


def bake(font: TTFont, tag: str) -> None:
    """Make a one-to-one substitution feature the default form of each glyph it
    touches — outline, variations and advance, since this font has all three."""
    gsub, glyf, hmtx, gvar = font["GSUB"].table, font["glyf"], font["hmtx"], font["gvar"]
    for record in gsub.FeatureList.FeatureRecord:
        if record.FeatureTag != tag:
            continue
        for index in record.Feature.LookupListIndex:
            for subtable in gsub.LookupList.Lookup[index].SubTable:
                for src, dst in getattr(subtable, "mapping", {}).items():
                    glyf[src] = copy.deepcopy(glyf[dst])
                    hmtx[src] = hmtx[dst]
                    gvar.variations[src] = copy.deepcopy(gvar.variations.get(dst, []))


def take(font: TTFont, donor: TTFont, name: str) -> int:
    """Transplant one variable glyph, and widen whatever Inter draws on top of it.

    Raveo forks Inter, so the two fonts share the axes, the avar and the em: the
    donor's gvar tuples are already in this font's normalised space and travel with
    the outline. Only the composites are left — Cyrillic а and every accented a are
    drawn from a, and each must gain the width the new drawing takes.
    """
    glyf, hmtx, gvar = font["glyf"], font["hmtx"], font["gvar"]
    delta = donor["hmtx"][name][0] - hmtx[name][0]
    glyf[name] = copy.deepcopy(donor["glyf"][name])
    glyf[name].recalcBounds(glyf)
    hmtx[name] = (donor["hmtx"][name][0], glyf[name].xMin)
    gvar.variations[name] = copy.deepcopy(donor["gvar"].variations.get(name, []))
    for other in font.getGlyphOrder():
        composite = glyf[other]
        if composite.isComposite() and composite.components[0].glyphName == name:
            composite.recalcBounds(glyf)
            hmtx[other] = (hmtx[other][0] + delta, composite.xMin)
    return delta


def rename(font: TTFont) -> None:
    """Inter Fix RA, and not "Inter Fix RA Variable": one face over a weight range is
    what every face in this directory is, and the name is what core.css asks for."""
    name = font["name"]
    for record in name.names:
        value = str(record).replace("InterVariable", "Inter").replace("Inter Variable", "Inter")
        if record.nameID in (1, 4, 16):
            name.setName(value.replace("Inter", "Inter Fix RA"), record.nameID,
                         record.platformID, record.platEncID, record.langID)
        elif record.nameID in (3, 6):
            name.setName(value.replace("Inter", "InterFixRA"), record.nameID,
                         record.platformID, record.platEncID, record.langID)


def web(font: TTFont, path: Path) -> None:
    """Subset to what the default face covers, and write the woff2.

    The coverage contract is Noto Sans's cmap plus the symbol set the kit adds to it
    (scripts/symbolfonts.py): a reader who switches fonts must not lose a character.
    Inter answers 1793 of Noto's 2028 codepoints; the rest are archaic Cyrillic, the
    mathematical alphabets and the formatting controls, none of which a page of the
    suite sets, and "Noto Sans" stays behind this face in the stack for them.
    Hinting goes — every browser that takes woff2 rasterises with its own — and with
    it a third of the file.
    """
    wanted = set(TTFont(REFERENCE, lazy=True).getBestCmap()) | set(SYMBOLS)
    covered = sorted(wanted & set(font.getBestCmap()))
    options = subset.Options()
    options.layout_features = ["*"]
    options.name_IDs = ["*"]
    options.name_languages = ["*"]
    options.notdef_outline = True
    options.hinting = False
    options.drop_tables += ["DSIG"]
    subsetter = subset.Subsetter(options)
    subsetter.populate(unicodes=covered)
    subsetter.subset(font)
    font.flavor = "woff2"
    font.save(path)
    print(f"+ {path.relative_to(ROOT)} ({path.stat().st_size} bytes, "
          f"{font['maxp'].numGlyphs} glyphs, {len(covered)} codepoints)")


def main() -> None:
    fixing()
    from build_inter_fix import fix  # noqa: E402 — the pinned checkout is the dependency
    from respacing import space, summary  # noqa: E402
    import joblib  # noqa: E402

    checkout = TMP / "fonts-fixing"
    model = joblib.load(checkout / "spacing-model.joblib")
    pairs = joblib.load(checkout / "pair-model.joblib")
    roman_src, italic_src, raveo_src = sources()
    raveo = cut(raveo_src)

    for data, italic in ((roman_src, False), (italic_src, True)):
        font = cut(data)
        added, anchors, bar = fix(font)
        # The pause sign is drawn once, on the default instance, and gvar wants an
        # entry per glyph even when it is the empty one that says "never varies".
        font["gvar"].variations.setdefault("uni23F8", [])
        bake(font, "cv01")
        report = f"cyrl +{','.join(added) or 'nothing'}  anchors +{' '.join(anchors) or '0'}  pause {bar}"
        if not italic:
            deltas = [take(font, raveo, name) for name in TAKEN]
            report += "  " + "  ".join(f"{n} {d:+d}" for n, d in zip(TAKEN, deltas))
        stats = space(font, model, pairs)
        rename(font)
        print(f"{'italic' if italic else 'roman':7s} {report}\n        {summary(stats)}", flush=True)
        web(font, OUT / ("inter-fix-ra-var-italic.woff2" if italic else "inter-fix-ra-var.woff2"))


if __name__ == "__main__":
    main()
