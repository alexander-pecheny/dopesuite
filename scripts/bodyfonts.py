# /// script
# requires-python = ">=3.11"
# dependencies = ["fonttools>=4.50", "brotli", "uharfbuzz", "numpy", "joblib", "scikit-learn==1.9.0"]
# ///
"""Build the body faces a reader may set the suite in instead of Noto Sans.

Five of them, and every one arrives here the same way: a VARIABLE upstream, the
Russian faults of that upstream fixed, both spacing models run over it, the axes
cut to what the design system names, and the coverage cut to what the default
face answers. Only the first step differs per family, and that difference is the
`patch` of each Face below.

  inter-fix-ra        Inter, with Raveo's tailed a and flagless 1, the stress
                      anchors \u042e and \u044f never had, `cyrl` taught to run `mark`,
                      and the pause sign U+23F8 it draws no other way.
  ibm-plex-sans-fix   Plex anchors most of its Cyrillic and forgets \u0401 \u042d \u042e \u042f
  ibm-plex-serif-fix  (and their lowercase), so a stress mark on those lands
                      past the letter. Nothing else about Plex is wrong.
  literata-fix        Literata draws its Cyrillic from its Latin but does not
                      kern it from its Latin; fonts-fixing carries that kerning
                      across, and ships the variable faces already fixed.
  stix-two-text-fix   STIX Two Text needs no anchor and no transfer \u2014 what it
                      gets here is the models, like the rest.

The spacing is the whole reason this script exists rather than a `pyftsubset`
line: `respacing.py` reads every letter's outline and says what sidebearing it
calls for, then reads every PAIR and says how far apart the two should stand,
and that judgement is what these faces are missing for Russian \u2014 each was drawn
Latin-first and had its Cyrillic added into the spacing conventions of a script
it does not share. Both models are fonts-fixing's, at a pinned commit, and the
face's own tracking is the only number read off the font itself.

Three things a variable font makes approximate, and all three are accepted here:

  \u00b7 the models read the DEFAULT instance, so Regular's spacing and kerning carry
    across the whole weight range instead of each weight being judged on its own
    (the static builds in fonts-fixing do judge each one);
  \u00b7 an anchor or an outline this script adds carries no gvar, so the pause sign
    keeps Regular's bar width at every weight \u2014 one symbol glyph, and the kit's
    Noto Sans symbol subset beside it is a static face for the same reason;
  \u00b7 Inter's HVAR is dropped (its gvar carries the phantom points too, to the
    unit), so a transplanted glyph's advance cannot disagree with its outline.

Every upstream is pinned and checksummed, so a rerun is byte-stable. Run from
the repo root, and expect a couple of minutes per face:

    uv run scripts/bodyfonts.py              # all five
    uv run scripts/bodyfonts.py literata-fix # one, by id
"""

from __future__ import annotations

import copy
import hashlib
import io
import subprocess
import sys
import urllib.request
import zipfile
from dataclasses import dataclass
from pathlib import Path
from typing import Callable

from fontTools import subset
from fontTools.pens.boundsPen import BoundsPen
from fontTools.ttLib import TTFont
from fontTools.varLib import instancer

from symbolfonts import CODEPOINTS as SYMBOLS, DONOR_COMMIT as GOOGLE_FONTS_COMMIT

ROOT = Path(__file__).resolve().parent.parent
TMP = ROOT / ".tmp"
OUT = ROOT / "dopeuikit/assets/fonts"
# The default face every alternative is measured against: its cmap is the
# coverage contract of a kit body font, and core.css keeps it behind whichever
# face a reader picks, so what an alternative does not answer it still answers.
REFERENCE = OUT / "noto-sans-var.woff2"

# fonts-fixing (code.pecheny.me/pecheny/fonts-fixing) holds the fixes and the two
# models, and its fonts/ holds the one face here that is already built variable.
FIXING_URL = "https://code.pecheny.me/pecheny/fonts-fixing.git"
FIXING_COMMIT = "bdc9db137b3646f1d1f57e0a3496e39635755d33"

