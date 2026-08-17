// timer.ts — a ЧГК (trivia) play timer that floats bottom-right of the board.
// Toggled by the ⏰ button in the header. Counts a question's minute (or its
// duplet/blitz sub-segments) down to zero with audible cues, then runs the
// 10-second answer-writing countdown. The cues are one shipped bell sample
// (/static/ding.mp3, same-origin so CSP-fine) played through WebAudio at
// different rates to stay distinguishable by ear; until it's decoded the old
// synthesised oscillator beeps sound instead. Every cue is put on the audio
// clock when a countdown starts, so a background tab still rings on time.
import { xyApp } from "./app.js";
const { el } = xyApp;

declare global {
  interface Window { webkitAudioContext?: typeof AudioContext }
}

// ---- presets ----------------------------------------------------------------
// Each preset is an ordered list of segment durations (seconds). A single 60s
// segment is an ordinary question; multi-segment presets are duplets/blitzes,
// where each segment is played (and re-Started) in turn. Only the LAST segment
// of any preset gets the 10-second warning beep and the answer countdown; the
// earlier ones simply end on a long beep.
export interface TimerPreset { label: string; segments: number[] }
const PRESETS: Record<string, TimerPreset> = {
  regular: { label: "Обычный вопрос (60 с)", segments: [60] },
  duplet: { label: "Дуплет (30 + 30)", segments: [30, 30] },
  blitz: { label: "Блиц (20 + 20 + 20)", segments: [20, 20, 20] },
  custom: { label: "Свой…", segments: [60] },
};
const ANSWER_SEC = 10; // post-question window to write the answer down
const WARN_AT = 10; // seconds-left at which the single warning beep fires

// ---- WebAudio bell ----------------------------------------------------------
let audioCtx: AudioContext | null = null;
let dingBuf: AudioBuffer | null = null; // decoded ding.mp3; null until it arrives (tones fall back)
let dingReq: Promise<void> | null = null;
// ensureAudio lazily builds the context and resumes it. Must be called from a
// user gesture (Start click) the first time, or iOS keeps it suspended.
function ensureAudio(): AudioContext | null {
  try {
    const AC = window.AudioContext || window.webkitAudioContext;
    if (!AC) return null;
    if (!audioCtx) audioCtx = new AC();
    if (audioCtx.state === "suspended") audioCtx.resume();
    loadDing(audioCtx);
    return audioCtx;
  } catch (_) {
    return null;
  }
}
function loadDing(ac: AudioContext): void {
  if (dingReq) return;
  dingReq = fetch("/static/ding.mp3")
    .then((r) => { if (!r.ok) throw new Error(String(r.status)); return r.arrayBuffer(); })
    .then((b) => ac.decodeAudioData(b))
    .then((buf) => { dingBuf = buf; })
    .catch(() => { dingReq = null; }); // retry on the next user gesture
}

// The three cues. `rate` shifts the bell's pitch and length together (2 = the
// answer ticks an octave up and half as long, 0.5 = the end an octave down);
// the rest shapes the oscillator that stands in until the bell is decoded.
type CueKind = "warn" | "tick" | "long";
interface Cue { rate: number; gain: number; freq: number; dur: number; wave: OscillatorType; toneGain: number }
const CUES: Record<CueKind, Cue> = {
  warn: { rate: 1, gain: 0.7, freq: 880, dur: 0.22, wave: "square", toneGain: 0.18 }, // "10 seconds left"
  tick: { rate: 2, gain: 0.35, freq: 1040, dur: 0.085, wave: "square", toneGain: 0.16 }, // answer-countdown tick
  long: { rate: 0.5, gain: 1, freq: 587, dur: 0.85, wave: "sawtooth", toneGain: 0.2 }, // segment / answer end
};

// Every cue of a countdown is scheduled up front on the audio clock, which
// keeps time while the tab is hidden — rAF stops there and timers crawl, so a
// loop that beeps when it notices the second change skips and bunches dings.
let scheduled: AudioScheduledSourceNode[] = [];
function playAt(kind: CueKind, inSec: number): void {
  const ac = ensureAudio();
  if (!ac) return;
  const cue = CUES[kind];
  const t = ac.currentTime + inSec;
  const g = ac.createGain();
  g.connect(ac.destination);
  let src: AudioScheduledSourceNode;
  if (dingBuf) {
    const s = ac.createBufferSource();
    s.buffer = dingBuf;
    s.playbackRate.value = cue.rate;
    g.gain.value = cue.gain;
    src = s;
    src.connect(g);
    src.start(t);
  } else {
    const osc = ac.createOscillator();
    osc.type = cue.wave;
    osc.frequency.value = cue.freq;
    g.gain.setValueAtTime(0, t);
    g.gain.linearRampToValueAtTime(cue.toneGain, t + 0.012);
    g.gain.setValueAtTime(cue.toneGain, t + Math.max(0.02, cue.dur - 0.04));
    g.gain.linearRampToValueAtTime(0, t + cue.dur);
    src = osc;
    src.connect(g);
    src.start(t);
    src.stop(t + cue.dur + 0.02);
  }
  scheduled.push(src);
  src.onended = () => { scheduled = scheduled.filter((s) => s !== src); };
}
function cancelCues(): void {
  for (const s of scheduled) { try { s.stop(); } catch (_) {} }
  scheduled = [];
}

