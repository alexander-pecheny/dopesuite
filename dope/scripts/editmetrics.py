#!/usr/bin/env python3
"""Summarize edit-path instrumentation from a live concurrent-editor test.

Run the server with DOPE_EDIT_METRICS=1; it emits `editmetric ...` log lines.
This script turns the raw lines (and, optionally, downloaded client recorder
JSON logs) into percentiles so you can answer: is the global write mutex the
bottleneck, and what do editors actually feel?

Usage:
    # Server log (stdin or files). Non-editmetric lines are ignored.
    uv run python scripts/editmetrics.py server.log
    journalctl -u dope --since today | uv run python scripts/editmetrics.py

    # Mix in one or more client recorder dumps (the JSON from the "download
    # log" button) for felt-latency. .json files are auto-detected.
    uv run python scripts/editmetrics.py server.log dope-log-game-state_42-*.json

What each metric means:
    wait_ms      time blocked acquiring the global write mutex  <- contention
    hold_ms      time the critical section ran (lock held)      <- work serialized
    db_ms        UPDATE + revision bump + COMMIT (subset of hold)
    e2e_ms       whole PATCH handler, request-in to response-out (server only)
    rebuild_ms   FestView cache miss rebuild (the invalidation-amplification cost)
    rtt_ms       client: keystroke-batch -> server-confirmed (own felt latency)
    delivery_ms  client: server emit -> render (co-editor/viewer felt; has skew)
"""

import json
import re
import sys

KV = re.compile(r"(\w+)=([0-9.]+)")


def pct(values, p):
    if not values:
        return 0.0
    s = sorted(values)
    idx = min(int(p * len(s) / 100), len(s) - 1)
    return s[idx]


def fmt_block(name, values, unit="ms"):
    if not values:
        return f"  {name:<12} (no samples)"
    return (
        f"  {name:<12} n={len(values):<6} "
        f"p50={pct(values,50):8.2f} p90={pct(values,90):8.2f} "
        f"p95={pct(values,95):8.2f} p99={pct(values,99):8.2f} "
        f"max={max(values):9.2f} {unit}"
    )


def parse_server(lines):
    """Returns dict metric-name -> list of float, plus counters."""
    edits = {k: [] for k in
             ("wait_ms", "hold_ms", "unmarshal_ms", "marshal_ms", "db_ms",
              "broadcast_ms", "e2e_ms", "bytes")}
    waiters, rebuilds = [], []
    n_edits = 0
    for line in lines:
        if "editmetric" not in line:
            continue
        kv = dict(KV.findall(line))
        if "editmetric edit" in line:
            n_edits += 1
            for k in edits:
                if k in kv:
                    edits[k].append(float(kv[k]))
            if "waiters" in kv:
                waiters.append(float(kv["waiters"]))
        elif "editmetric festview" in line and "rebuild_ms" in kv:
            rebuilds.append(float(kv["rebuild_ms"]))
    return edits, waiters, rebuilds, n_edits


def parse_recorder(path):
    """Pull patch-rtt / delta-latency samples out of a client recorder dump."""
    rtt, delivery = [], []
    try:
        with open(path) as fh:
            doc = json.load(fh)
    except (OSError, ValueError) as e:
        print(f"  ! could not read {path}: {e}", file=sys.stderr)
        return rtt, delivery
    for ev in doc.get("events", []):
        if ev.get("type") == "patch-rtt" and "rtt_ms" in ev:
            rtt.append(float(ev["rtt_ms"]))
        elif ev.get("type") == "delta-latency" and "delivery_ms" in ev:
            delivery.append(float(ev["delivery_ms"]))
    return rtt, delivery


def main(argv):
    files = argv[1:]
    server_lines = []
    recorder_files = []
    for f in files:
        if f.endswith(".json"):
            recorder_files.append(f)
        else:
            with open(f) as fh:
                server_lines.extend(fh.readlines())
    if not files and not sys.stdin.isatty():
        server_lines.extend(sys.stdin.readlines())

    edits, waiters, rebuilds, n_edits = parse_server(server_lines)

    print("=" * 78)
    print(f"SERVER  edits={n_edits}")
    print("=" * 78)
    if n_edits:
        print(fmt_block("wait", edits["wait_ms"]), "  <- lock contention")
        print(fmt_block("hold", edits["hold_ms"]), "  <- critical section")
        print(fmt_block("unmarshal", edits["unmarshal_ms"]))
        print(fmt_block("marshal", edits["marshal_ms"]))
        print(fmt_block("db", edits["db_ms"]))
        print(fmt_block("broadcast", edits["broadcast_ms"]))
        print(fmt_block("e2e", edits["e2e_ms"]))
        print(fmt_block("bytes", edits["bytes"], unit="B"))
        if waiters:
            print(f"\n  max writeWaiters depth = {int(max(waiters))}  "
                  f"(p95={int(pct(waiters,95))})  "
                  f"-- >1 means editors queued behind each other on s.mu")
        # Where does the held lock go?
        hold = sum(edits["hold_ms"]) or 1.0
        print("\n  hold breakdown (share of total lock-held time):")
        for k in ("unmarshal_ms", "marshal_ms", "db_ms"):
            print(f"    {k:<12} {100*sum(edits[k])/hold:5.1f}%")
    else:
        print("  no 'editmetric edit' lines found "
              "(is DOPE_EDIT_METRICS=1 set on the server?)")

    print("\n" + "=" * 78)
    print(f"FESTVIEW CACHE  rebuilds={len(rebuilds)}")
    print("=" * 78)
    if rebuilds:
        print(fmt_block("rebuild", rebuilds), "  <- cost paid per cache miss")
        print("  Each edit invalidates the whole fest's cache, so a rebuild can "
              "fire\n  on the next reader request. High rebuild count + cost = "
              "the\n  invalidation-amplification we suspected.")
    else:
        print("  no rebuild lines (cache stayed warm, or metrics off)")

    if recorder_files:
        rtt_all, delivery_all = [], []
        for path in recorder_files:
            r, d = parse_recorder(path)
            rtt_all.extend(r)
            delivery_all.extend(d)
        print("\n" + "=" * 78)
        print(f"CLIENT FELT LATENCY  ({len(recorder_files)} recorder dump(s))")
        print("=" * 78)
        print(fmt_block("patch-rtt", rtt_all), "  <- own edit confirm (clean)")
        print(fmt_block("delivery", delivery_all), "  <- co-editor render (has skew)")


if __name__ == "__main__":
    main(sys.argv)
