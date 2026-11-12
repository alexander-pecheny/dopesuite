import { Call, Events } from "/wails/runtime.js";

// The classic tab: chgksuite's old window, a row of commands over a form, drawn
// from what `chgksuite-cli spec` says, so a flag added to the CLI turns up here.

const call = (method, ...args) => Call.ByName(`main.Classic.${method}`, ...args);
const $ = (id) => document.getElementById(id);
const logLines = 2000;
const storeKey = "chgksuite-classic";

function el(tag, props = {}, ...kids) {
  const n = Object.assign(document.createElement(tag), props);
  n.append(...kids);
  return n;
}

// The command and the flag values outlive a restart, as the old window's did.
// Picked files do not.
let saved = { verb: "compose docx", values: {}, advanced: false };
try { saved = { ...saved, ...JSON.parse(localStorage.getItem(storeKey) ?? "{}") }; } catch {}
const save = () => { try { localStorage.setItem(storeKey, JSON.stringify(saved)); } catch {} };

let specs = [];
const picked = {}; // verb → one array of strings per input
let running = false;

function seg(options, current, choose) {
  return el("div", { className: "seg" }, ...options.map(([value, label]) => {
    const b = el("button", { type: "button", className: "seg-btn" + (value === current ? " active" : ""), textContent: label });
    b.onclick = () => choose(value);
    return b;
  }));
}

function line(caption, ...control) {
  return el("div", { className: "u-row u-align-center u-gap-md u-wrap" },
    el("span", { className: "classic-caption", textContent: caption }), ...control);
}

const value = (f) => saved.values[f.name] ?? f.default;
const setValue = (f, v) => { saved.values[f.name] = v; save(); renderRun(); };

function flagRow(f) {
  const v = value(f);
  if (f.bool) {
    const box = el("input", { type: "checkbox", checked: v === "true" });
    box.onchange = () => setValue(f, String(box.checked));
    return el("label", { className: "checkbox", title: f.usage }, box, f.caption);
  }
  if (f.choices) {
    return line(f.caption, seg(f.choices.map((c) => [c, c]), v, (c) => { setValue(f, c); render(); }));
  }
  const secret = /password|token/.test(f.name);
  const input = el("input", { value: v, type: secret ? "password" : "text", autocomplete: "off", placeholder: f.usage, title: f.usage });
  input.className = "input u-grow";
  input.oninput = () => setValue(f, input.value);
  if (f.suggestions) {
    const list = el("datalist", { id: "suggest-" + f.name }, ...f.suggestions.map((v) => el("option", { value: v })));
    input.setAttribute("list", list.id);
    return line(f.caption, input, list);
  }
  if (!f.path) return line(f.caption, input);
  const pick = el("button", { type: "button", className: "btn", textContent: "Выбрать…" });
  pick.onclick = async () => {
    const p = f.path === "folder" ? await call("PickFolder") : (await call("PickFiles", [], false))[0];
    if (p) { input.value = p; setValue(f, p); }
  };
  return line(f.caption, input, pick);
}

function inputRow(spec, i) {
  const values = picked[spec.verb];
  const v = values[i];
  const inp = spec.inputs[i];
  const set = (x) => { values[i] = x; render(); };
  if (inp.kind === "choice") return line(inp.caption, seg(inp.choices.map((c) => [c, c]), v[0], (c) => set([c])));
  if (inp.kind === "text") {
    const input = el("input", { value: v[0] ?? "", autocomplete: "off" });
    input.className = "input u-grow";
    input.oninput = () => { values[i] = input.value ? [input.value] : []; renderRun(); };
    return line(inp.caption, input);
  }
  const names = v.map((p) => p.split(/[\\/]/).pop()).join(", ");
  const shown = el("span", { className: "u-grow" + (v.length ? "" : " hint"), textContent: names || "(не выбрано)", title: v.join("\n") });
  const choose = el("button", { type: "button", className: "btn", textContent: inp.kind === "folder" ? "Выбрать папку" : "Открыть файл" });
  choose.onclick = async () => {
    const got = inp.kind === "folder" ? [await call("PickFolder")].filter(Boolean) : await call("PickFiles", inp.ext ?? [], inp.kind === "files");
    if (got?.length) set(got);
  };
  return line(inp.caption, shown, choose);
}

// argv is what the form adds up to. A flag left at its default is left out, so
// the log reads like what someone would have typed.
// extra is what a run asked about adds, such as --token. Flags have to come
// before the files, where the CLI stops reading them.
function argv(spec, extra = []) {
  const args = [...spec.verb.split(" "), ...extra];
  for (const f of spec.flags) {
    const v = value(f);
    if (v === f.default || v === "") continue;
    if (f.bool) { if (v === "true") args.push("--" + f.name); continue; }
    args.push("--" + f.name, v);
  }
  return args.concat(...picked[spec.verb]);
}