// cueTimes lists the bells of a countdown that has `rem` seconds left, each as
// seconds from now. Only the last segment gets the warning, the ten answer
// ticks and the final bell; an earlier duplet/blitz segment just ends long.
function cueTimes(phase: "running" | "answer", last: boolean, rem: number): Array<[CueKind, number]> {
  const out: Array<[CueKind, number]> = [];
  if (phase === "answer") {
    for (let d = 1; d < rem - 1e-3; d++) out.push(["tick", rem - d]);
    out.push(["long", rem]);
    return out;
  }
  if (!last) return [["long", rem]];
  if (rem - WARN_AT > 1e-3) out.push(["warn", rem - WARN_AT]);
  for (let j = 0; j < ANSWER_SEC; j++) out.push(["tick", rem + j]);
  out.push(["long", rem + ANSWER_SEC]);
  return out;
}

// ---- state machine ----------------------------------------------------------
// phase: ready    → press Start to run the current segment
//        running  → counting the current question segment down
//        paused   → frozen (resumePhase remembers what to resume into)
//        answer   → counting the 10s answer window (last segment only)
//        done     → whole preset finished; Reset to play again
type Phase = "ready" | "running" | "paused" | "answer" | "done";
interface TimerState {
  presetKey: string;
  segments: number[];
  segIdx: number;
  phase: Phase;
  resumePhase: "running" | "answer";
  remaining: number;
  deadline: number;
  shown: number;
  timer: number;
}
const m: TimerState = {
  presetKey: "regular",
  segments: PRESETS.regular.segments.slice(),
  segIdx: 0,
  phase: "ready",
  resumePhase: "running",
  remaining: 60, // frozen seconds for the current/paused countdown
  deadline: 0, // performance.now() target while running/answer
  shown: 60, // last integer shown
  timer: 0, // setInterval handle while running/answer
};
const isLast = (): boolean => m.segIdx === m.segments.length - 1;

// The loop only paints and moves the phase along; the bells are already on the
// audio clock, so a throttled or paused loop delays the display, not the sound.
function stopLoop(): void {
  if (m.timer) clearInterval(m.timer);
  m.timer = 0;
}
function startLoop(): void {
  stopLoop();
  m.timer = window.setInterval(loop, 100);
}
function loop(): void {
  const rem = (m.deadline - performance.now()) / 1000;
  const disp = Math.max(0, Math.ceil(rem - 1e-3));
  if (disp !== m.shown) {
    m.shown = disp;
    renderTime();
  }
  if (rem <= 0) endCountdown();
}

// endCountdown handles a countdown reaching zero, branching on phase/segment.
function endCountdown(): void {
  if (m.phase === "running") {
    if (isLast()) {
      // Question's up → roll straight into the answer-writing window, measured
      // from the question's deadline so a late frame does not stretch it.
      m.phase = "answer";
      m.deadline += ANSWER_SEC * 1000;
      m.shown = ANSWER_SEC;
      renderTime();
      renderControls();
      return;
    }
    // A non-final duplet/blitz segment: queue up the next segment and wait for
    // the player to press Start again.
    stopLoop();
    m.segIdx += 1;
    m.remaining = m.segments[m.segIdx];
    m.shown = m.remaining;
    m.phase = "ready";
  } else if (m.phase === "answer") {
    stopLoop();
    m.phase = "done";
    m.remaining = 0;
    m.shown = 0;
  }
  renderTime();
  renderControls();
}

