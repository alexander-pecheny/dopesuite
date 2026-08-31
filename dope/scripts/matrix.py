#!/usr/bin/env python3
"""The verify skill's hand-over matrix, as a tool.

Shoots every page of the matrix (phone × desktop × light × dark) through
agent-browser, many workers on ONE Chrome, and pixel-diffs two runs.

    uv run --with pillow scripts/matrix.py run --db .tmp/verify/fest.db
    uv run --with pillow scripts/matrix.py shoot --label after --host http://127.0.0.1:9782
    uv run --with pillow scripts/matrix.py diff before after

`run` is the whole gate: HEAD checked out into a second worktree and built,
both servers started on a copy of the DB (HEAD on 9783, the working tree on
9782), both shot, diffed, everything cleaned up. `shoot` and `diff` are its
halves for a deployed host or a re-diff. `just matrix` wraps `run`.

Pages default to the studchr-2026 fest of the dopetest DB (see the
dopetest memory note for how to snapshot it); `--pages` takes a file of
`name|/path` lines instead.

Readiness is a contract the tool can check, not a guess per page: fonts
loaded, a content node present, no DOM mutation for a while, two painted
frames. Each worker session has a Chrome of its own, launched one at a time
(agent-browser 0.34's `connect` does not outlive the command that ran it, and
sessions sharing a browser race on tab binding); the Chromes are killed by
pid at the end because `close` does not always take one with it. Every page opens in
a fresh tab: over plain HTTP/1.1 (a local dev server; dopetest is h2) the
previous page's SSE stream outlives an in-place navigation and starves the
next one of connections. MATRIX_DEBUG=1 dumps a failed page's request log.
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

# The gallery is every shared table on one page from fixtures; dev servers only.
GALLERY = "gallery|/gallery"

PAGES = """
ek-grid|/fest/studchr-2026/game/2/
ek-venues|/fest/studchr-2026/game/2/venues
ek-reseed|/fest/studchr-2026/game/2/stage/s1-reseed
ek-stats|/fest/studchr-2026/game/2/stats
ek-roster|/fest/studchr-2026/game/2/roster
brain-grid|/fest/studchr-2026/game/3/#grid
brain-block1|/fest/studchr-2026/game/3/#block:s1
brain-de|/fest/studchr-2026/game/3/#block:s2
brain-reseed|/fest/studchr-2026/game/3/#reseed
brain-stats|/fest/studchr-2026/game/3/#stats
brain-roster|/fest/studchr-2026/game/3/#roster
si-grid|/fest/studchr-2026/game/4/
si-venues|/fest/studchr-2026/game/4/venues
si-groups|/fest/studchr-2026/game/4/stage/group-stage
si-reseed|/fest/studchr-2026/game/4/stage/reseeds
si-stats|/fest/studchr-2026/game/4/stats
tpsh-grid|/fest/studchr-2026/game/5/
tpsh-venues|/fest/studchr-2026/game/5/venues
tpsh-otbor|/fest/studchr-2026/game/5/stage/s1
tpsh-reseed|/fest/studchr-2026/game/5/stage/reseeds
tpsh-stats|/fest/studchr-2026/game/5/stats
"""

CELLS = [(device, theme) for device in ("phone", "desktop") for theme in ("light", "dark")]

# Ready: fonts in, something drawn, and the DOM quiet for 400 ms. The observer
# is installed on the first poll and reused.
READY = (
    'document.fonts.status === "loaded"'
    ' && document.querySelector(".grid-slot-cell, .results-table, .match-table, .roster-empty, .empty")'
    " && (window.__quiet ||= (() => { let t = performance.now();"
    " new MutationObserver(() => { t = performance.now(); })"
    ".observe(document, {subtree: true, childList: true, attributes: true, characterData: true});"
    " return () => performance.now() - t > 400; })())()"
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


DEVICES = {"phone": ("393", "852", "3"), "desktop": ("1280", "800", "1")}


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
    # content-visibility:auto is a rendering hint, not appearance: a capture can
    # run before Chrome has found a skipped box relevant and paint it blank.
    # Off for the shot, then two painted frames.
    ab(session, "eval", "document.head.appendChild(Object.assign(document.createElement('style'), {textContent: '* { content-visibility: visible !important }'})); new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r)))")
    # The page below its header: the topbar's viewer count (the workers
    # themselves move it) and the tab strip's scroll are not the subject, and
    # they are the two things that differ between two shots of one page.
    # Not a "full page" capture: that resizes the viewport under CDP mid-shot,
    # and the Сетка re-lays out on resize. The viewport becomes as tall as the
    # page, the page settles again, and a plain capture follows.
    height = ab(session, "eval", "String(Math.min(document.documentElement.scrollHeight, 16000))").strip('"')
    emulate(session, device, height)
    ab(session, "wait", "--fn", READY)
    ab(session, "eval", "new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r)))")
    header = int(ab(session, "eval", "Math.round(document.querySelector('main').getBoundingClientRect().top * devicePixelRatio)"))
    # A settled page gives the same pixels twice in a row; a capture that
    # outran the compositor does not — the shot counts when two captures agree.
    again = png.with_suffix(".again.png")
    ab(session, "screenshot", str(png))
    crop_top(png, header)
    for _ in range(4):
        ab(session, "wait", "300")
        ab(session, "screenshot", str(again))
        crop_top(again, header)
        if diff_pair(png, again) == "identical":
            again.unlink()
            return
        again.replace(png)
    raise RuntimeError("capture never settled")


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


def diff_pair(before, after):
    from PIL import Image, ImageChops

    a = Image.open(before).convert("RGB")
    b = Image.open(after).convert("RGB")
    if a.size != b.size:
        return f"size differs: {a.size} vs {b.size}"
    mask = ImageChops.difference(a, b).point(lambda p: 255 if p else 0).convert("L").point(lambda p: 255 if p else 0)
    box = mask.getbbox()
    if box is None:
        return "identical"
    return f"{mask.histogram()[255]} px differ, bbox {box}"


def diff(before_dir, after_dir):
    rows = []
    for after in sorted(Path(after_dir).glob("*.png")):
        before = Path(before_dir) / after.name
        rows.append((after.name, diff_pair(before, after) if before.exists() else "no before"))
    width = max(len(name) for name, _ in rows) if rows else 0
    for name, result in rows:
        print(f"{name:<{width}}  {result}")
    same = sum(1 for _, r in rows if r == "identical")
    print(f"\n{same} identical, {len(rows) - same} differ")
    return 0 if same == len(rows) else 1


class Server:
    """A dope-server built from `tree` (a checkout of the module) on `port`."""

    def __init__(self, tree, port, db, log):
        self.tree, self.port, self.log = Path(tree), port, log
        self.db = OUT / f"fest-{port}.db"
        shutil.copy(db, self.db)
        root = self.tree.parent
        self.log(f"building {self.tree} …")
        subprocess.run(["go", "-C", str(root / "scripts" / "webbuild"), "run", ".", "dope", "uikit"], check=True, capture_output=True)
        self.binary = OUT / f"dope-server-{port}"
        subprocess.run(["go", "build", "-o", str(self.binary), "./dope/cmd/dope-server"], cwd=self.tree, check=True)
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
    head_wt = OUT / "head-wt"
    subprocess.run(["git", "worktree", "remove", "--force", str(head_wt)], cwd=REPO, check=False, capture_output=True)
    subprocess.run(["git", "worktree", "add", "--detach", str(head_wt), args.base], cwd=REPO, check=True, capture_output=True)
    (head_wt / "node_modules").symlink_to(REPO / "node_modules")
    servers, hub = [], None
    try:
        before = Server(head_wt / "dope", 9783, args.db, log)
        after = Server(DOPE, 9782, args.db, log)
        servers = [before, after]
        hub = Fleet()
        t0 = time.time()
        # One host at a time: rendering is CPU-bound (software GL at DPR 3),
        # so more tabs than cores only buys CDP timeouts.
        shoot(hub, before.host, "before", pages, OUT / "shots" / "before", args.split, log)
        shoot(hub, after.host, "after", pages, OUT / "shots" / "after", args.split, log)
        log(f"shot {2 * len(pages) * len(CELLS)} pages in {time.time() - t0:.0f}s")
    finally:
        if hub:
            hub.close()
        for server in servers:
            server.stop()
        subprocess.run(["git", "worktree", "remove", "--force", str(head_wt)], cwd=REPO, check=False, capture_output=True)
    return diff(OUT / "shots" / "before", OUT / "shots" / "after")


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="cmd", required=True)
    p_run = sub.add_parser("run", help="HEAD against the working tree, end to end")
    p_run.add_argument("--db", default=str(OUT / "fest.db"), help="a DB to copy for both servers")
    p_run.add_argument("--base", default="HEAD", help="the git ref to shoot as before")
    p_run.add_argument("--pages", help="file of name|/path lines")
    p_run.add_argument("--split", type=int, default=1, help="workers per matrix cell")
    p_shoot = sub.add_parser("shoot", help="one host into .tmp/verify/shots/<label>")
    p_shoot.add_argument("--label", required=True)
    p_shoot.add_argument("--host", required=True)
    p_shoot.add_argument("--pages")
    p_shoot.add_argument("--split", type=int, default=1)
    p_shoot.add_argument("--gallery", action="store_true", help="add /gallery (a dev server)")
    p_diff = sub.add_parser("diff", help="pixel-diff two labels")
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
    return diff(OUT / "shots" / args.before, OUT / "shots" / args.after)


if __name__ == "__main__":
    sys.exit(main())
