// testmode.ts — Test mode (ADR-0012): press play on a Test Session and the
// device does the bookkeeping a test evening otherwise costs in clicks. While
// the mode is on, a question card kept open for a minute is marked with the
// test, and every comment posted goes up already tagged with it.
//
// The mode is a device-local fact, not a board fact: two editors of one packet
// run test sessions simultaneously, each on their own device, and the server
// never needs to know which. One localStorage slot holds the whole state, so
// the mode survives reloads and expires by wall clock — an hour without a
// touch anywhere in xy ends it, whether the tab was open, backgrounded or
// closed the entire time.
//
// The kernel is pure over an injected clock and store (jstest drives it the
// way timer.ts is driven); board.ts and pwa.ts own the wiring.

export interface TestModeState {
  boardId: number;
  sessionId: number;
  lastActivity: number; // ms epoch of the device's last interaction with xy
  unmarked: number[]; // cards hand-unmarked during this mode: never re-marked
}

export const TESTMODE_KEY = "xy-testmode";
export const IDLE_LIMIT_MS = 60 * 60 * 1000;
export const DWELL_MS = 60 * 1000;
// Against an hour-long idle limit, stamping activity to the second buys nothing.
export const TOUCH_EVERY_MS = 30 * 1000;

export interface TestModeDeps {
  now(): number;
  read(): TestModeState | null;
  write(s: TestModeState | null): void;
}

export interface TestMode {
  start(boardId: number, sessionId: number): void;
  stop(): void;
  // The state, with the expiry rule applied: an idle hour reads as null and
  // wipes the slot, so every caller sees the same answer.
  active(): TestModeState | null;
  sessionFor(boardId: number): number | null;
  touch(): void;
  noteUnmarked(boardId: number, cardId: number): void;
  allowMark(boardId: number, cardId: number): boolean;
}

export function createTestMode(deps: TestModeDeps): TestMode {
  function active(): TestModeState | null {
    const s = deps.read();
    if (!s) return null;
    if (deps.now() - s.lastActivity > IDLE_LIMIT_MS) {
      deps.write(null);
      return null;
    }
    return s;
  }
  return {
    active,
    start(boardId, sessionId) {
      deps.write({ boardId, sessionId, lastActivity: deps.now(), unmarked: [] });
    },
    stop() {
      deps.write(null);
    },
    sessionFor(boardId) {
      const s = active();
      return s && s.boardId === boardId ? s.sessionId : null;
    },
    touch() {
      const s = active();
      if (!s || deps.now() - s.lastActivity < TOUCH_EVERY_MS) return;
      deps.write({ ...s, lastActivity: deps.now() });
    },
    noteUnmarked(boardId, cardId) {
      const s = active();
      if (!s || s.boardId !== boardId || s.unmarked.includes(cardId)) return;
      deps.write({ ...s, unmarked: [...s.unmarked, cardId] });
    },
    allowMark(boardId, cardId) {
      const s = active();
      return !!s && s.boardId === boardId && !s.unmarked.includes(cardId);
    },
  };
}

// liveTestMode is the controller over the real clock and localStorage — the
// one construction board.ts and pwa.ts share.
export function liveTestMode(): TestMode {
  return createTestMode({ now: () => Date.now(), ...testModeStore(localStorage) });
}

// testModeStore reads and writes the one slot, shrugging off garbage: a state
// that does not parse, or parses into the wrong shape, reads as no mode.
type StorageLike = Pick<Storage, "getItem" | "setItem" | "removeItem">;

export function testModeStore(storage: StorageLike): Pick<TestModeDeps, "read" | "write"> {
  return {
    read() {
      const raw = storage.getItem(TESTMODE_KEY);
      if (!raw) return null;
      try {
        const s = JSON.parse(raw) as TestModeState;
        if (typeof s.boardId !== "number" || typeof s.sessionId !== "number" ||
          typeof s.lastActivity !== "number" || !Array.isArray(s.unmarked)) return null;
        return s;
      } catch (_) {
        return null;
      }
    },
    write(s) {
      if (s) storage.setItem(TESTMODE_KEY, JSON.stringify(s));
      else storage.removeItem(TESTMODE_KEY);
    },
  };
}

// ---- the dwell watcher ----
// One open card at a time, one stamp per open. The timer is only a wake-up
// call: the decision is always now() against the stamp, so a background tab
// whose timers are throttled marks late rather than never, and the
// visibilitychange catch-up (the wiring calls check()) closes even that gap.
// Whether a mark is currently allowed is tryMark's business — eligibility is
// checked when the minute is up, not when the card opens.

export interface DwellDeps {
  now(): number;
  setTimer(fn: () => void, ms: number): unknown;
  clearTimer(id: unknown): void;
  // true = the dwell is spent (marked, already marked, or vetoed for good);
  // false = nothing to decide yet (no mode live) — a later check() retries, so
  // a mode started mid-read still marks the card that has been open all along.
  tryMark(cardId: number): boolean;
}

export interface Dwell {
  opened(cardId: number): void;
  closed(): void;
  check(): void;
}

export function createDwell(deps: DwellDeps): Dwell {
  let open: { cardId: number; at: number; fired: boolean } | null = null;
  let timer: unknown = null;

  function check(): void {
    if (!open || open.fired || deps.now() - open.at < DWELL_MS) return;
    open.fired = deps.tryMark(open.cardId);
  }
  function drop(): void {
    if (timer != null) deps.clearTimer(timer);
    timer = null;
    open = null;
  }
  return {
    check,
    opened(cardId) {
      drop();
      open = { cardId, at: deps.now(), fired: false };
      timer = deps.setTimer(check, DWELL_MS + 1000);
    },
    closed: drop,
  };
}