// ---- controls ---------------------------------------------------------------
function beginRun(kind: "running" | "answer"): void {
  m.phase = kind;
  m.deadline = performance.now() + m.remaining * 1000;
  m.shown = Math.max(0, Math.ceil(m.remaining - 1e-3));
  cancelCues();
  for (const [cue, at] of cueTimes(kind, isLast(), m.remaining)) playAt(cue, at);
  renderTime();
  renderControls();
  startLoop();
}
function start(): void {
  ensureAudio(); // warm/resume the context inside this user gesture
  if (m.phase === "ready") beginRun("running");
  else if (m.phase === "paused") beginRun(m.resumePhase);
  // running / answer / done → no-op
}
function pause(): void {
  if (m.phase !== "running" && m.phase !== "answer") return;
  stopLoop();
  cancelCues();
  m.remaining = Math.max(0, (m.deadline - performance.now()) / 1000);
  m.resumePhase = m.phase;
  m.phase = "paused";
  renderControls();
}
function reset(): void {
  stopLoop();
  cancelCues();
  m.segIdx = 0;
  m.remaining = m.segments[0] || 0;
  m.shown = m.remaining;
  m.phase = "ready";
  renderTime();
  renderControls();
}
function selectPreset(key: string): void {
  m.presetKey = key;
  if (key !== "custom") m.segments = PRESETS[key].segments.slice();
  else m.segments = parseCustom(customInput && customInput.value);
  reset();
}
// parseCustom reads a plus-separated list of positive integers ("40+20" →
// [40,20]); falls back to a single 60s segment when nothing usable is entered.
function parseCustom(raw: string | null | undefined): number[] {
  const parts = String(raw || "")
    .split("+")
    .map((s) => parseInt(s.trim(), 10))
    .filter((n) => Number.isFinite(n) && n > 0);
  return parts.length ? parts : [60];
}

// ---- DOM --------------------------------------------------------------------
let overlay!: HTMLElement, timeNode!: HTMLElement, labelNode!: HTMLElement,
  startBtn!: HTMLButtonElement, pauseBtn!: HTMLButtonElement,
  presetSel!: HTMLSelectElement, customWrap!: HTMLElement, customInput!: HTMLInputElement;

// Inline SVG button icons (Feather shapes, currentColor). Font glyphs were the
// first take, but ↺ renders half the size of ▶/⏸ and varies per platform —
// drawn paths keep the three buttons visually equal everywhere.
const SVG_NS = "http://www.w3.org/2000/svg";
function icon(...shapes: Array<[string, Record<string, string>]>): SVGSVGElement {
  const svg = document.createElementNS(SVG_NS, "svg");
  svg.setAttribute("class", "timer-ico");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("aria-hidden", "true");
  for (const [tag, attrs] of shapes) {
    const n = document.createElementNS(SVG_NS, tag);
    for (const [k, v] of Object.entries(attrs)) n.setAttribute(k, v);
    svg.append(n);
  }
  return svg;
}
const stroked = (extra: Record<string, string>): Record<string, string> =>
  ({ fill: "none", stroke: "currentColor", "stroke-width": "2.5", "stroke-linecap": "round", "stroke-linejoin": "round", ...extra });
const playIcon = (): SVGSVGElement => icon(["polygon", { points: "7 4 20 12 7 20", fill: "currentColor" }]);
const pauseIcon = (): SVGSVGElement => icon(
  ["rect", { x: "6", y: "4", width: "4", height: "16", rx: "1", fill: "currentColor" }],
  ["rect", { x: "14", y: "4", width: "4", height: "16", rx: "1", fill: "currentColor" }],
);
const resetIcon = (): SVGSVGElement => icon(
  ["polyline", stroked({ points: "1.5 4 1.5 10 7.5 10" })],
  ["path", stroked({ d: "M3.8 15a9 9 0 1 0 2.1-9.4L1.5 10" })],
);

function build(): void {
  presetSel = el("select", { class: "input timer-preset", "aria-label": "Режим таймера" }) as HTMLSelectElement;
  for (const [key, p] of Object.entries(PRESETS)) presetSel.append(el("option", { value: key, text: p.label }));
  presetSel.addEventListener("change", () => {
    customWrap.hidden = presetSel.value !== "custom";
    selectPreset(presetSel.value);
  });

  customInput = el("input", {
    class: "input timer-custom-input",
    type: "text",
    inputmode: "numeric",
    placeholder: "напр. 40+20",
    "aria-label": "Свои длительности, через +",
  }) as HTMLInputElement;
  const applyCustom = (): void => { if (m.presetKey === "custom") selectPreset("custom"); };
  customInput.addEventListener("change", applyCustom);
  customInput.addEventListener("input", applyCustom);
  customWrap = el("div", { class: "timer-custom", hidden: true }, customInput);

  timeNode = el("div", { class: "timer-time", text: "60" });
  labelNode = el("div", { class: "timer-label", text: "" });

  // Icons, not captions — three worded buttons overflowed the 240px box
  // («Продолжить» alone nearly filled it). The word lives in title/aria-label.
  startBtn = el("button", { class: "btn btn-small", type: "button", title: "Старт", "aria-label": "Старт", onclick: start }, playIcon()) as HTMLButtonElement;
  pauseBtn = el("button", { class: "btn btn-small btn-ghost", type: "button", title: "Пауза", "aria-label": "Пауза", onclick: pause }, pauseIcon()) as HTMLButtonElement;
  const resetBtn = el("button", { class: "btn btn-small btn-ghost", type: "button", title: "Сброс", "aria-label": "Сброс", onclick: reset }, resetIcon());

  overlay = el(
    "div",
    { class: "timer-overlay", role: "dialog", "aria-label": "Таймер ЧГК", hidden: true },
    el("div", { class: "timer-row" }, presetSel),
    customWrap,
    el("div", { class: "timer-display" }, timeNode, labelNode),
    el("div", { class: "timer-actions" }, startBtn, pauseBtn, resetBtn),
  );
  document.body.append(overlay);
  wireDrag();
  renderTime();
  renderControls();
}

