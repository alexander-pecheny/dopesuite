#!/usr/bin/env python3
"""The verify skill's hand-over matrix, as a per-commit gate.

Seeds a fixture database, serves the working tree on it, shoots every page of
the matrix (phone × desktop × light × dark) through agent-browser, and
pixel-diffs each shot against the golden committed beside this script.

    uv run --with pillow scripts/matrix.py run
    uv run --with pillow scripts/matrix.py run --bless      # adopt what it saw
    uv run --with pillow scripts/matrix.py shoot --label after --host https://dopetest.pecheny.me
    uv run --with pillow scripts/matrix.py diff goldens after

`just matrix` wraps `run`. It depends on nothing but this checkout: the fest it
shoots is built by `dope-server seed-fixture` (dope/domain/fixture) in about a
second, so there is no snapshot to fetch and no staging database to keep in
step. A differing page is a finding; `--bless` is how an intended change lands,
and the diff in the goldens is then part of the commit that caused it.

Goldens are shot at DPR 1 and cropped to one viewport, which is what keeps them
small enough to commit on every UI change. That trades away the DPR-3
hairline detail — run `shoot` against a deployed host and `diff` by hand when a
change is about rendering rather than layout.

Readiness is a contract the tool can check, not a guess per page: fonts loaded,
a content node present, no DOM mutation for a while, two painted frames. Each
worker session has a Chrome of its own, launched one at a time (agent-browser
0.34's `connect` does not outlive the command that ran it, and sessions sharing
a browser race on tab binding); the Chromes are killed by pid at the end
because `close` does not always take one with it. Every page opens in a fresh
tab: over plain HTTP/1.1 (a local dev server) the previous page's SSE stream
outlives an in-place navigation and starves the next one of connections.
MATRIX_DEBUG=1 dumps a failed page's request log.
"""

import argparse
import os
import shutil
import signal
import subprocess
import sys
import threading
import time
import urllib.request
from pathlib import Path

DOPE = Path(__file__).resolve().parent.parent
REPO = DOPE.parent
OUT = REPO / ".tmp" / "verify"
GOLDENS = DOPE / "scripts" / "matrix-goldens"

# The fixture's fest and its games, in the order dope/domain/fixture builds
# them. A game is reached by id because the fixture gives none of them a slug.
FEST = "fixture"

# The gallery is every shared table on one page from fixtures; dev servers only.
GALLERY = "gallery|/gallery"

PAGES = f"""
ek-grid|/fest/{FEST}/game/1/
ek-venues|/fest/{FEST}/game/1/venues
ek-stats|/fest/{FEST}/game/1/stats
ek-roster|/fest/{FEST}/game/1/roster
brain-grid|/fest/{FEST}/game/2/#grid
brain-block1|/fest/{FEST}/game/2/#block:s1
brain-protocol|/fest/{FEST}/game/2/#protocol:s1
brain-reseed|/fest/{FEST}/game/2/#reseed
brain-stats|/fest/{FEST}/game/2/#stats
brain-roster|/fest/{FEST}/game/2/#roster
si-grid|/fest/{FEST}/game/3/
si-groups|/fest/{FEST}/game/3/stage/group-stage
si-reseed|/fest/{FEST}/game/3/stage/reseeds
si-stats|/fest/{FEST}/game/3/stats
troika-grid|/fest/{FEST}/game/4/#grid
troika-block1|/fest/{FEST}/game/4/#block:s1
troika-stats|/fest/{FEST}/game/4/#stats
od-results|/fest/{FEST}/game/5/#results
od-detailed|/fest/{FEST}/game/5/#detailed
od-roster|/fest/{FEST}/game/5/#roster
ksi-detailed|/fest/{FEST}/game/6/#detailed
ksi-results|/fest/{FEST}/game/6/#results
ksi-roster|/fest/{FEST}/game/6/#roster
ksi-stickers|/fest/{FEST}/game/7/#detailed
multi-detailed|/fest/{FEST}/game/8/#detailed
multi-results|/fest/{FEST}/game/8/#results
fest|/fest/{FEST}
"""

CELLS = [(device, theme) for device in ("phone", "desktop") for theme in ("light", "dark")]

# Ready: fonts in, something drawn, and the DOM quiet for 400 ms. The observer
# is installed on the first poll and reused.
READY = (
    'document.fonts.status === "loaded"'
    ' && document.querySelector(".grid-slot-cell, .results-table, .match-table, .roster-empty, .empty,'
    ' .list-row, .roster-team, .si-table, .standings-table")'
    " && (window.__quiet ||= (() => { let t = performance.now();"
    " new MutationObserver(() => { t = performance.now(); })"
    ".observe(document, {subtree: true, childList: true, attributes: true, characterData: true});"
    " return () => performance.now() - t > 400; })())()"
)