PINS = {
    # rsms/inter's release zip: InterVariable.ttf + InterVariable-Italic.ttf.
    "Inter-4.1.zip": (
        "https://github.com/rsms/inter/releases/download/v4.1/Inter-4.1.zip",
        "9883fdd4a49d4fb66bd8177ba6625ef9a64aa45899767dde3d36aa425756b11e",
    ),
    # Raveo's variable font \u2014 a fork of Inter over the same two axes with the same
    # avar, which is what lets its a and its 1 transplant whole, with their gvar.
    "RaveoVF.ttf": (
        "https://raw.githubusercontent.com/jakubfoglar/raveo/7f68a005a27123ffdefc9e62fddc6276e41face5/fonts/variable/RaveoVF.ttf",
        "d4516d4882881bd824efc66844e8bde4843c466e2cb27fb2fe60277ec1b78feb",
    ),
    "plex-sans-variable.zip": (
        "https://github.com/IBM/plex/releases/download/%40ibm%2Fplex-sans-variable%400.2.0/plex-sans-variable.zip",
        "f83825d527be6cd39c8971c932b9bf22688a3ad3e5ac6305b6143d02f52b87b6",
    ),
    "plex-serif-variable.zip": (
        "https://github.com/IBM/plex/releases/download/%40ibm%2Fplex-serif-variable%402.0.0/plex-serif-variable.zip",
        "87282fe6f7aa3c26149d8b4b709e035d0be2a9124b859e68d8046d8e786d9d30",
    ),
    # STIX ships its built fonts only inside dated archives; google/fonts carries
    # the two variable text faces, at the same commit symbolfonts.py pins for the
    # symbol donor.
    "STIXTwoText.ttf": (
        f"https://raw.githubusercontent.com/google/fonts/{GOOGLE_FONTS_COMMIT}/ofl/stixtwotext/STIXTwoText%5Bwght%5D.ttf",
        "7962b8b7811e6a896c9a91a0bccbb5241047770eb24d4997c5cb5fe21d5c0df2",
    ),
    "STIXTwoText-Italic.ttf": (
        f"https://raw.githubusercontent.com/google/fonts/{GOOGLE_FONTS_COMMIT}/ofl/stixtwotext/STIXTwoText-Italic%5Bwght%5D.ttf",
        "88c0e2e316eaff56eddc9e51e4850317e2a1e490bbf758b2dec4793aedba9c74",
    ),
}

# What the kit asks of a body face: the weights the design system names
# (--fw-regular 400 \u2026 --fw-bold 700) and the text optical size. Everything
# outside is interpolation the page can never ask for, and it is the bulk of a
# variable font's deltas \u2014 the same cut core.css documents for Noto Sans.
WEIGHTS = (400, 400, 700)


def fetch(name: str) -> bytes:
    url, digest = PINS[name]
    cache = TMP / name
    if cache.exists():
        data = cache.read_bytes()
    else:
        data = urllib.request.urlopen(url, timeout=180).read()
        cache.parent.mkdir(exist_ok=True)
        cache.write_bytes(data)
    got = hashlib.sha256(data).hexdigest()
    if got != digest:
        sys.exit(f"{name} checksum mismatch: {got} (delete {cache} if the pin moved)")
    return data


def unzip(archive: str, suffix: str) -> bytes:
    """The one member of a release zip whose path ends in `suffix`."""
    zf = zipfile.ZipFile(io.BytesIO(fetch(archive)))
    hits = [n for n in zf.namelist() if n.endswith(suffix)]
    if len(hits) != 1:
        sys.exit(f"{archive}: {len(hits)} members end in {suffix!r}")
    return zf.read(hits[0])


def fixing() -> Path:
    """The fonts-fixing checkout, at the pinned commit, importable."""
    path = TMP / "fonts-fixing"
    if not path.exists():
        subprocess.run(["git", "clone", "--quiet", FIXING_URL, str(path)], check=True)
    subprocess.run(["git", "-C", str(path), "checkout", "--quiet", FIXING_COMMIT], check=True)
    if str(path) not in sys.path:
        sys.path.insert(0, str(path))
    return path


def built(name: str) -> bytes:
    """A face fonts-fixing has already built and committed, from that checkout."""
    return (fixing() / "fonts" / name).read_bytes()


