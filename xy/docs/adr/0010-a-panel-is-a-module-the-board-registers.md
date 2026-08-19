# 10. A panel is a module the board registers, and a kernel takes its DOM

Date: 2026-08-19

## Status

Accepted

## Context

The 18 Aug 2026 architecture review found board.ts at 3896 lines with no test
file while AGENTS.md called it «a thin orchestrator». Every feature of the ☰
and the list ⋯ menus was an inline block; adding «Счётчик авторов»
(`0bf81c9`) touched eight files in three languages, and the two newest panels
had already stopped following the modal lifecycle the eleven before them
hand-rolled. The extracted kernels (carddetail.ts, timeline.ts, timer.ts) had
moved code, not coupling: a private byId into document, a state machine on
setInterval and a live AudioContext, three underscore test hooks. The 4s
format had two specs — the Go one honoured chgksuite parity with oracles, the
TS one kept its own marker table, its own .hndt reader and four copies of an
(img …) regex — and the Version algebra, the best-modelled concept in xy, had
no file to land in.

## Decision

Five moves, one commit each, in the order D1 → D2 → D3 → D5 → D4:

- modal.ts: `modal(stem)` is the one lifecycle for a plain dialog — hidden,
  the overlay-stack registration, ✕/Готово/Отмена, the backdrop, the message
  node — by the `<stem>Overlay/Close/Cancel/Message` convention the .dopeui
  pages follow. Every plain modal on every page goes through it (`df92e50`).
- panels.ts: a feature the menus offer is a module that registers
  `{id, menu, icon, label, offered?, open(scope)}`; board.ts lists them in
  one `registerPanel(...)` call in menu order and both menus render the
  registry as data. A panel works through the Board seam — the live state,
  the read helpers, the four verbs, render, setStatus, reload — and takes
  anything else (the card detail's copy machinery, the attachments cache) as
  its own dependency. A panel that builds its body with `el()` renders into
  the one panel shell and touches no .dopeui, vocab or Go (`e4d70d6`).
- One 4s spec: `go generate` writes web/ts/markers_gen.ts from
  fsource.markerMapping; `internal/chgk/fsource/testdata/parity.json` is the
  corpus both suites read — every fixture line split at its marker, the
  (img …) references, one .hndt the browser writes and Go parses — with the
  Go test as the oracle (`-update`) and a jstest as the other reader; imgRefs
  reads through the tokenizer's bracket matching (`11ba812`).
- versions.ts and hndt.ts leave chgk.ts, the test-card functions join
  sessions.ts; chgk.ts re-exports nothing (`65821df`).
- createTimer({clock, bell, view}) is the timer's kernel and mountTimer its
  page adapter; carddetail.ts and timeline.ts take their nodes as typed ui
  records from board.ts (`5f4c1dd`).

## Considered options

A registry that panels populate at import time was rejected for the explicit
list: import order is not a menu order anyone reads. Rebuilding every panel's
markup with `el()` for the shell was rejected: the panels with heavy declared
markup (import, lists-manage, replace) keep their .dopeui modal through
modal.ts, and the shell stays optional. Collapsing the card detail's seven
board callbacks into one «card N changed» seam was deferred: it changes
behaviour on the app's core surface, and the ui record alone makes the kernel
run under the DOM shim. Asserting the two marker tables equal instead of
generating one from the other was rejected: a table that is data on one side
and code on the other is two tables.

## Consequences

- A menu entry that is not registered in panels.ts, a dialog that toggles
  `hidden` itself, a kernel that calls `document.getElementById`, or a marker
  spelled by hand in TS is a regression. A new panel is one module, one
  `registerPanel` line and — when it builds its own body — nothing else.
- The DOM shim (jstest/dom.js) is the test surface for everything that
  renders: fakeBoard, installDOM, and a history whose back() fires popstate.
- The pixel gate is dope's, not xy's: each of these commits was smoke-tested
  by hand in a browser (local, then xytest) before prod.
- The TS block types `number` and `setcounter` replaced `num` and `numnum`,
  and `battle`/`round`/`theme` are marked lines now, as in chgksuite.
- profile.html and index.html modals gained the board's back-button
  dismissal, which they never had; every plain dialog now also dismisses on a
  backdrop tap.
