#!/usr/bin/env -S uv run --quiet --with websocket-client --with requests python
"""Tiny CDP client for driving Comet/Chrome with --remote-debugging-port=9222.

Comet keeps an internal CDP client attached to its own tabs, so we use a
dedicated tab opened via /json/new. The tab id is stored in /tmp/cdp-tab.txt
and reused across calls.

Usage:
  cdp.py navigate <url>
  cdp.py reload
  cdp.py eval <js>
  cdp.py click <css>
  cdp.py wait <css>
  cdp.py screenshot <out.png>
  cdp.py size <width> <height>
  cdp.py reset
"""
import base64
import functools
import json
import os
import sys
import time

import requests
import websocket

print = functools.partial(print, flush=True)

CDP_HOST = os.environ.get("CDP_HOST", "localhost:9222")
TAB_FILE = "/tmp/cdp-tab.txt"


def http_get_json(path):
    return requests.get(f"http://{CDP_HOST}{path}", timeout=5).json()


def http_put_json(path):
    return requests.put(f"http://{CDP_HOST}{path}", timeout=5).json()


def find_tab(tab_id):
    for t in http_get_json("/json/list"):
        if t.get("id") == tab_id and t.get("type") == "page":
            return t
    return None


def open_tab(url="about:blank"):
    t = http_put_json(f"/json/new?{url}")
    with open(TAB_FILE, "w") as fh:
        fh.write(t["id"])
    return t


def ensure_tab():
    if os.path.exists(TAB_FILE):
        with open(TAB_FILE) as fh:
            tid = fh.read().strip()
        existing = find_tab(tid)
        if existing:
            return existing
    return open_tab()


class Client:
    def __init__(self, ws_url):
        self.ws = websocket.create_connection(ws_url, suppress_origin=True, timeout=20)
        self._id = 0

    def call(self, method, params=None):
        self._id += 1
        self.ws.send(json.dumps({"id": self._id, "method": method, "params": params or {}}))
        while True:
            d = json.loads(self.ws.recv())
            if d.get("id") == self._id:
                if "error" in d:
                    raise RuntimeError(d["error"])
                return d.get("result", {})

    def wait_ready(self, timeout=15):
        deadline = time.time() + timeout
        while time.time() < deadline:
            r = self.call("Runtime.evaluate", {"expression": "document.readyState", "returnByValue": True})
            if r.get("result", {}).get("value") == "complete":
                return True
            time.sleep(0.2)
        return False

    def close(self):
        try:
            self.ws.close()
        except Exception:
            pass


def main():
    args = sys.argv[1:]
    if not args:
        print(__doc__)
        return
    cmd = args[0]

    if cmd == "reset":
        if os.path.exists(TAB_FILE):
            with open(TAB_FILE) as fh:
                tid = fh.read().strip()
            try:
                requests.get(f"http://{CDP_HOST}/json/close/{tid}", timeout=5)
            except Exception:
                pass
            os.remove(TAB_FILE)
        print("ok")
        return

    tab = ensure_tab()
    c = Client(tab["webSocketDebuggerUrl"])
    try:
        if cmd == "navigate":
            c.call("Page.navigate", {"url": args[1]})
            c.wait_ready()
            href = c.call("Runtime.evaluate", {"expression": "location.href", "returnByValue": True})["result"]["value"]
            print(href)
        elif cmd == "reload":
            c.call("Page.reload")
            c.wait_ready()
            print("ok")
        elif cmd == "eval":
            r = c.call("Runtime.evaluate", {"expression": args[1], "returnByValue": True, "awaitPromise": True})
            res = r.get("result", {})
            v = res.get("value", res)
            print(json.dumps(v, ensure_ascii=False))
        elif cmd == "click":
            js = f"""(() => {{
                const el = document.querySelector({json.dumps(args[1])});
                if (!el) return false;
                el.scrollIntoView({{block:'center'}});
                el.click();
                return true;
            }})()"""
            r = c.call("Runtime.evaluate", {"expression": js, "returnByValue": True})
            print(r["result"].get("value"))
        elif cmd == "wait":
            sel = args[1]
            deadline = time.time() + 10
            while time.time() < deadline:
                js = f"document.querySelector({json.dumps(sel)}) !== null"
                r = c.call("Runtime.evaluate", {"expression": js, "returnByValue": True})
                if r["result"].get("value"):
                    print("ok")
                    return
                time.sleep(0.2)
            print("timeout", file=sys.stderr)
            sys.exit(2)
        elif cmd == "screenshot":
            r = c.call("Page.captureScreenshot", {"format": "png"})
            with open(args[1], "wb") as fh:
                fh.write(base64.b64decode(r["data"]))
            print(args[1])
        elif cmd == "size":
            w, h = int(args[1]), int(args[2])
            c.call("Emulation.setDeviceMetricsOverride", {"width": w, "height": h, "deviceScaleFactor": 2, "mobile": False})
            print(f"{w}x{h}")
        else:
            print(__doc__)
    finally:
        c.close()


if __name__ == "__main__":
    main()