const current = () => specs.find((s) => s.verb === saved.verb) ?? specs[0];
const ready = (spec) => spec.inputs?.every((inp, i) => inp.optional || picked[spec.verb][i].length) ?? true;

function renderRun() {
  const spec = current();
  $("classic-run").disabled = running || !spec || !ready(spec);
  $("classic-stop").hidden = !running;
  $("classic-answer").hidden = !running;
}

function render() {
  const spec = current();
  if (!spec) return;
  picked[spec.verb] ??= (spec.inputs ?? []).map(() => []);
  const [head, tail] = spec.verb.split(" ");
  const heads = [...new Set(specs.map((s) => s.verb.split(" ")[0]))];
  const pick = (verb) => { saved.verb = verb; save(); render(); };
  const rows = [seg(heads.map((h) => [h, h]), head, (h) => pick(specs.find((s) => s.verb.startsWith(h)).verb))];
  if (tail) {
    const subs = specs.filter((s) => s.verb.startsWith(head + " ")).map((s) => [s.verb, s.verb.split(" ")[1]]);
    rows.push(seg(subs, spec.verb, pick));
  }
  (spec.inputs ?? []).forEach((_, i) => rows.push(inputRow(spec, i)));
  rows.push(...spec.flags.filter((f) => !f.advanced).map(flagRow));
  const advanced = spec.flags.filter((f) => f.advanced);
  if (advanced.length) {
    const box = el("input", { type: "checkbox", checked: saved.advanced });
    box.onchange = () => { saved.advanced = box.checked; save(); render(); };
    rows.push(el("label", { className: "checkbox" }, box, "Показать дополнительные настройки"));
    if (saved.advanced) rows.push(...advanced.map(flagRow));
  }
  $("classic-form").replaceChildren(...rows);
  renderRun();
}

function log(text) {
  const out = $("classic-log");
  const stick = out.scrollTop + out.clientHeight >= out.scrollHeight - 4;
  out.append(text + "\n");
  while (out.childNodes.length > logLines) out.firstChild.remove();
  if (stick) out.scrollTop = out.scrollHeight;
}

Events.On("classic:line", (ev) => log(ev.data));
// The codes `compose telegram` stops on for want of an answer (exitNoStats and
// exitNoToken in the CLI's main.go). The CLI never asks, since agents run it
// unattended, so the question is asked here and the run is repeated with the
// answer added.
const questions = {
  5: { text: "В пакете нет «Взятий». Всё равно опубликовать?", ok: "Опубликовать", extra: () => ["--stop_if_no_stats=false"] },
  6: { text: "Нужен токен бота от @BotFather. Он сохранится, и в следующий раз спрашивать не придётся.", ok: "Запустить", secret: true, extra: (v) => ["--token", v] },
};

let extra = [];

function ask(q) {
  const input = q.secret ? el("input", { type: "password", autocomplete: "off", className: "input u-grow" }) : null;
  const ok = el("button", { type: "button", className: "btn btn-primary", textContent: q.ok });
  const cancel = el("button", { type: "button", className: "btn", textContent: "Отмена" });
  const close = () => { $("classic-ask").hidden = true; };
  ok.onclick = () => {
    if (input && !input.value.trim()) return input.focus();
    close();
    run(extra.concat(q.extra(input?.value.trim())));
  };
  cancel.onclick = close;
  if (input) input.onkeydown = (e) => { if (e.key === "Enter") ok.click(); };
  $("classic-ask").hidden = false;
  $("classic-ask").replaceChildren(el("div", { className: "card u-col u-gap-sm" },
    el("p", { className: "subhead", textContent: q.text }),
    el("div", { className: "u-row u-gap-sm u-align-center" }, ...[input, ok, cancel].filter(Boolean))));
  (input ?? ok).focus();
}

Events.On("classic:done", (ev) => {
  const { code, error } = ev.data;
  running = false;
  renderRun();
  if (questions[code]) return ask(questions[code]);
  log(code ? `Ошибка: ${error}` : "Готово.");
});

async function run(more = []) {
  extra = more;
  $("classic-ask").hidden = true;
  try {
    running = true;
    renderRun();
    await call("Run", argv(current(), extra));
  } catch (err) {
    running = false;
    renderRun();
    log(err.message ?? String(err));
  }
}

$("classic-run").onclick = () => run();
$("classic-stop").onclick = () => call("Stop");
$("classic-answer").onkeydown = async (e) => {
  if (e.key !== "Enter") return;
  // What is typed may be a passphrase, so it stays out of the log.
  const a = $("classic-answer");
  await call("Send", a.value);
  a.value = "";
};

try {
  specs = await call("Spec");
  render();
} catch (err) {
  $("classic-form").replaceChildren(el("p", { className: "hint hint-danger", textContent: err.message ?? String(err) }));
}