def load(data: bytes) -> TTFont:
    """A font that will not restamp its own modification date.

    These faces are committed artifacts, and a rebuild that changes nothing should
    change no bytes — `head.modified` is written at save time, and the spacing
    passes save the font three times on their way through it.
    """
    font = TTFont(io.BytesIO(data) if isinstance(data, bytes) else data)
    font.recalcTimestamp = False
    return font


def reload(font: TTFont) -> TTFont:
    """Round-trip through bytes: the instancer leaves gvar lazy over a glyph order
    it has already rewritten, and the next table to read it trips over a name."""
    buffer = io.BytesIO()
    font.save(buffer)
    buffer.seek(0)
    return load(buffer)


def cut(data: bytes, axes: dict, drop_hvar: bool = False) -> TTFont:
    """Pin every axis but the weight, and clamp that to the range the kit names."""
    font = instancer.instantiateVariableFont(load(data), axes, updateFontNames=False)
    font.recalcTimestamp = False
    if drop_hvar and "HVAR" in font:
        del font["HVAR"]
    return reload(font)


def rename(font: TTFont, old: str, new: str) -> None:
    """Rename the family, in the spaced form and the joined one."""
    joined_old, joined_new = old.replace(" ", ""), new.replace(" ", "")
    names = font["name"]
    for record in names.names:
        if record.nameID in (1, 4, 16):
            value = str(record).replace(old, new)
        elif record.nameID in (3, 6):
            value = str(record).replace(joined_old, joined_new)
        else:
            continue
        names.setName(value, record.nameID, record.platformID, record.platEncID, record.langID)


