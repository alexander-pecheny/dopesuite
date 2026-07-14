// timer.js — a ЧГК (trivia) play timer that floats bottom-right of the board.
// Toggled by the ⏰ button in the header. Counts a question's minute (or its
// duplet/blitz sub-segments) down to zero with audible cues, then runs the
// 10-second answer-writing countdown. All sound is synthesised with WebAudio
// (oscillators) so there are no media assets to ship and nothing to fetch —
// which keeps it within the strict same-origin CSP (default-src 'self').
import { xyApp } from "./app.js";
const { el } = xyApp;

// ---- presets ----------------------------------------------------------------
// Each preset is an ordered list of segment durations (seconds). A single 60s
// segment is an ordinary question; multi-segment presets are duplets/blitzes,
// where each segment is played (and re-Started) in turn. Only the LAST segment
// of any preset gets the 10-second warning beep and the answer countdown; the
// earlier ones simply end on a long beep.
const PRESETS = {
  regular: { label: "Обычный вопрос (60 с)", segments: [60] },
  duplet: { label: "Дуплет (30 + 30)", segments: [30, 30] },
  blitz: { label: "Блиц (20 + 20 + 20)", segments: [20, 20, 20] },
  custom: { label: "Свой…", segments: [60] },
};
const ANSWER_SEC = 10; // post-question window to write the answer down
const WARN_AT = 10; // seconds-left at which the single warning beep fires

// ---- WebAudio beeps ---------------------------------------------------------
let audioCtx = null;
// ensureAudio lazily builds the context and resumes it. Must be called from a
// user gesture (Start click) the first time, or iOS keeps it suspended.
function ensureAudio() {
  try {
    const AC = window.AudioContext || window.webkitAudioContext;
    if (!AC) return null;
    if (!audioCtx) audioCtx = new AC();
    if (audioCtx.state === "suspended") audioCtx.resume();
    return audioCtx;
  } catch (_) {
    return null;
  }
}
// tone schedules a single shaped oscillator burst (attack/release envelope so it
// doesn't click). Frequencies/lengths are chosen to be distinguishable by ear.
function tone(freq, dur, type = "square", gain = 0.18) {
  const ac = ensureAudio();
  if (!ac) return;
  const t = ac.currentTime;
  const osc = ac.createOscillator();
  const g = ac.createGain();
  osc.type = type;
  osc.frequency.value = freq;
  g.gain.setValueAtTime(0, t);
  g.gain.linearRampToValueAtTime(gain, t + 0.012);
  g.gain.setValueAtTime(gain, t + Math.max(0.02, dur - 0.04));
  g.gain.linearRampToValueAtTime(0, t + dur);
  osc.connect(g);
  g.connect(ac.destination);
  osc.start(t);
  osc.stop(t + dur + 0.02);
}
const warningBeep = () => tone(880, 0.22, "square", 0.18); // "10 seconds left"
const tickBeep = () => tone(1040, 0.085, "square", 0.16); // answer-countdown tick
const longBeep = () => tone(587, 0.85, "sawtooth", 0.2); // segment / answer end

// ---- state machine ----------------------------------------------------------
// phase: ready    → press Start to run the current segment
//        running  → counting the current question segment down
//        paused   → frozen (resumePhase remembers what to resume into)
//        answer   → counting the 10s answer window (last segment only)
//        done     → whole preset finished; Reset to play again
const m = {
  presetKey: "regular",
  segments: PRESETS.regular.segments.slice(),
  segIdx: 0,
  phase: "ready",
  resumePhase: "running",
  remaining: 60, // frozen seconds for the current/paused countdown
  deadline: 0, // performance.now() target while running/answer
  shown: 60, // last integer shown (drives beep-on-change + display)
  raf: 0,
};
const isLast = () => m.segIdx === m.segments.length - 1;

function stopLoop() {
  if (m.raf) cancelAnimationFrame(m.raf);
  m.raf = 0;
}
function startLoop() {
  stopLoop();
  m.raf = requestAnimationFrame(loop);
}
function loop() {
  m.raf = requestAnimationFrame(loop);
  const rem = (m.deadline - performance.now()) / 1000;
  step(rem);
}

// step advances the display and fires the audio cues for one animation frame.
function step(rem) {
  const disp = Math.max(0, Math.ceil(rem - 1e-3));
  if (disp !== m.shown) {
    if (m.phase === "answer") {
      if (disp >= 1) tickBeep(); // 10,9,…,1 — the "10" fired on entry
    } else if (m.phase === "running" && isLast() && disp === WARN_AT) {
      warningBeep();
    }
    m.shown = disp;
    renderTime();
  }
  if (rem <= 0) endCountdown();
}

