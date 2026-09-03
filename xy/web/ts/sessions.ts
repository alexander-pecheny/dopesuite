// Test Sessions: one sitting at which a group of testers played a set of
// questions. A board-level entity (test_sessions.meta_enc), NOT a card in a
// list — see docs/labels-redesign.md and ADR-0003.
//
// Everything here is pure: parsing meta_enc, deriving a label's display name,
// writing the invite line, and the tester lists (players / teams) of the test
// cards the sessions grew out of. The DOM lives in board.ts and sessionspanel.ts.

import S from "./i18nstrings.js";

// ---- test cards: tester lists (players / teams) ----
// A test card's description is JSON {datetime, title, testers:[{text,type}]},
// where type is "player" or "team". The first iteration stored {players:[ids]}
// (integer rating.chgk.info ids that were never resolvable client-side);
// parseTestCard folds that legacy shape forward, turning each id into a
// player-typed string so nothing is silently dropped on migration.

export type TesterType = "player" | "team";
export interface Tester { text: string; type: TesterType }
export interface TestCardModel { datetime: string; title: string; testers: Tester[] }

// The lax shape serializeTestCard/testersToText/testerCopyText accept: anything
// tester-ish, normalized on the way through.
export interface TesterLike { text?: string | null; type?: string | null }
export interface TestCardDraft {
  datetime?: string | null;
  title?: string | null;
  testers?: ReadonlyArray<TesterLike> | null;
}

// parseTestCard: JSON desc → {datetime, title, testers:[{text,type}]}.
export function parseTestCard(desc: string): TestCardModel {
  let m: unknown;
  try {
    m = JSON.parse(desc);
  } catch (_) {
    m = null;
  }
  const obj: Record<string, unknown> = m && typeof m === "object" ? (m as Record<string, unknown>) : {};
  let testers: unknown[] | null = Array.isArray(obj.testers) ? (obj.testers as unknown[]) : null;
  if (!testers) {
    // legacy {players:[ids]} → player-typed strings (see note above).
    const legacy: unknown[] = Array.isArray(obj.players) ? (obj.players as unknown[]) : [];
    testers = legacy.map((p) => ({ text: String(p == null ? "" : p), type: "player" }));
  }
  const clean = testers
    .filter((t): t is Record<string, unknown> => Boolean(t) && typeof t === "object")
    .map((t): Tester => ({ text: String(t.text == null ? "" : t.text), type: t.type === "team" ? "team" : "player" }));
  return {
    datetime: typeof obj.datetime === "string" ? obj.datetime : "",
    title: typeof obj.title === "string" ? obj.title : "",
    testers: clean,
  };
}

// serializeTestCard: {datetime, title, testers} → JSON desc, dropping blank rows.
export function serializeTestCard(m: TestCardDraft): string {
  const testers = (m.testers || [])
    .map((t): Tester => ({ text: (t.text || "").trim(), type: t.type === "team" ? "team" : "player" }))
    .filter((t) => t.text);
  return JSON.stringify({ datetime: m.datetime || "", title: m.title || "", testers });
}

// testersToText: testers[] → plaintext, "- name" (player) / "-T name" (team).
export function testersToText(testers: ReadonlyArray<TesterLike> | null | undefined): string {
  return (testers || []).map((t) => (t.type === "team" ? "-T " : "- ") + (t.text || "")).join("\n");
}

// testersFromText: plaintext → testers[]. A "-T" prefix (Latin or Cyrillic T,
// followed by whitespace) marks a team; any other leading dash marks a player.
export function testersFromText(text: string | null | undefined): Tester[] {
  const out: Tester[] = [];
  for (const raw of String(text == null ? "" : text).split("\n")) {
    const line = raw.trim();
    if (!line) continue;
    let type: TesterType = "player", body = line;
    if (/^-[TtТт](?=\s|$)/.test(line)) { type = "team"; body = line.slice(2); }
    else if (line[0] === "-") { body = line.slice(1); }
    body = body.trim();
    if (body) out.push({ text: body, type });
  }
  return out;
}

// testerSortKey returns the [surname, given] comparison key for a player name:
// the last whitespace-separated word is the surname, the rest the given name(s),
// so "Alexander Ivanov" sorts under "Ivanov", then "Alexander".
function testerSortKey(name: string | null | undefined): [string, string] {
  const words = String(name || "").trim().split(/\s+/).filter(Boolean);
  if (!words.length) return ["", ""];
  const surname = words[words.length - 1];
  return [surname, words.slice(0, -1).join(" ")];
}