SETTLE = (
    "document.head.appendChild(Object.assign(document.createElement('style'),"
    " {textContent: '* { content-visibility: visible !important }'}));"
    # A scrollable row settles wherever the page last put it, and half a pixel
    # of horizontal scroll is a different antialiasing of the same text — the
    # one thing that made two runs of an identical tree differ.
    " document.querySelectorAll('*').forEach(el => { el.scrollLeft = 0; el.scrollTop = 0; });"
    " window.scrollTo(0, 0);"
    " new Promise(r => requestAnimationFrame(() => requestAnimationFrame(() =>"
    " r(Math.round(document.querySelector('main').getBoundingClientRect().top * devicePixelRatio)))))"
)


def ab(session, *args, check=True):
    env = dict(os.environ, AGENT_BROWSER_SESSION=session)
    proc = subprocess.run(["agent-browser", *args], env=env, capture_output=True, text=True)
    out = (proc.stdout + proc.stderr).strip()
    if check and (proc.returncode != 0 or out.startswith("✗")):
        raise RuntimeError(f"agent-browser {' '.join(args)}: {out}")
    return out


def pages_from(path, gallery=False):
    text = Path(path).read_text() if path else (GALLERY + PAGES if gallery else PAGES)
    return [tuple(line.split("|", 1)) for line in text.strip().splitlines() if line.strip()]


def chrome_pids():
    """The browser processes (not renderers) of every headless Chrome."""
    out = subprocess.run(["pgrep", "-f", "agent-browser-chrome"], capture_output=True, text=True).stdout.split()
    pids = set()
    for pid in out:
        try:
            if "--type=" not in Path(f"/proc/{pid}/cmdline").read_bytes().decode(errors="replace"):
                pids.add(int(pid))
        except OSError:
            pass
    return pids


class Fleet:
    """One Chrome per worker session, launched one at a time. agent-browser
    0.34's `connect` does not outlive the command that ran it — a worker's next
    `open` launches a Chrome of its own anyway — and sessions sharing one
    browser race on tab binding; four Chromes booting at once on four cores
    time each other out, so the first `open` of a session takes the lock."""

    launch = threading.Lock()

    def __init__(self):
        self.before = chrome_pids()
        self.workers = []

    def worker(self, name):
        if name not in self.workers:
            self.workers.append(name)
        return name

    def close(self):
        for name in self.workers:
            ab(name, "close", check=False)
        # `close` ends the session; the Chrome behind it sometimes survives.
        for pid in chrome_pids() - self.before:
            os.kill(pid, signal.SIGTERM)


def worker_host(host, index):
    """A loopback alias per worker: the workers share one Chrome, and Chrome
    allows six HTTP/1.1 connections per host — every dope page holds an SSE
    stream, so eight tabs on 127.0.0.1 starve the seventh navigation."""
    return host.replace("127.0.0.1", f"127.0.0.{index + 1}") if "127.0.0.1" in host else host


# Goldens are DPR 1 and one viewport tall. At DPR 3 and full page height a
# single page costs about a megabyte across the four cells, which is not a
# thing to commit on every UI change; this is about forty kilobytes and still
# catches layout, overflow and skin regressions.
DEVICES = {"phone": ("393", "852", "1"), "desktop": ("1280", "800", "1")}


def emulate(session, device, height=None):
    width, default_height, dpr = DEVICES[device]
    ab(session, "set", "viewport", width, height or default_height, dpr)


def shoot_cell(host, label, device, theme, pages, out, session, log):
    with Fleet.launch:
        emulate(session, device)
        ab(session, "open", f"{host}/")
    ab(session, "eval", f"localStorage.setItem('dope-theme','{theme}')")
    for name, path in pages:
        t0 = time.time()
        try:
            shoot_page(host, path, out / f"{name}-{device}-{theme}.png", session, device)
            log(f"{label} {name} {device} {theme}: {time.time() - t0:.1f}s")
        except RuntimeError as err:
            log(f"{label} {name} {device} {theme}: FAILED — {err}")
            if os.environ.get("MATRIX_DEBUG"):
                log("REQUESTS " + ab(session, "network", "requests", check=False)[-1500:])


