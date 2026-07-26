// The «🧪 Тесты» panel: the board-level surface that replaced the тест-список.
// One row per Test Session — date, title, tester count — plus the two things a
// session is actually for: the invite line you paste into a messenger, and the
// «Вопросы тестировали» line you paste into a document.
//
// A create(deps) kernel like the board's others; board.ts owns the wiring.

import {
  type AnnounceCity, COMMON_CITIES, humanDate, inviteLine, type Mark, newKey,
  parseSession, serializeSession, type SessionMeta, sessionLabel,
} from "./sessions.js";
import type { BoardSession } from "./unlock.js";
import type { Tester } from "./chgk.js";
import * as people from "./people.js";

export interface SessionsPanelDeps {
  boardId: number;
  el(tag: string, props?: Record<string, unknown>, ...kids: unknown[]): HTMLElement;
  byId<T extends HTMLElement = HTMLElement>(id: string): T;
  sessions(): BoardSession[];
  boardName(): string;
  defaultTimezone(): string;
  defaultCities(): AnnounceCity[];
  marks(): Mark[];
  labelCountFor(sessionId: number): number;
  createSession(meta: string): Promise<number>;
  patchSession(id: number, meta: string): Promise<void>;
  deleteSession(id: number): Promise<void>;
  copyText(text: string): Promise<void>;
  // The session's лента: everything said about any question at this test, plus
  // the notes about the test itself. Decrypted by the caller, which owns the DK.
  loadNotes(sessionId: number): Promise<Array<{ text: string; card: number | null; when: string }>>;
  addNote(sessionId: number, text: string): Promise<void>;
  overlayOpen(el: HTMLElement, close: () => void): void;
  overlayClose(el: HTMLElement): void;
  render(): void;
}

export interface SessionsPanel {
  open(): void;
  openSession(id: number): void;
}

