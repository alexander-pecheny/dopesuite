// Test Sessions: one sitting at which a group of testers played a set of
// questions. A board-level entity (test_sessions.meta_enc), NOT a card in a
// list — see docs/labels-redesign.md and ADR-0003.
//
// Everything here is pure: parsing meta_enc, deriving a label's display name,
// and writing the invite line. The DOM lives in board.ts.

import { type Tester, type TesterLike, xyChgk } from "./chgk.js";

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
    testers: xyChgk.parseTestCard(raw).testers,
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

// parseTime: чч:мм, 24-hour. "" when it isn't one — including "7:00 PM", which
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

// Wall clock plus a zone is what the editor means — «19:00 по Москве» is an
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
//   20 июля, 19:00 (Берлин) / 21:00 (Москва) / 23:00 (Алматы)
//
// A city whose local date differs from the anchor's carries its own — 23:00
// Алматы can be tomorrow.
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
  const { players, teams } = xyChgk.testerNames(all);
  return [...players, ...teams].join(", ");
}