def shoot_page(host, path, png, session, device):
    # Each page in a fresh tab, the previous tab closed: over plain HTTP/1.1
    # the old page's SSE stream and in-flight fetches outlive an in-place
    # navigation long enough to starve the next one of connections. A new
    # tab does not inherit the emulation, so it is set again.
    tabs = [line.split("[")[1].split("]")[0] for line in ab(session, "tab").splitlines() if "[t" in line]
    ab(session, "tab", "new")
    emulate(session, device)
    ab(session, "open", f"{host}{path}")
    for tab in tabs:
        ab(session, "tab", "close", tab, check=False)
    if ab(session, "eval", "Boolean(document.querySelector('main'))") != "true":
        raise RuntimeError("no page")
    ab(session, "wait", "--fn", READY)
    # One eval, not three: every agent-browser call is a process, and at ten of
    # them per shot the CLI round trips cost more than the rendering does. It
    # turns content-visibility off (a rendering hint, not appearance: a capture
    # can run before Chrome has found a skipped box relevant and paint it
    # blank), waits two painted frames, and reports where the page starts.
    header = int(ab(session, "eval", SETTLE))
    # The page below its header: the topbar's viewer count (the workers
    # themselves move it) and the tab strip's scroll are not the subject, and
    # they are the two things that differ between two shots of one page.
    # One viewport tall, not the whole page: a golden is a fingerprint of the
    # skin, and the first screen carries it at a fraction of the bytes.
    ab(session, "screenshot", str(png))
    crop_top(png, header)


def shoot(fleet, host, label, pages, out, split, log):
    shutil.rmtree(out, ignore_errors=True)
    out.mkdir(parents=True)
    threads = []
    for index, (device, theme) in enumerate(CELLS):
        for part in range(split):
            chunk = pages[part::split]
            session = fleet.worker(f"matrix-{device}-{theme}-{part}")
            cell_host = worker_host(host, index * split + part)
            thread = threading.Thread(target=shoot_cell, args=(cell_host, label, device, theme, chunk, out, session, log))
            thread.start()
            threads.append(thread)
    for thread in threads:
        thread.join()


def crop_top(path, pixels):
    from PIL import Image

    with Image.open(path) as im:
        cropped = im.crop((0, pixels, im.width, im.height))
    cropped.save(path)


# The Сетка on a phone antialiases a handful of pixels differently between two
# runs of the same tree — the layout is measured in JS and the last fraction of
# a pixel depends on when it ran. A real change to a skin moves thousands, so a
# page counts as unchanged below this and the count is printed anyway.
TOLERANCE = 64


def diff_pair(before, after, tolerance=0):
    from PIL import Image, ImageChops

    a = Image.open(before).convert("RGB")
    b = Image.open(after).convert("RGB")
    if a.size != b.size:
        return f"size differs: {a.size} vs {b.size}"
    mask = ImageChops.difference(a, b).point(lambda p: 255 if p else 0).convert("L").point(lambda p: 255 if p else 0)
    box = mask.getbbox()
    if box is None:
        return "identical"
    pixels = mask.histogram()[255]
    if pixels <= tolerance:
        return f"identical ({pixels} px of noise)"
    return f"{pixels} px differ, bbox {box}"


def diff(before_dir, after_dir, expected=None):
    rows = []
    for after in sorted(Path(after_dir).glob("*.png")):
        before = Path(before_dir) / after.name
        rows.append((after.name, diff_pair(before, after, TOLERANCE) if before.exists() else "no golden"))
    shot = {name for name, _ in rows}
    for golden in sorted(Path(before_dir).glob("*.png")):
        if golden.name not in shot:
            rows.append((golden.name, "not shot — the page is gone or it failed"))
    width = max((len(name) for name, _ in rows), default=0)
    for name, result in rows:
        print(f"{name:<{width}}  {result}")
    same = sum(1 for _, r in rows if r.startswith("identical"))
    print(f"\n{same} identical, {len(rows) - same} differ")
    if expected and same != expected:
        print(f"expected {expected} pages, saw {same} — run with --bless if that is the change")
    return 0 if same == len(rows) and rows else 1


def bless(shots, goldens):
    shutil.rmtree(goldens, ignore_errors=True)
    goldens.mkdir(parents=True)
    count = 0
    for png in sorted(Path(shots).glob("*.png")):
        shutil.copy(png, goldens / png.name)
        count += 1
    print(f"blessed {count} goldens in {goldens}")
    return 0