// testerNames dedupes and orders testers: players by surname-then-given, teams
// alphabetically. Split out of testerCopyText because the "Seen" line on a
// card wants the names WITHOUT the "Questions tested:" framing.
export function testerNames(testers: ReadonlyArray<TesterLike> | null | undefined): { players: string[]; teams: string[] } {
  const seen: Record<TesterType, Set<string>> = { player: new Set(), team: new Set() };
  const players: string[] = [], teams: string[] = [];
  for (const t of testers || []) {
    const text = ((t && t.text) || "").trim();
    if (!text) continue;
    const type: TesterType = t.type === "team" ? "team" : "player";
    if (seen[type].has(text)) continue;
    seen[type].add(text);
    (type === "team" ? teams : players).push(text);
  }
  players.sort((a, b) => {
    const ka = testerSortKey(a), kb = testerSortKey(b);
    return ka[0].localeCompare(kb[0], "ru") || ka[1].localeCompare(kb[1], "ru");
  });
  teams.sort((a, b) => a.localeCompare(b, "ru"));
  return { players, teams };
}

// testerCopyText flattens testers into the shareable line. "" when there are none.
export function testerCopyText(testers: ReadonlyArray<TesterLike> | null | undefined): string {
  const { players, teams } = testerNames(testers);
  let s = "";
  if (players.length) s = S.sessions.summary.players(players.join(", "));
  if (teams.length) s += (s ? S.sessions.summary.teamsAlso(teams.join(", ")) : S.sessions.summary.teamsOnly(teams.join(", ")));
  return s;
}

// A session's decrypted meta. `time`/`tz` are optional because most tests only
// ever get a date (issue #33). `key` identifies the SITTING, not the row: a
// session copied to another board keeps it, which is what stops a second
// question from the same test creating a twin there.
export interface SessionMeta {
  date: string; // YYYY-MM-DD
  time: string; // HH:MM, "" when the session is date-only
  tz: string; // IANA zone the time is written in
  title: string;
  testers: Tester[];
  cities: AnnounceCity[];
  key: string;
  origin?: SessionOrigin;
}

export interface AnnounceCity {
  zone: string;
  name: string;
}

export interface SessionOrigin {
  board: string;
  at: string;
}

export interface Session {
  id: number;
  meta: SessionMeta;
}

export const COMMON_CITIES: AnnounceCity[] = [
  { zone: "Europe/Moscow", name: "Москва" },
  { zone: "Europe/Kyiv", name: "Киев" },
  { zone: "Europe/Minsk", name: "Минск" },
  { zone: "Europe/Berlin", name: "Берлин" },
  { zone: "Europe/Belgrade", name: "Белград" },
  { zone: "Europe/Lisbon", name: "Лиссабон" },
  { zone: "Europe/London", name: "Лондон" },
  { zone: "Asia/Tbilisi", name: "Тбилиси" },
  { zone: "Asia/Yerevan", name: "Ереван" },
  { zone: "Asia/Almaty", name: "Алматы" },
  { zone: "Asia/Tashkent", name: "Ташкент" },
  { zone: "Asia/Jerusalem", name: "Тель-Авив" },
  { zone: "Asia/Vladivostok", name: "Владивосток" },
  { zone: "Asia/Yekaterinburg", name: "Екатеринбург" },
  { zone: "Asia/Novosibirsk", name: "Новосибирск" },
  { zone: "America/New_York", name: "Нью-Йорк" },
];

const MONTHS = [
  "января", "февраля", "марта", "апреля", "мая", "июня",
  "июля", "августа", "сентября", "октября", "ноября", "декабря",
];

function str(v: unknown): string {
  return typeof v === "string" ? v : "";
}

// parseSession folds every shape meta_enc has ever held into the current one.
// migrateV18 moved the old test card's ciphertext across verbatim — it could not
// decrypt to reshape it — so the legacy {datetime,title,testers} arrives here
// untouched and is split into date/time on read. The same trick chgk.ts already
// plays for the older {players:[ids]} shape.
export function parseSession(raw: string): SessionMeta {
  let obj: Record<string, unknown> = {};
  try {
    const p: unknown = JSON.parse(raw);
    if (p && typeof p === "object") obj = p as Record<string, unknown>;
  } catch (_) { /* an unparseable session still renders as a blank one */ }

  let date = str(obj.date);
  let time = str(obj.time);
  if (!date) {
    const [d, t] = str(obj.datetime).split(" ");
    date = d || "";
    time = t || "";
  }
  const cities = Array.isArray(obj.cities)
    ? (obj.cities as unknown[])
      .filter((c): c is Record<string, unknown> => Boolean(c) && typeof c === "object")
      .map((c): AnnounceCity => ({ zone: str(c.zone), name: str(c.name) }))
      .filter((c) => c.zone && c.name)
    : [];
  const origin = obj.origin && typeof obj.origin === "object"
    ? { board: str((obj.origin as Record<string, unknown>).board), at: str((obj.origin as Record<string, unknown>).at) }
    : undefined;

  return {
    date,
    time,
    tz: str(obj.tz),
    title: str(obj.title),
    testers: parseTestCard(raw).testers,
    cities,
    key: str(obj.key),
    ...(origin && origin.board ? { origin } : {}),
  };
}

