// The «🧪 Тесты» panel: the board-level surface that replaced the тест-список.
// One row per Test Session — date, title, tester count — plus the two things a
// session is actually for: the invite line you paste into a messenger, and the
// «Вопросы тестировали» line you paste into a document.
//
// A create(deps) kernel like the board's others; board.ts owns the wiring.

import {
  allZones, type AnnounceCity, formatDate, humanDate, inviteLine, newKey,
  parseDate, parseSession, parseTime, serializeSession, type SessionMeta,
  sessionLabel, zoneOffset,
} from "./sessions.js";
import { xyApp } from "./app.js";
import { TOWNS } from "./towns.js";
import { autocomplete, type Choice, townChoices, zoneChoices } from "./suggest.js";
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
  playedCount(sessionId: number): number;
  createSession(meta: string): Promise<number>;
  patchSession(id: number, meta: string): Promise<void>;
  deleteSession(id: number): Promise<void>;
  copyText(text: string): Promise<void>;
  // The session's лента: everything said about any question at this test, plus
  // the notes about the test itself. Decrypted by the caller, which owns the DK.
  loadNotes(sessionId: number): Promise<Array<{ text: string; card: number | null; when: string; author: string }>>;
  addNote(sessionId: number, text: string): Promise<void>;
  overlayOpen(el: HTMLElement, close: () => void, confirm?: () => Promise<boolean>): void;
  // Dismisses the topmost overlay, which is always the one this panel opened.
  overlayClose(): void;
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
      const marked = deps.playedCount(s.id);

      const head = el("div", { class: "sess-head" },
        el("span", { class: "sess-title", text: sessionLabel(m) || "(без даты)" }),
        el("span", { class: "sess-meta", text: [m.time, counts.join(", "), marked ? `${marked} вопр.` : ""].filter(Boolean).join(" · ") }),
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
    deps.overlayOpen(editOverlay, closeEdit, saveOnLeave);
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

  function renderForm(m: SessionMeta): void {
    const box = byId("sessionForm");
    box.replaceChildren();

    const dateInp = el("input", {
      class: "input", type: "text", value: formatDate(m.date), placeholder: "дд.мм.гггг", autocomplete: "off",
    }) as HTMLInputElement;
    // Date only by default (issue #33): most tests never need a time, and the
    // ones that do get a zone with it.
    const timeInp = el("input", {
      class: "input", type: "text", value: m.time, placeholder: "чч:мм", autocomplete: "off",
    }) as HTMLInputElement;
    const tzInp = el("input", {
      class: "input", type: "text", value: m.tz || deps.defaultTimezone(),
      placeholder: "Europe/Moscow", autocomplete: "off",
    }) as HTMLInputElement;
    autocomplete(tzInp, zoneChoices);
    const titleInp = el("input", { class: "input", type: "text", value: m.title, placeholder: "короткое имя теста — напр. «Алиев и др.»" }) as HTMLInputElement;

    box.append(
      field("Дата", dateInp),
      field("Время (необязательно)", timeInp),
      field("Часовой пояс", tzInp),
      field("Алиас", titleInp),
    );

    const cityBox = el("div", { class: "sess-cities" });
    const cities: AnnounceCity[] = (m.cities || []).slice();
    const drawCities = (): void => {
      cityBox.replaceChildren();
      for (const [i, c] of cities.entries()) {
        cityBox.append(el("span", { class: "city-chip", title: `${c.name} · ${c.zone}` },
          el("span", { class: "city-chip-name", text: c.name }),
          el("span", { class: "city-chip-zone", text: zoneOffset(c.zone) }),
          el("button", {
            class: "city-chip-x", type: "button", text: "×",
            title: "Убрать город", "aria-label": `Убрать ${c.name}`,
            onclick: () => { cities.splice(i, 1); drawCities(); previewInvite(); },
          })));
      }
      // A town brings its zone with it, so a city nobody could place — Тбилиси,
      // Кокшетау — needs no IANA knowledge from the person inviting.
      const add = el("input", {
        class: "input sess-city-add", type: "text", placeholder: "+ город…", autocomplete: "off",
      }) as HTMLInputElement;
      const addCity = (name: string, zone: string): void => {
        if (!name || cities.some((c) => c.name === name)) return;
        cities.push({ name, zone: zone || tzInp.value.trim() || m.tz });
        add.value = "";
        drawCities();
        previewInvite();
      };
      autocomplete(add, townChoices, (choice) => {
        const town = TOWNS.find((c) => c.name === choice.value);
        addCity(choice.value, (town && town.zone) || "");
      });
      // A town off the list still works: it takes the session's own zone, which
      // the user can then change on the chip's row.
      add.addEventListener("keydown", (e) => {
        if ((e as KeyboardEvent).key !== "Enter") return;
        e.preventDefault();
        const typed = add.value.trim();
        const town = TOWNS.find((c) => c.name.toLowerCase() === typed.toLowerCase());
        addCity(town ? town.name : typed, (town && town.zone) || "");
      });
      cityBox.append(add);
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
      autocomplete(inp, testerChoices);
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
    box.append(field("Тестировали", el("div", { class: "sess-testers" }, rows, add)));
    testerRows = () => [...rows.querySelectorAll<TesterRow>(".tester-row")].map((r) => (r._read as () => Tester)());

    // These come BEFORE the лента: they act on the fields above, and burying them
    // under a comment thread of unknown length puts them off the bottom. There is
    // no Сохранить — Готово saves, and so does every other way out.
    const summary = el("button", {
      class: "input", type: "button", text: "👥 Скопировать список тестеров",
      onclick: () => {
        const line = summaryLine(testerRows ? testerRows() : []);
        if (line) void deps.copyText(line);
      },
    });
    const drop = el("button", { class: "btn btn-danger", type: "button", text: "🗑️ Удалить тест" });
    drop.addEventListener("click", () => { void removeSession(); });
    box.append(el("div", { class: "sess-actions" }, summary, drop));

    // The debrief wears the card's OWN лента classes — .tl-event / .tl-meta /
    // .tl-comment, and .card-desc on the box — so a note here is the same markup
    // as a comment there rather than a lookalike.
    const notes = el("div", { class: "timeline sess-notes" });
    const noteInput = el("textarea", {
      class: "card-desc comment-input", placeholder: "Комментарий…", spellcheck: "false",
    }) as HTMLTextAreaElement;
    const noteAdd = el("button", { class: "btn", type: "button", text: "Отправить" });
    noteAdd.addEventListener("click", () => { void postNote(noteInput); });
    box.append(field("Лента", el("div", { class: "sess-feed" }, notes,
      el("div", { class: "u-col u-gap-sm" }, noteInput, noteAdd))));
    void drawNotes(notes);

    function read(): { date: string; time: string; tz: string; title: string } {
      return {
        date: parseDate(dateInp.value) || m.date,
        time: parseTime(timeInp.value),
        tz: tzInp.value.trim(),
        title: titleInp.value.trim(),
      };
    }
    formRead = () => read();
    formSave = () => saveSession(m, cities);
    previewInvite();
  }

  let formRead: (() => { date: string; time: string; tz: string; title: string }) | null = null;
  let formSave: (() => Promise<void>) | null = null;
  let notesBox: HTMLElement | null = null;

  async function drawNotes(box: HTMLElement): Promise<void> {
    notesBox = box;
    if (editing == null) return;
    box.replaceChildren(el("p", { class: "label-empty", text: "…" }));
    try {
      const notes = await deps.loadNotes(editing);
      box.replaceChildren();
      if (!notes.length) { box.append(el("p", { class: "label-empty", text: "Комментариев пока нет." })); return; }
      for (const n of notes) {
        const meta = [n.author, n.card ? "к вопросу" : "", shortWhen(n.when)].filter(Boolean).join(" · ");
        box.append(el("div", { class: "tl-event tl-comment" },
          el("div", { class: "tl-meta", text: meta }),
          el("div", { class: "tl-comment", text: n.text })));
      }
    } catch (_) {
      box.replaceChildren(el("p", { class: "label-empty", text: "Не удалось загрузить." }));
    }
  }

  function shortWhen(iso: string): string {
    const d = new Date(iso);
    return Number.isNaN(d.getTime()) ? "" : d.toLocaleString("ru-RU");
  }

  async function postNote(input: HTMLTextAreaElement): Promise<void> {
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

  function testerChoices(q: string): Choice[] {
    if (!q.trim()) return [];
    return people.suggest(deps.boardId, q).map((s) => ({ value: s.text, label: s.text, hint: s.board }));
  }

  // Throws on failure, so saveOnLeave can keep you on the form. Identical meta
  // is not sent: opening a test and closing it again should cost nothing.
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
    const next = serializeSession(meta);
    if (next === serializeSession(prev)) return;
    await deps.patchSession(editing, next);
    renderList();
    deps.render();
  }

  // The form has no Отмена: every field on it is a fact about the test, so any
  // exit saves, and only a save that FAILS keeps you here (the stack's gate).
  async function saveOnLeave(): Promise<boolean> {
    if (editing == null || !formSave) return true;
    try {
      await formSave();
      return true;
    } catch (_) {
      byId("sessionEditMessage").textContent = "Не удалось сохранить.";
      return false;
    }
  }

  async function removeSession(): Promise<void> {
    if (editing == null) return;
    if (!confirm("Удалить тест-сессию? Её метки исчезнут с карточек.")) return;
    try {
      await deps.deleteSession(editing);
      editing = null; // nothing left to save on the way out
      dismiss();
      renderList();
      deps.render();
    } catch (_) {
      byId("sessionEditMessage").textContent = "Не удалось удалить.";
    }
  }

  // The teardown the stack runs; `dismiss` is the ask. One function doing both
  // popped the stack twice and took the Тесты panel down with the form.
  function closeEdit(): void {
    editOverlay.hidden = true;
    editing = null;
    testerRows = null;
    formRead = null;
    formSave = null;
  }

  const dismiss = (): void => deps.overlayClose();

  // The form has no submit button — leaving it is what saves (saveOnLeave). So
  // Cmd/Ctrl-Enter means the same thing every other editor's does here: commit
  // and get out.
  xyApp.onCmdEnter(byId("sessionForm"), dismiss);

  byId("sessionAddBtn").addEventListener("click", () => { void addSession(); });
  byId("sessionsClose").addEventListener("click", dismiss);
  byId("sessionEditClose").addEventListener("click", dismiss);
  overlay.addEventListener("pointerdown", (e) => { if (e.target === overlay) dismiss(); });
  editOverlay.addEventListener("pointerdown", (e) => { if (e.target === editOverlay) dismiss(); });

  return { open, openSession };
}

interface TesterRow extends HTMLElement { _read?: () => Tester }
