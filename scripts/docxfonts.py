# /// script
# requires-python = ">=3.11"
# ///
"""Embed xy's four Noto Sans faces in a docx template.

The .docx export embeds its face in template.docx, so a reader without Noto
Sans still sets the questions in it. The template came with stock Noto Sans
2.015; the .pdf export and the handouts use the faces handoutfonts.py builds,
respaced and with the symbols merged in. This puts those faces in the template
as well, so every export draws the same glyphs, and the width a handout box is
given (measured in the Regular face) is the width it is drawn at.

Word stores an embedded face obfuscated: the first 32 bytes are XORed with the
font key fontTable.xml names, a GUID read as 16 bytes from its last hex pair
to its first. Every other part of the template is copied as it is.

Run from the repo root, after handoutfonts.py:

    uv run scripts/docxfonts.py [template.docx ...]

With no argument it rewrites xy's own template.
"""

from __future__ import annotations

import re
import sys
import zipfile
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
FACES = ROOT / "xy/internal/chgk/handout/assets"
TEMPLATE = ROOT / "xy/internal/chgk/docx/assets/template.docx"

OBFUSCATED_BYTES = 32
KEY_BYTES = 16

EMBED = re.compile(r'<w:embed(\w+) r:id="([^"]+)" w:fontKey="\{([0-9A-Fa-f-]+)\}"')
RELATIONSHIP = re.compile(r'Id="([^"]+)"[^>]*Target="([^"]+)"')


def font_key(guid: str) -> bytes:
    digits = guid.replace("-", "")
    return bytes(
        int(digits[len(digits) - 2 * (i + 1) : len(digits) - 2 * i], 16)
        for i in range(KEY_BYTES)
    )


def obfuscate(face: bytes, guid: str) -> bytes:
    key = font_key(guid)
    out = bytearray(face)
    for i in range(OBFUSCATED_BYTES):
        out[i] ^= key[i % KEY_BYTES]
    return bytes(out)


def embedded_parts(zf: zipfile.ZipFile) -> dict[str, bytes]:
    """Each embedded face's part name, mapped to the face that replaces it."""
    table = zf.read("word/fontTable.xml").decode("utf-8")
    rels = zf.read("word/_rels/fontTable.xml.rels").decode("utf-8")
    targets = dict(RELATIONSHIP.findall(rels))
    parts = {}
    for style, rid, guid in EMBED.findall(table):
        face = (FACES / f"NotoSans-{style}.ttf").read_bytes()
        parts["word/" + targets[rid]] = obfuscate(face, guid)
    return parts


def rewrite(template: Path) -> None:
    with zipfile.ZipFile(template) as zf:
        parts = embedded_parts(zf)
        entries = [(info, zf.read(info)) for info in zf.infolist()]
    with zipfile.ZipFile(template, "w") as zf:
        for info, data in entries:
            zf.writestr(info, parts.get(info.filename, data))
    print(f"+ {template}: {', '.join(sorted(parts))}")


def main() -> None:
    templates = [Path(p) for p in sys.argv[1:]] or [TEMPLATE]
    for template in templates:
        rewrite(template)


if __name__ == "__main__":
    main()