export function serializeSession(m: SessionMeta): string {
  return JSON.stringify({
    date: m.date,
    time: m.time,
    tz: m.tz,
    title: m.title,
    testers: (m.testers || []).map((t: TesterLike) => ({
      text: (t.text || "").trim(),
      type: t.type === "team" ? "team" : "player",
    })).filter((t) => t.text),
    cities: m.cities || [],
    key: m.key,
    ...(m.origin ? { origin: m.origin } : {}),
  });
}

export function newKey(): string {
  return crypto.randomUUID();
}

export type TitleMode = "date-title" | "title" | "date";

export function sessionLabel(m: SessionMeta, mode: TitleMode = "date-title"): string {
  const date = humanDate(m.date);
  if (mode === "date") return date || m.title;
  if (mode === "title") return m.title || date;
  return m.title ? (date ? `${date} · ${m.title}` : m.title) : date;
}

export function humanDate(date: string): string {
  const [y, mo, d] = (date || "").split("-").map(Number);
  if (!y || !mo || !d) return date || "";
  return `${d} ${MONTHS[mo - 1] || ""}`.trim();
}

const pad = (n: number): string => String(n).padStart(2, "0");

// ---- date and time as the UI writes them ----
//
// Native <input type="date"|"time"> render in the BROWSER's locale, not the
// document's, so Chrome shows 02/23/2026 and 07:00 PM to anyone whose browser
// is en-US however the page is marked up. These are the deterministic
// alternative: text in, ISO stored.

// formatDate: 2026-02-23 → 23.02.2026. Anything unparseable passes through, so
// a half-typed value is never eaten.
export function formatDate(iso: string): string {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(iso || "");
  return m ? `${m[3]}.${m[2]}.${m[1]}` : (iso || "");
}

// parseDate: 23.02.2026 → 2026-02-23, "" when it isn't a real date. Accepts
// 23.2.2026 and 23/02/2026 too — both are what people actually type.
export function parseDate(human: string): string {
  const m = /^(\d{1,2})[.\/-](\d{1,2})[.\/-](\d{4})$/.exec((human || "").trim());
  if (!m) return "";
  const d = Number(m[1]), mo = Number(m[2]), y = Number(m[3]);
  if (mo < 1 || mo > 12 || d < 1 || d > 31) return "";
  const probe = new Date(Date.UTC(y, mo - 1, d));
  if (probe.getUTCMonth() !== mo - 1 || probe.getUTCDate() !== d) return ""; // 31.02
  return `${y}-${pad(mo)}-${pad(d)}`;
}

// parseTime: hh:mm, 24-hour. "" when it isn't one — including "7:00 PM", which
// is exactly the input this replaces.
export function parseTime(human: string): string {
  const m = /^(\d{1,2})[:.](\d{2})$/.exec((human || "").trim());
  if (!m) return "";
  const h = Number(m[1]), min = Number(m[2]);
  if (h > 23 || min > 59) return "";
  return `${pad(h)}:${pad(min)}`;
}

export function zoneOffset(zone: string, at: Date = new Date()): string {
  try {
    const fmt = new Intl.DateTimeFormat("en-US", { timeZone: zone, timeZoneName: "longOffset" });
    const part = fmt.formatToParts(at).find((p) => p.type === "timeZoneName");
    const raw = (part && part.value) || "GMT";
    const norm = raw.replace("GMT", "UTC");
    return norm === "UTC" ? "UTC+0" : norm.replace(/:00$/, "").replace(/UTC([+-])0(\d)/, "UTC$1$2");
  } catch (_) {
    return "";
  }
}

export function guessZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "";
  } catch (_) {
    return "";
  }
}