class Server:
    """A dope-server built from `tree` (a checkout of the module) on `port`,
    serving a database it seeds itself."""

    def __init__(self, tree, port, log, db=None):
        self.tree, self.port, self.log = Path(tree), port, log
        self.db = OUT / f"fest-{port}.db"
        root = self.tree.parent
        self.log(f"building {self.tree} …")
        subprocess.run(["go", "-C", str(root / "scripts" / "webbuild"), "run", ".", "dope", "uikit"], check=True, capture_output=True)
        self.binary = OUT / f"dope-server-{port}"
        subprocess.run(["go", "build", "-o", str(self.binary), "./dope/cmd/dope-server"], cwd=self.tree, check=True)
        for stale in OUT.glob(f"fest-{port}.db*"):
            stale.unlink()
        if db:
            shutil.copy(db, self.db)
        else:
            # The fixture is the binary's own: seeded by the tree under test,
            # so a schema change needs no snapshot refreshed anywhere.
            subprocess.run([str(self.binary), "seed-fixture", "-db", str(self.db)], cwd=self.tree, check=True, capture_output=True)
        env = dict(os.environ, DOPE_DB=str(self.db), PORT=str(port), DOPE_ENV="development")
        self.proc = subprocess.Popen([str(self.binary)], cwd=self.tree, env=env, stdout=(OUT / f"server-{port}.log").open("w"), stderr=subprocess.STDOUT)
        for _ in range(100):
            try:
                urllib.request.urlopen(f"http://127.0.0.1:{port}/", timeout=1)
                break
            except Exception:
                time.sleep(0.2)
        else:
            raise RuntimeError(f"server on {port} did not come up; see {OUT}/server-{port}.log")
        self.host = f"http://127.0.0.1:{port}"

    def stop(self):
        self.proc.send_signal(signal.SIGTERM)
        self.proc.wait(timeout=10)


def run(args):
    log = lambda line: print(line, file=sys.stderr, flush=True)
    OUT.mkdir(parents=True, exist_ok=True)
    pages = pages_from(args.pages, gallery=True)
    shots = OUT / "shots" / "now"
    server, hub = None, None
    try:
        server = Server(DOPE, 9782, log, db=args.db)
        hub = Fleet()
        t0 = time.time()
        shoot(hub, server.host, "shot", pages, shots, args.split, log)
        log(f"shot {len(pages) * len(CELLS)} pages in {time.time() - t0:.0f}s")
    finally:
        if hub:
            hub.close()
        if server:
            server.stop()
    if args.bless:
        return bless(shots, GOLDENS)
    if not GOLDENS.exists():
        print(f"no goldens yet in {GOLDENS} — run `just matrix --bless` once to adopt what you see")
        return 1
    return diff(GOLDENS, shots, expected=len(pages) * len(CELLS))


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="cmd", required=True)
    p_run = sub.add_parser("run", help="shoot the working tree and diff against the goldens")
    p_run.add_argument("--bless", action="store_true", help="adopt what was shot as the new goldens")
    p_run.add_argument("--db", help="serve this DB instead of a freshly seeded fixture")
    p_run.add_argument("--pages", help="file of name|/path lines")
    p_run.add_argument("--split", type=int, default=1, help="workers per matrix cell")
    p_shoot = sub.add_parser("shoot", help="one host into .tmp/verify/shots/<label>")
    p_shoot.add_argument("--label", required=True)
    p_shoot.add_argument("--host", required=True)
    p_shoot.add_argument("--pages")
    p_shoot.add_argument("--split", type=int, default=1)
    p_shoot.add_argument("--gallery", action="store_true", help="add /gallery (a dev server)")
    p_diff = sub.add_parser("diff", help="pixel-diff two labels; `goldens` names the committed set")
    p_diff.add_argument("before")
    p_diff.add_argument("after")
    args = parser.parse_args()
    if args.cmd == "run":
        return run(args)
    if args.cmd == "shoot":
        log = lambda line: print(line, file=sys.stderr, flush=True)
        hub = Fleet()
        try:
            t0 = time.time()
            pages = pages_from(args.pages, gallery=args.gallery)
            shoot(hub, args.host.rstrip("/"), args.label, pages, OUT / "shots" / args.label, args.split, log)
            log(f"shot {len(pages) * len(CELLS)} pages in {time.time() - t0:.0f}s")
        finally:
            hub.close()
        return 0
    where = lambda label: GOLDENS if label == "goldens" else OUT / "shots" / label
    return diff(where(args.before), where(args.after))


if __name__ == "__main__":
    sys.exit(main())
