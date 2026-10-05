"""Where the suite's font builds get their inputs: pinned downloads and fonts-fixing.

bodyfonts.py and handoutfonts.py both start from an upstream fetched by URL and
checked by sha256, and both run fonts-fixing's spacing models over it. This holds
the parts they share, so a pin or the fonts-fixing commit is set in one place.
"""

from __future__ import annotations

import hashlib
import io
import subprocess
import sys
import urllib.request
import zipfile
from pathlib import Path

from fontTools.ttLib import TTFont

ROOT = Path(__file__).resolve().parent.parent
TMP = ROOT / ".tmp"

# fonts-fixing (code.pecheny.me/pecheny/fonts-fixing) holds the fixes and the two
# models, and its fonts/ holds faces already built there.
FIXING_URL = "https://code.pecheny.me/pecheny/fonts-fixing.git"
FIXING_COMMIT = "6ab0f113860aac8c429e7a2ae1449b7db4778fcb"

DOWNLOAD_TIMEOUT = 180  # seconds


def fetch(name: str, url: str, digest: str) -> bytes:
    """A download, cached under .tmp by `name`, that must match its sha256."""
    cache = TMP / name
    if cache.exists():
        data = cache.read_bytes()
    else:
        data = urllib.request.urlopen(url, timeout=DOWNLOAD_TIMEOUT).read()
        cache.parent.mkdir(exist_ok=True)
        cache.write_bytes(data)
    got = hashlib.sha256(data).hexdigest()
    if got != digest:
        sys.exit(f"{name} checksum mismatch: {got} (delete {cache} if the pin moved)")
    return data


def member(archive: bytes, name: str, suffix: str) -> bytes:
    """The one member of a release zip whose path ends in `suffix`."""
    zf = zipfile.ZipFile(io.BytesIO(archive))
    hits = [n for n in zf.namelist() if n.endswith(suffix)]
    if len(hits) != 1:
        sys.exit(f"{name}: {len(hits)} members end in {suffix!r}")
    return zf.read(hits[0])


def fixing() -> Path:
    """The fonts-fixing checkout, at the pinned commit, importable."""
    path = TMP / "fonts-fixing"
    if not path.exists():
        subprocess.run(["git", "clone", "--quiet", FIXING_URL, str(path)], check=True)
    subprocess.run(["git", "-C", str(path), "fetch", "--quiet", "origin"], check=True)
    subprocess.run(["git", "-C", str(path), "checkout", "--quiet", FIXING_COMMIT], check=True)
    if str(path) not in sys.path:
        sys.path.insert(0, str(path))
    return path


def models():
    """fonts-fixing's two models: sidebearings from an outline, distances per pair."""
    import joblib

    checkout = fixing()
    return joblib.load(checkout / "spacing-model.joblib"), joblib.load(checkout / "pair-model.joblib")


def load(data) -> TTFont:
    """A font that will not restamp its own modification date.

    These faces are committed artifacts, and a rebuild that changes nothing should
    change no bytes — `head.modified` is written at save time, and the spacing
    passes save the font three times on their way through it.
    """
    font = TTFont(io.BytesIO(data) if isinstance(data, bytes) else data)
    font.recalcTimestamp = False
    return font