export function allZones(): string[] {
  try {
    const f = Intl as unknown as { supportedValuesOf?: (k: string) => string[] };
    return f.supportedValuesOf ? f.supportedValuesOf("timeZone") : [];
  } catch (_) {
    return [];
  }
}

// ---- the invite line ----

// Wall clock plus a zone is what the editor means — "19:00 Moscow time" is an
// anchor, not an instant — so the other cities are computed FROM that pairing
// rather than from a stored timestamp.
function anchorInstant(m: SessionMeta): Date | null {
  if (!m.date || !m.time) return null;
  const [y, mo, d] = m.date.split("-").map(Number);
  const [hh, mm] = m.time.split(":").map(Number);
  if (!y || !mo || !d || Number.isNaN(hh) || Number.isNaN(mm)) return null;
  const zone = m.tz || "UTC";
  // Guess UTC, measure how far off the target zone reads, correct once. Zone
  // offsets are whole minutes, so one pass is exact.
  const guess = Date.UTC(y, mo - 1, d, hh, mm);
  const seen = zonedParts(new Date(guess), zone);
  const drift = Date.UTC(seen.y, seen.mo - 1, seen.d, seen.hh, seen.mm) - guess;
  return new Date(guess - drift);
}

interface Parts { y: number; mo: number; d: number; hh: number; mm: number }

function zonedParts(at: Date, zone: string): Parts {
  const fmt = new Intl.DateTimeFormat("en-CA", {
    timeZone: zone,
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  });
  const got: Record<string, string> = {};
  for (const p of fmt.formatToParts(at)) got[p.type] = p.value;
  return {
    y: Number(got.year),
    mo: Number(got.month),
    d: Number(got.day),
    // Some engines render midnight as hour 24 under hour12:false.
    hh: Number(got.hour) % 24,
    mm: Number(got.minute),
  };
}

// inviteLine is the one artifact that leaves xy: a line to paste into the
// messenger where the testers actually are. 99% of them will never have an
// account, so this is an outbound string, not a rendering preference.
//
//   20 July, 19:00 (Berlin) / 21:00 (Moscow) / 23:00 (Almaty)
//
// A city whose local date differs from the anchor's carries its own — 23:00
// Almaty can be tomorrow.
export function inviteLine(m: SessionMeta): string {
  const head = humanDate(m.date);
  if (!m.time) return head;
  const at = anchorInstant(m);
  if (!at) return head ? `${head}, ${m.time}` : m.time;

  const cities = m.cities && m.cities.length
    ? m.cities
    : [{ zone: m.tz || "UTC", name: m.tz || "" }];
  const anchorDay = m.date;
  const parts = cities.map((c) => {
    const p = zonedParts(at, c.zone);
    const day = `${p.y}-${pad(p.mo)}-${pad(p.d)}`;
    const clock = `${pad(p.hh)}:${pad(p.mm)}`;
    const where = c.name ? ` (${c.name})` : "";
    return day === anchorDay ? `${clock}${where}` : `${clock}${where} — ${humanDate(day)}`;
  });
  return head ? `${head}, ${parts.join(" / ")}` : parts.join(" / ");
}

// whoSaw is every tester from every session a card is tagged with, deduped.
// Sessions that arrived as copies from other boards are in here too — that is
// the case the whole refactor exists for.
export function whoSaw(sessions: ReadonlyArray<SessionMeta>): string {
  const all: Tester[] = [];
  for (const s of sessions) all.push(...(s.testers || []));
  const { players, teams } = testerNames(all);
  return [...players, ...teams].join(", ");
}

export interface SeenQuestion {
  num: string;
  testers: ReadonlyArray<Tester>;
}

// The inverse of whoSaw: whoSaw names the people a tour's preamble covers, who
// then know not to play it — this names everyone ELSE who saw some of it, and
// which questions, because those have to be warned one question at a time.
export function partialSeen(questions: ReadonlyArray<SeenQuestion>, named: ReadonlySet<string>): string {
  const byName = new Map<string, string[]>();
  for (const q of questions) {
    for (const t of q.testers) {
      const name = (t.text || "").trim();
      if (!name || named.has(name)) continue;
      const nums = byName.get(name) || [];
      if (!nums.includes(q.num)) nums.push(q.num);
      byName.set(name, nums);
    }
  }
  const parts = [...byName.entries()]
    .sort((a, b) => a[0].localeCompare(b[0], "ru"))
    .map(([name, nums]) => `${name}: ${nums.join(", ")}`);
  return parts.length ? S.sessions.seen.partial(parts.join("; ")) : "";
}