// endCountdown handles a countdown reaching zero, branching on phase/segment.
function endCountdown() {
  if (m.phase === "running") {
    if (isLast()) {
      // Question's up → roll straight into the answer-writing window. The first
      // tick (for "10") fires now; the loop keeps running for 9…1.
      m.phase = "answer";
      m.deadline = performance.now() + ANSWER_SEC * 1000;
      m.shown = ANSWER_SEC;
      tickBeep();
      renderTime();
      renderControls();
      return;
    }
    // A non-final duplet/blitz segment: long beep, queue up the next segment and
    // wait for the player to press Start again.
    stopLoop();
    longBeep();
    m.segIdx += 1;
    m.remaining = m.segments[m.segIdx];
    m.shown = m.remaining;
    m.phase = "ready";
  } else if (m.phase === "answer") {
    stopLoop();
    longBeep();
    m.phase = "done";
    m.remaining = 0;
    m.shown = 0;
  }
  renderTime();
  renderControls();
}

// ---- controls ---------------------------------------------------------------
function beginRun(kind) {
  m.phase = kind;
  m.deadline = performance.now() + m.remaining * 1000;
  m.shown = Math.max(0, Math.ceil(m.remaining - 1e-3));
  renderTime();
  renderControls();
  startLoop();
}
function start() {
  ensureAudio(); // warm/resume the context inside this user gesture
  if (m.phase === "ready") beginRun("running");
  else if (m.phase === "paused") beginRun(m.resumePhase);
  // running / answer / done → no-op
}
function pause() {
  if (m.phase !== "running" && m.phase !== "answer") return;
  stopLoop();
  m.remaining = Math.max(0, (m.deadline - performance.now()) / 1000);
  m.resumePhase = m.phase;
  m.phase = "paused";
  renderControls();
}
function reset() {
  stopLoop();
  m.segIdx = 0;
  m.remaining = m.segments[0] || 0;
  m.shown = m.remaining;
  m.phase = "ready";
  renderTime();
  renderControls();
}
function selectPreset(key) {
  m.presetKey = key;
  if (key !== "custom") m.segments = PRESETS[key].segments.slice();
  else m.segments = parseCustom(customInput && customInput.value);
  reset();
}
// parseCustom reads a plus-separated list of positive integers ("40+20" →
// [40,20]); falls back to a single 60s segment when nothing usable is entered.
function parseCustom(raw) {
  const parts = String(raw || "")
    .split("+")
    .map((s) => parseInt(s.trim(), 10))
    .filter((n) => Number.isFinite(n) && n > 0);
  return parts.length ? parts : [60];
}

// ---- DOM --------------------------------------------------------------------
let overlay, timeNode, labelNode, startBtn, pauseBtn, presetSel, customWrap, customInput;

function build() {
  presetSel = el("select", { class: "input timer-preset", "aria-label": "Режим таймера" });
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
  });
  const applyCustom = () => { if (m.presetKey === "custom") selectPreset("custom"); };
  customInput.addEventListener("change", applyCustom);
  customInput.addEventListener("input", applyCustom);
  customWrap = el("div", { class: "timer-custom", hidden: true }, customInput);

  timeNode = el("div", { class: "timer-time", text: "60" });
  labelNode = el("div", { class: "timer-label", text: "" });

  startBtn = el("button", { class: "btn btn-small", type: "button", text: "Старт", onclick: start });
  pauseBtn = el("button", { class: "btn btn-small btn-ghost", type: "button", text: "Пауза", onclick: pause });
  const resetBtn = el("button", { class: "btn btn-small btn-ghost", type: "button", text: "Сброс", onclick: reset });

  overlay = el(
    "div",
    { class: "timer-overlay", role: "dialog", "aria-label": "Таймер ЧГК", hidden: true },
    el("div", { class: "timer-row" }, presetSel),
    customWrap,
    el("div", { class: "timer-display" }, timeNode, labelNode),
    el("div", { class: "timer-actions" }, startBtn, pauseBtn, resetBtn),
  );
  document.body.append(overlay);
  renderTime();
  renderControls();
}

function renderTime() {
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

function renderControls() {
  if (!startBtn) return;
  const canStart = m.phase === "ready" || m.phase === "paused";
  const canPause = m.phase === "running" || m.phase === "answer";
  startBtn.disabled = !canStart;
  pauseBtn.disabled = !canPause;
  startBtn.textContent = m.phase === "paused" ? "Продолжить" : "Старт";
}

// ---- toggle wiring ----------------------------------------------------------
function toggle() {
  if (!overlay) build();
  const show = overlay.hidden;
  overlay.hidden = !show;
  const btn = document.getElementById("timerToggle");
  if (btn) btn.setAttribute("aria-pressed", String(show));
  if (show) ensureAudio(); // user gesture — get audio ready before first Start
}

function init() {
  const btn = document.getElementById("timerToggle");
  if (btn) btn.addEventListener("click", toggle);
}

if (typeof document !== "undefined") {
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", init);
  else init();
}

export const xyTimer = { _presets: PRESETS, _parseCustom: parseCustom };
if (typeof window !== "undefined") window.xyTimer = xyTimer;