// ---- drag anywhere + remembered position ------------------------------------
// The overlay floats above everything and can be parked wherever it does not
// cover the question being played; the spot is remembered per browser.
const POS_KEY = "xyTimerPos";

function savedPos(): unknown {
  try { return JSON.parse(localStorage.getItem(POS_KEY) || "null"); } catch (_) { return null; }
}
// applyPos pins the overlay at left/top (switching it off its default
// bottom-right anchor), clamped so at least the whole box stays on screen.
function applyPos(pos: unknown): void {
  if (!pos || typeof pos !== "object") return;
  const p = pos as { left?: unknown; top?: unknown };
  if (typeof p.left !== "number" || typeof p.top !== "number") return;
  const left = Math.max(0, Math.min(p.left, window.innerWidth - overlay.offsetWidth));
  const top = Math.max(0, Math.min(p.top, window.innerHeight - overlay.offsetHeight));
  overlay.classList.add("timer-moved");
  overlay.style.left = left + "px";
  overlay.style.top = top + "px";
}

function wireDrag(): void {
  let drag: { dx: number; dy: number } | null = null; // pointer offset inside the box while a drag is live
  overlay.addEventListener("pointerdown", (e) => {
    if (e.button !== 0) return;
    if ((e.target as Element).closest("button, select, input")) return; // controls are not drag handles
    const r = overlay.getBoundingClientRect();
    drag = { dx: e.clientX - r.left, dy: e.clientY - r.top };
    try { overlay.setPointerCapture(e.pointerId); } catch (_) {} // synthetic events have no active pointer
    overlay.classList.add("timer-dragging");
    e.preventDefault();
  });
  overlay.addEventListener("pointermove", (e) => {
    if (!drag) return;
    applyPos({ left: e.clientX - drag.dx, top: e.clientY - drag.dy });
  });
  const end = (): void => {
    if (!drag) return;
    drag = null;
    overlay.classList.remove("timer-dragging");
    const r = overlay.getBoundingClientRect();
    try { localStorage.setItem(POS_KEY, JSON.stringify({ left: r.left, top: r.top })); } catch (_) {}
  };
  overlay.addEventListener("pointerup", end);
  overlay.addEventListener("pointercancel", end);
  // keep a parked overlay on screen when the window shrinks
  window.addEventListener("resize", () => {
    if (overlay.hidden || !overlay.classList.contains("timer-moved")) return;
    const r = overlay.getBoundingClientRect();
    applyPos({ left: r.left, top: r.top });
  });
}

function renderTime(): void {
  if (!timeNode) return;
  timeNode.textContent = String(m.shown);
  const answer = m.phase === "answer" || (m.phase === "paused" && m.resumePhase === "answer");
  timeNode.classList.toggle("timer-answer", answer);
  timeNode.classList.toggle("timer-urgent", !answer && m.phase === "running" && m.shown <= WARN_AT);
  // sub-label: answer window, multi-segment progress, or completion
  let label = "";
  if (answer) label = "Ответ";
  else if (m.phase === "done") label = "Готово";
  else if (m.segments.length > 1) label = `Вопрос ${m.segIdx + 1} / ${m.segments.length}`;
  labelNode.textContent = label;
}

function renderControls(): void {
  if (!startBtn) return;
  const canStart = m.phase === "ready" || m.phase === "paused";
  const canPause = m.phase === "running" || m.phase === "answer";
  startBtn.disabled = !canStart;
  pauseBtn.disabled = !canPause;
  const startWord = m.phase === "paused" ? "Продолжить" : "Старт";
  startBtn.title = startWord;
  startBtn.setAttribute("aria-label", startWord);
}

// ---- toggle wiring ----------------------------------------------------------
function toggle(): void {
  if (!overlay) build();
  const show = overlay.hidden;
  overlay.hidden = !show;
  const btn = document.getElementById("timerToggle");
  if (btn) btn.setAttribute("aria-pressed", String(show));
  if (show) {
    applyPos(savedPos()); // restore the remembered spot (clamped, now measurable)
    ensureAudio(); // user gesture — get audio ready before first Start
  }
}

function init(): void {
  const btn = document.getElementById("timerToggle");
  if (btn) btn.addEventListener("click", toggle);
}

if (typeof document !== "undefined") {
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", init);
  else init();
}

export const xyTimer = { _presets: PRESETS, _parseCustom: parseCustom, _cueTimes: cueTimes };