export function createSessionsPanel(deps: SessionsPanelDeps): SessionsPanel {
  const { el, byId } = deps;
  const overlay = byId("sessionsOverlay");
  const editOverlay = byId("sessionEditOverlay");
  let editing: number | null = null;
  let testerRows: (() => Tester[]) | null = null;

  function todayISO(): string {
    const d = new Date();
    const p = (n: number): string => String(n).padStart(2, "0");
    return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
  }

  function open(): void {
    renderList();
    overlay.hidden = false;
    deps.overlayOpen(overlay, close);
  }

  function close(): void {
    overlay.hidden = true;
  }

  // Sessions sort newest first by date — a better order than the manual rank a
  // list gave them, and the one you want when adding to the most recent test.
  function sorted(): Array<{ s: BoardSession; m: SessionMeta }> {
    return deps.sessions()
      .map((s) => ({ s, m: parseSession(s.meta) }))
      .sort((a, b) => (b.m.date || "").localeCompare(a.m.date || "") || b.s.id - a.s.id);
  }

  function renderList(): void {
    const box = byId("sessionsList");
    box.replaceChildren();
    const rows = sorted();
    if (!rows.length) {
      box.append(el("p", { class: "label-empty", text: "Тестов пока нет." }));
      return;
    }
    for (const { s, m } of rows) {
      const players = m.testers.filter((t) => t.type === "player").length;
      const teams = m.testers.filter((t) => t.type === "team").length;
      const counts: string[] = [];
      if (players) counts.push(`${players} игр.`);
      if (teams) counts.push(`${teams} ком.`);
      const marked = deps.labelCountFor(s.id);

      const head = el("div", { class: "sess-head" },
        el("span", { class: "sess-title", text: sessionLabel(m) || "(без даты)" }),
        el("span", { class: "sess-meta", text: [m.time, counts.join(", "), marked ? `${marked} мет.` : ""].filter(Boolean).join(" · ") }),
      );
      // A copied session says how stale it might be (ADR-0003) — its testers are
      // frozen at transfer time, and that list is what «Видели» reads.
      if (m.origin && m.origin.board) {
        head.append(el("span", {
          class: "sess-origin",
          title: "Скопирована с другой доски: список тестеров с тех пор мог измениться",
          text: `копия с «${m.origin.board}»${m.origin.at ? ` от ${humanDate(m.origin.at)}` : ""}`,
        }));
      }
      const row = el("div", { class: "sess-row" }, head,
        el("div", { class: "sess-actions" },
          el("button", { class: "input", type: "button", text: "Открыть", onclick: () => openSession(s.id) }),
          el("button", {
            class: "input", type: "button", text: "📋 Приглашение",
            title: "Скопировать строку со временем начала для мессенджера",
            onclick: () => { void deps.copyText(inviteLine(m)); },
          }),
        ));
      box.append(row);
    }
  }

  function openSession(id: number): void {
    const s = deps.sessions().find((x) => x.id === id);
    if (!s) return;
    editing = id;
    renderForm(parseSession(s.meta));
    editOverlay.hidden = false;
    deps.overlayOpen(editOverlay, closeEdit);
  }

  async function addSession(): Promise<void> {
    const meta: SessionMeta = {
      date: todayISO(),
      time: "",
      tz: deps.defaultTimezone() || Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC",
      title: "",
      testers: [],
      cities: deps.defaultCities(),
      key: newKey(),
    };
    try {
      const id = await deps.createSession(serializeSession(meta));
      renderList();
      openSession(id);
    } catch (_) {
      byId("sessionsMessage").textContent = "Не удалось создать тест.";
    }
  }

  // ---- the session form ----

  function renderForm(m: SessionMeta): void {
    const box = byId("sessionForm");
    box.replaceChildren();

    const dateInp = el("input", { class: "input", type: "date", value: m.date }) as HTMLInputElement;
    // Date only by default (issue #33): most tests never need a time, and the
    // ones that do get a zone with it.
    const timeInp = el("input", { class: "input", type: "time", value: m.time }) as HTMLInputElement;
    const tzInp = el("input", { class: "input", type: "text", value: m.tz, placeholder: "Europe/Moscow" }) as HTMLInputElement;
    const titleInp = el("input", { class: "input", type: "text", value: m.title, placeholder: "напр. «Алиев и др.»" }) as HTMLInputElement;

    box.append(
      field("Дата", dateInp),
      field("Время (необязательно)", timeInp),
      field("Часовой пояс времени", tzInp),
      field("Название", titleInp),
    );

    // Announce cities: the invite line's whole point.
    const cityBox = el("div", { class: "sess-cities" });
    const cities: AnnounceCity[] = (m.cities || []).slice();
    const drawCities = (): void => {
      cityBox.replaceChildren();
      for (const [i, c] of cities.entries()) {
        cityBox.append(el("span", { class: "label-pick is-on", text: `${c.name} ×`, onclick: () => { cities.splice(i, 1); drawCities(); previewInvite(); } }));
      }
      const pick = el("select", { class: "input" }) as HTMLSelectElement;
      pick.append(el("option", { value: "", text: "+ город…" }));
      for (const c of COMMON_CITIES) pick.append(el("option", { value: c.zone, text: c.name }));
      pick.append(el("option", { value: "__own", text: "свой…" }));
      pick.addEventListener("change", () => {
        if (!pick.value) return;
        if (pick.value === "__own") {
          const zone = (prompt("Часовой пояс (IANA), напр. Asia/Tbilisi:", "") || "").trim();
          const name = (prompt("Как назвать город в приглашении?", "") || "").trim();
          if (zone && name) cities.push({ zone, name });
        } else {
          const found = COMMON_CITIES.find((c) => c.zone === pick.value);
          if (found && !cities.some((c) => c.zone === found.zone)) cities.push(found);
        }
        pick.value = "";
        drawCities();
        previewInvite();
      });
      cityBox.append(pick);
    };
    drawCities();
    box.append(field("Города для приглашения", cityBox));

    const invitePreview = el("p", { class: "sess-invite" });
    const inviteCopy = el("button", { class: "input", type: "button", text: "📋 Скопировать приглашение" });
    const previewInvite = (): void => {
      invitePreview.textContent = inviteLine({ ...m, ...read(), cities });
    };
    inviteCopy.addEventListener("click", () => { void deps.copyText(invitePreview.textContent || ""); });
    for (const inp of [dateInp, timeInp, tzInp]) inp.addEventListener("input", previewInvite);
    box.append(el("div", { class: "sess-invite-box" }, invitePreview, inviteCopy));

    // Testers: one row each, name + игрок/команда, suggested from every board
    // this device has unlocked.
    const rows = el("div", { class: "fld-rows" });
    const addRow = (t: Tester | null): HTMLInputElement => {
      const seg = el("div", { class: "seg tester-seg" });
      const bP = el("button", { class: "seg-btn", type: "button", text: "игрок" });
      const bT = el("button", { class: "seg-btn", type: "button", text: "команда" });
      let type: Tester["type"] = t && t.type === "team" ? "team" : "player";
      const sync = (): void => { bP.classList.toggle("active", type === "player"); bT.classList.toggle("active", type === "team"); };
      bP.addEventListener("click", () => { type = "player"; sync(); });
      bT.addEventListener("click", () => { type = "team"; sync(); });
      seg.append(bP, bT); sync();
      const inp = el("input", { class: "input fld-row-input", type: "text", value: (t && t.text) || "", placeholder: "имя…", autocomplete: "off" }) as HTMLInputElement;
      attachSuggest(inp);
      const rm = el("button", { class: "fld-row-rm", type: "button", text: "×", title: "Удалить строку" });
      const row = el("div", { class: "fld-row tester-row" }, seg, inp, rm);
      rm.addEventListener("click", () => row.remove());
      (row as TesterRow)._read = () => ({ text: inp.value, type });
      rows.append(row);
      return inp;
    };
    (m.testers.length ? m.testers : [{ text: "", type: "player" as const }]).forEach((t) => addRow(t));
    const add = el("button", { class: "input fld-add-row", type: "button", text: "+ тестер" });
    add.addEventListener("click", () => addRow({ text: "", type: "player" }).focus());
    box.append(field("Тестировали", el("div", {}, rows, add)));
    testerRows = () => [...rows.querySelectorAll<TesterRow>(".tester-row")].map((r) => (r._read as () => Tester)());

    const summary = el("button", {
      class: "input", type: "button", text: "👥 Скопировать список тестеров",
      onclick: () => {
        const line = summaryLine(testerRows ? testerRows() : []);
        if (line) void deps.copyText(line);
      },
    });
    // The debrief: what was said at this test, across every question. Today that
    // is unrecoverable, because a comment records only which card it sits on.
    const notes = el("div", { class: "sess-notes" });
    const noteInput = el("input", { class: "input", type: "text", placeholder: "Заметка о тесте…" }) as HTMLInputElement;
    const noteAdd = el("button", { class: "input", type: "button", text: "Добавить" });
    noteAdd.addEventListener("click", () => { void postNote(noteInput); });
    box.append(field("Обсуждали на тесте", el("div", {}, notes, el("div", { class: "sess-actions" }, noteInput, noteAdd))));
    void drawNotes(notes);

    const save = el("button", { class: "input", type: "button", text: "Сохранить" });
    save.addEventListener("click", () => { void saveSession(m, cities); });
    const drop = el("button", { class: "input danger", type: "button", text: "🗑️ Удалить тест" });
    drop.addEventListener("click", () => { void removeSession(); });
    box.append(el("div", { class: "sess-actions" }, save, summary, drop));

    function read(): { date: string; time: string; tz: string; title: string } {
      return { date: dateInp.value, time: timeInp.value, tz: tzInp.value.trim(), title: titleInp.value.trim() };
    }
    formRead = () => read();
    previewInvite();
  }

  let formRead: (() => { date: string; time: string; tz: string; title: string }) | null = null;
  let notesBox: HTMLElement | null = null;

  async function drawNotes(box: HTMLElement): Promise<void> {
    notesBox = box;
    if (editing == null) return;
    box.replaceChildren(el("p", { class: "label-empty", text: "…" }));
    try {
      const notes = await deps.loadNotes(editing);
      box.replaceChildren();
      if (!notes.length) { box.append(el("p", { class: "label-empty", text: "Пока ничего." })); return; }
      for (const n of notes) {
        box.append(el("p", { class: "sess-note" },
          el("span", { class: "sess-meta", text: n.card ? "к вопросу · " : "о тесте · " }),
          el("span", { text: n.text })));
      }
    } catch (_) {
      box.replaceChildren(el("p", { class: "label-empty", text: "Не удалось загрузить." }));
    }
  }

  async function postNote(input: HTMLInputElement): Promise<void> {
    const text = input.value.trim();
    if (!text || editing == null) return;
    try {
      await deps.addNote(editing, text);
      input.value = "";
      if (notesBox) await drawNotes(notesBox);
    } catch (_) {
      byId("sessionEditMessage").textContent = "Не удалось добавить заметку.";
    }
  }

  function field(label: string, control: HTMLElement): HTMLElement {
    return el("div", { class: "fld fld-wide" },
      el("div", { class: "fld-head" }, el("span", { class: "fld-label", text: label })),
      control);
  }

  // summaryLine is the «Вопросы тестировали: …» line, terminated with a period.
  function summaryLine(testers: Tester[]): string {
    const parts = testers.map((t) => ({ text: (t.text || "").trim(), type: t.type })).filter((t) => t.text);
    if (!parts.length) return "";
    const players = parts.filter((t) => t.type === "player").map((t) => t.text);
    const teams = parts.filter((t) => t.type === "team").map((t) => t.text);
    let s = "";
    if (players.length) s = "Вопросы тестировали: " + players.join(", ");
    if (teams.length) s += (s ? ", а также команды: " : "Вопросы тестировали команды: ") + teams.join(", ");
    return s ? s + "." : "";
  }

  // attachSuggest hangs the person directory off a tester input: this board's
  // names first and unlabelled, then names from other unlocked boards tagged
  // with where they came from.
  function attachSuggest(inp: HTMLInputElement): void {
    let pop: HTMLElement | null = null;
    const dismiss = (): void => { if (pop) { pop.remove(); pop = null; } };
    inp.addEventListener("blur", () => setTimeout(dismiss, 150));
    inp.addEventListener("input", () => {
      dismiss();
      const hits = people.suggest(deps.boardId, inp.value);
      if (!hits.length || !inp.value.trim()) return;
      pop = el("div", { class: "menu-dropdown suggest-pop" });
      for (const h of hits) {
        pop.append(el("button", {
          class: "menu-item", type: "button",
          onmousedown: (e: Event) => { e.preventDefault(); inp.value = h.text; dismiss(); },
        }, el("span", { text: h.text }), h.board ? el("span", { class: "suggest-board", text: h.board }) : el("span")));
      }
      inp.parentElement?.append(pop);
    });
  }

  async function saveSession(prev: SessionMeta, cities: AnnounceCity[]): Promise<void> {
    if (editing == null || !formRead) return;
    const f = formRead();
    const meta: SessionMeta = {
      ...prev,
      date: f.date,
      time: f.time,
      tz: f.tz,
      title: f.title,
      cities,
      testers: testerRows ? testerRows() : prev.testers,
      key: prev.key || newKey(),
    };
    try {
      await deps.patchSession(editing, serializeSession(meta));
      byId("sessionEditMessage").textContent = "Сохранено.";
      renderList();
      deps.render();
    } catch (_) {
      byId("sessionEditMessage").textContent = "Не удалось сохранить.";
    }
  }

  async function removeSession(): Promise<void> {
    if (editing == null) return;
    if (!confirm("Удалить тест-сессию? Её метки исчезнут с карточек.")) return;
    try {
      await deps.deleteSession(editing);
      closeEdit();
      renderList();
      deps.render();
    } catch (_) {
      byId("sessionEditMessage").textContent = "Не удалось удалить.";
    }
  }

  function closeEdit(): void {
    editOverlay.hidden = true;
    editing = null;
    testerRows = null;
    formRead = null;
    deps.overlayClose(editOverlay);
  }

  byId("sessionAddBtn").addEventListener("click", () => { void addSession(); });
  byId("sessionsClose").addEventListener("click", close);
  byId("sessionEditClose").addEventListener("click", closeEdit);

  return { open, openSession };
}

interface TesterRow extends HTMLElement { _read?: () => Tester }