# ---- the per-family fixes ----------------------------------------------------


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
    """Transplant one variable glyph, and widen whatever the font draws on top of it.

    Raveo forks Inter, so the two share the axes, the avar and the em: the donor's
    gvar tuples are already in this font's normalised space and travel with the
    outline. Only the composites are left — Cyrillic а and every accented a are
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


PAUSE, STOP = "uni23F8", 0x25A0


def settle_pause(font: TTFont) -> None:
    """Give the drawn pause sign the per-glyph entries the rest of the font has.

    `pausefix` writes the outline, the advance and the cmap, which is all a
    static face needs. A variable one keeps three more things per glyph, and one
    the new glyph is missing from is a hole the compiler falls into: gvar (here
    the empty entry that says "never varies"), vmtx where the family has vertical
    metrics, and HVAR's map from glyph to the variation its advance takes. Each
    takes the stop sign's, that being the box the bars were drawn inside and the
    advance they were given. The vertical tables do not survive `web` below, but
    they have to survive the passes between here and it.
    """
    if PAUSE not in font.getGlyphOrder():
        return
    stop = font.getBestCmap()[STOP]
    if "gvar" in font:
        font["gvar"].variations.setdefault(PAUSE, [])
    if "vmtx" in font:
        font["vmtx"].metrics[PAUSE] = font["vmtx"].metrics[stop]
    if "HVAR" in font:
        for attr in ("AdvWidthMap", "LsbMap", "RsbMap"):
            mapping = getattr(font["HVAR"].table, attr, None)
            if mapping is not None:
                mapping.mapping[PAUSE] = mapping.mapping[stop]


def patch_inter(font: TTFont, italic: bool) -> str:
    from build_inter_fix import fix

    added, anchors, bar = fix(font)
    settle_pause(font)
    bake(font, "cv01")
    report = f"cyrl +{','.join(added) or 'nothing'}  anchors +{' '.join(anchors) or '0'}  pause {bar}"
    if not italic:
        # Raveo has no italics, and Inter's italic a is single-storey, so the
        # italic keeps Inter's a and takes only the 1 — through Inter's own cv01
        # above, which reaches the tabular and superscript forms Raveo never drew.
        raveo = cut(fetch("RaveoVF.ttf"), {"opsz": 14, "wght": WEIGHTS})
        deltas = [take(font, raveo, name) for name in ("a", "one")]
        report += "  " + "  ".join(f"{n} {d:+d}" for n, d in zip(("a", "one"), deltas))
    rename(font, "InterVariable", "Inter")
    rename(font, "Inter Variable", "Inter")
    rename(font, "Inter", "Inter Fix RA")
    return report


def patch_plex(font: TTFont, italic: bool, family: str) -> str:
    from acutefix import add_acute_anchors, enable_features
    from pausefix import add_pause

    # Plex draws its acute so that the designer's own anchors already read right,
    # so the added letters keep the font's mark anchor rather than the tip one.
    added = enable_features(font, "cyrl")
    anchors = add_acute_anchors(font, point_at_center=False)
    bar = add_pause(font)
    settle_pause(font)
    # "Var" is how Plex names the variable cut; the web has no static one to tell
    # it apart from, and "IBM Plex Sans Var Fix" would only name a build step.
    rename(font, f"IBM Plex {family} Var", f"IBM Plex {family} Fix")
    return f"cyrl +{','.join(added) or 'nothing'}  anchors +{' '.join(anchors) or '0'}  pause {bar}"


def patch_literata(font: TTFont, italic: bool) -> str:
    from pausefix import add_pause

    # Already Literata Fix: fonts-fixing carried the Latin kerning across to the
    # Cyrillic and ships the variable faces built. The pause is ours to draw —
    # it is the one family here that has the play and the stop to draw it from.
    bar = add_pause(font)
    settle_pause(font)
    return f"pause {bar}"


def patch_stix(font: TTFont, italic: bool) -> str:
    from pausefix import add_pause

    # Nothing to repair: STIX anchors every Cyrillic vowel and registers the
    # feature. The models below are the whole of what this face gets, and that
    # is why it still gets a name of its own.
    bar = add_pause(font)
    settle_pause(font)
    rename(font, "STIX Two Text", "STIX Two Text Fix")
    return f"pause {bar}"


# ---- the faces ---------------------------------------------------------------


@dataclass(frozen=True)
class Face:
    # id is the <html data-font> value, the file stem, and the argument this
    # script takes; family is the name table's and core.css's.
    id: str
    family: str
    axes: dict
    sources: Callable[[], tuple[bytes, bytes]]
    patch: Callable[[TTFont, bool], str]
    drop_hvar: bool = False


FACES = (
    Face(
        id="inter-fix-ra",
        family="Inter Fix RA",
        axes={"opsz": 14, "wght": WEIGHTS},
        sources=lambda: (unzip("Inter-4.1.zip", "InterVariable.ttf"),
                         unzip("Inter-4.1.zip", "InterVariable-Italic.ttf")),
        patch=patch_inter,
        # Its glyphs are transplanted and its advances move with them; Inter's
        # gvar carries the phantom points, so HVAR is a second opinion nobody
        # needs. The others are patched in GPOS only and keep theirs.
        drop_hvar=True,
    ),
    Face(
        id="ibm-plex-sans-fix",
        family="IBM Plex Sans Fix",
        axes={"wdth": 100, "wght": WEIGHTS},
        sources=lambda: (unzip("plex-sans-variable.zip", "ttf/IBM Plex Sans Var-Roman.ttf"),
                         unzip("plex-sans-variable.zip", "ttf/IBM Plex Sans Var-Italic.ttf")),
        patch=lambda font, italic: patch_plex(font, italic, "Sans"),
    ),
    Face(
        id="ibm-plex-serif-fix",
        family="IBM Plex Serif Fix",
        axes={"wght": WEIGHTS},
        sources=lambda: (unzip("plex-serif-variable.zip", "ttf/IBM Plex Serif Var-Roman.ttf"),
                         unzip("plex-serif-variable.zip", "ttf/IBM Plex Serif Var-Italic.ttf")),
        patch=lambda font, italic: patch_plex(font, italic, "Serif"),
    ),
    Face(
        id="literata-fix",
        family="Literata Fix",
        # Literata's optical size runs 7..72 and defaults to 12, which is the
        # text end of it; a page of the suite is set at 13..17px and asks for
        # nothing else.
        axes={"opsz": 12, "wght": WEIGHTS},
        sources=lambda: (built("LiterataFix/LiterataFix[opsz,wght].ttf"),
                         built("LiterataFix/LiterataFix-Italic[opsz,wght].ttf")),
        patch=patch_literata,
    ),
    Face(
        id="stix-two-text-fix",
        family="STIX Two Text Fix",
        axes={"wght": WEIGHTS},
        sources=lambda: (fetch("STIXTwoText.ttf"), fetch("STIXTwoText-Italic.ttf")),
        patch=patch_stix,
    ),
)


def web(font: TTFont, path: Path) -> None:
    """Subset to what the default face covers, and write the woff2.

    The coverage contract is Noto Sans's cmap plus the symbol set the kit adds to
    it (scripts/symbolfonts.py): a reader who switches fonts must not lose a
    character. None of these answers all of it — the archaic Cyrillic, the
    mathematical alphabets and the formatting controls are nobody's — and what is
    missing comes from "Noto Sans", which core.css keeps behind every one of
    them. Hinting goes: every browser that takes woff2 rasterises with its own.
    """
    wanted = set(TTFont(REFERENCE, lazy=True).getBestCmap()) | set(SYMBOLS)
    font.recalcTimestamp = False
    covered = sorted(wanted & set(font.getBestCmap()))
    options = subset.Options()
    options.layout_features = ["*"]
    options.name_IDs = ["*"]
    options.name_languages = ["*"]
    options.notdef_outline = True
    options.hinting = False
    # Vertical metrics are for vertical writing, which no page of the suite sets;
    # in Literata's italic they are also a table the new pause glyph cannot join
    # (its VVAR maps glyph id straight to variation index, so a 1770th glyph has
    # nowhere to point).
    options.drop_tables += ["DSIG", "vhea", "vmtx", "VVAR", "VORG"]
    subsetter = subset.Subsetter(options)
    subsetter.populate(unicodes=covered)
    subsetter.subset(font)
    font.flavor = "woff2"
    font.save(path)
    print(f"+ {path.relative_to(ROOT)} ({path.stat().st_size} bytes, "
          f"{font['maxp'].numGlyphs} glyphs, {len(covered)} codepoints)")


def xheight(path: Path) -> float:
    """The height of x at weight 400, as a fraction of the em.

    Not OS/2.sxHeight, which a font may round, leave stale or not set at all: the
    ink is what a reader sees, so the ink is what is measured.
    """
    font = instancer.instantiateVariableFont(TTFont(path), {"wght": 400}, updateFontNames=False)
    glyphs = font.getGlyphSet()
    pen = BoundsPen(glyphs)
    glyphs[font.getBestCmap()[ord("x")]].draw(pen)
    return pen.bounds[3] / font["head"].unitsPerEm


def size_adjust(path: Path) -> float:
    """What core.css must scale this face by to read as the same size as the default.

    Two faces at one font-size look the same size when their x-heights agree, not
    when their ems do — STIX sets x at 0.473 of the em against Noto Sans's 0.536,
    and at 17px that is text a reader reads as smaller. The number belongs in the
    @font-face as `size-adjust`, and this prints it on every build so the
    stylesheet can be checked against the file it describes.
    """
    return xheight(REFERENCE) / xheight(path) * 100


def build(face: Face, model, pairs) -> None:
    from respacing import space, summary

    roman, italic = face.sources()
    for data, is_italic in ((roman, False), (italic, True)):
        font = cut(data, face.axes, drop_hvar=face.drop_hvar)
        report = face.patch(font, is_italic)
        stats = space(font, model, pairs)
        style = "italic" if is_italic else "roman"
        print(f"{face.id} {style:6s} {report}\n        {summary(stats)}", flush=True)
        path = OUT / f"{face.id}{'-italic' if is_italic else ''}.woff2"
        web(font, path)
        if not is_italic:
            # One value per family, read off the roman: an italic run inside a
            # roman one must not change size, whatever its own x-height does.
            print(f"        size-adjust: {size_adjust(path):.1f}%  (core.css)", flush=True)


def main() -> None:
    wanted = sys.argv[1:]
    known = {face.id: face for face in FACES}
    unknown = [name for name in wanted if name not in known]
    if unknown:
        sys.exit(f"no such face: {', '.join(unknown)} (have {', '.join(known)})")

    checkout = fixing()
    import joblib  # noqa: E402 — the pinned checkout is where the models live

    model = joblib.load(checkout / "spacing-model.joblib")
    pairs = joblib.load(checkout / "pair-model.joblib")
    for face in FACES:
        if not wanted or face.id in wanted:
            build(face, model, pairs)


if __name__ == "__main__":
    main()
