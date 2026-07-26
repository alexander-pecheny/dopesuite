# /// script
# requires-python = ">=3.11"
# dependencies = ["pillow"]
# ///
"""Render dope's icon set from icon.png into dope/web/assets/static/.

    uv run dope/design/icon/gen-icon.py

The source is a 1080px transparent PNG of the green `do/pe` glyphs. Every
launcher surface wants a different framing, so each output is the same art on a
different tile: rounded and transparent-cornered for the manifest icons, square
and opaque for the ones iOS and Android crop themselves.
"""

import json
from pathlib import Path

from PIL import Image, ImageDraw

HERE = Path(__file__).resolve().parent
STATIC = HERE / "../../dope/web/assets/static"

BG = "#262a31"  # --structure, dark theme (dopeuikit/assets/core.css)
ART = 0.68  # art width as a fraction of the tile
SAFE = 0.78  # Android crops a maskable icon to this much of the tile
RADIUS = 0.18


def tile(px: int, art: float, radius: float = 0.0, opaque: bool = False, bg: str | None = BG) -> Image.Image:
    im = Image.new("RGBA", (px, px), (0, 0, 0, 0))
    if bg:
        ImageDraw.Draw(im).rounded_rectangle((0, 0, px - 1, px - 1), radius=radius * px, fill=bg)

    side = round(art * px)
    glyphs = SOURCE.copy()
    glyphs.thumbnail((side, side), Image.LANCZOS)
    im.alpha_composite(glyphs, ((px - glyphs.width) // 2, (px - glyphs.height) // 2))
    return im.convert("RGB") if opaque else im


SOURCE = Image.open(HERE / "icon.png").convert("RGBA")
SOURCE = SOURCE.crop(SOURCE.getbbox())

tile(192, ART, RADIUS).save(STATIC / "icon-192.png")
tile(512, ART, RADIUS).save(STATIC / "icon-512.png")
tile(180, ART, opaque=True).save(STATIC / "apple-touch-icon.png")
tile(512, ART * SAFE, opaque=True).save(STATIC / "icon-maskable.png")

# The tab favicon keeps the bare glyphs: a 16px tile would spend most of its
# pixels on the background. Squared first — an .ico frame is square, and Pillow
# would stretch the taller-than-wide art to fit.
tile(512, 1.0, bg=None).save(STATIC / "favicon.ico", sizes=[(48, 48), (32, 32), (16, 16)])

manifest = {
    "name": "dope — фесты ЧГК",
    "short_name": "dope",
    "lang": "ru",
    "start_url": "/",
    "scope": "/",
    "display": "browser",
    "background_color": BG,
    "theme_color": BG,
    "icons": [
        {"src": "/static/icon-192.png", "sizes": "192x192", "type": "image/png", "purpose": "any"},
        {"src": "/static/icon-512.png", "sizes": "512x512", "type": "image/png", "purpose": "any"},
        {"src": "/static/icon-maskable.png", "sizes": "512x512", "type": "image/png", "purpose": "maskable"},
    ],
}
(STATIC / "manifest.webmanifest").write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n")
print(f"wrote icon-192, icon-512, apple-touch-icon, icon-maskable, favicon.ico, manifest into {STATIC.resolve()}")
