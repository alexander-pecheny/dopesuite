// bundle.ts — the Board Bundle's shape (ADR-0013): what board.json holds, how
// an attachment's file inside the zip is named, and the validation an untrusted
// file passes before an import touches the server. All pure; the encrypt/
// decrypt and the network live in bundleexport.ts / bundleimport.ts.

export const BUNDLE_FORMAT = "xy.board.v1";
export const BOARD_JSON = "board.json";

export interface BundleList {
  id: number;
  type: string;
  title: string;
  rank: string;
  group_id: number | null;
}
export interface BundleGroup {
  id: number;
  name: string;
}
export interface BundleCard {
  id: number;
  list_id: number;
  kind: string;
  description: string;
  rank: string;
  handout_meta: string | null;
  alias: string | null;
  created_at: string | null;
}
export interface BundleLabel {
  id: number;
  name: string;
  color: string;
}
export interface BundleSession {
  id: number;
  meta: string;
  created_at: string | null;
}
export interface BundleCardLabel {
  card_id: number;
  label_id: number;
  session_id: number | null;
}
export interface BundlePlaying {
  card_id: number;
  session_id: number;
}
export interface BundleTourTester {
  list_id: number | null;
  group_id: number | null;
  session_id: number | null;
}
export interface BundleEvent {
  id: number;
  card_id: number | null;
  session_id: number | null;
  type: string;
  author: string | null; // advisory username; re-matched on import, else dropped
  created_at: string;
  edited_at: string | null;
  is_excerpt: boolean;
  reply_to_id: number | null;
  payload: string;
}
export interface BundleAttachment {
  id: number;
  card_id: number;
  filename: string;
  mime: string;
  size: number; // ciphertext size on the source — the quota pre-check's estimate
  lossless: boolean;
  is_excerpt: boolean;
  path: string; // the entry inside the zip
}
export interface BundleMember {
  username: string;
  role: string;
}

export interface Bundle {
  format: typeof BUNDLE_FORMAT;
  exported_at: string;
  board: { name: string };
  members: BundleMember[]; // advisory only — import ignores it
  lists: BundleList[];
  groups: BundleGroup[];
  cards: BundleCard[];
  labels: BundleLabel[];
  sessions: BundleSession[];
  card_labels: BundleCardLabel[];
  card_sessions: BundlePlaying[];
  tour_testers: BundleTourTester[];
  timeline: BundleEvent[];
  attachments: BundleAttachment[];
}

// attachmentPath names an attachment's file inside the zip: the source id keeps
// two «раздатка.png» apart; the filename keeps the archive browsable. Path
// separators and control characters go, everything else (unicode included) stays.
export function attachmentPath(id: number, filename: string): string {
  // deno-lint-ignore no-control-regex
  const safe = filename.replace(/[/\\\x00-\x1f]/g, "_").replace(/^\.+/, "_") || "file";
  return `attachments/${id}-${safe}`;
}

export const EVENT_TYPES = new Set([
  "comment", "desc_edit", "label_add", "label_remove",
  "attach_add", "attach_remove", "attach_replace", "reaction",
]);

// parseBundle validates an untrusted board.json just enough that the import
// loop can trust ids to resolve and fields to have their declared types —
// every message names what is wrong, since the file may be hand-edited.
export function parseBundle(text: string): Bundle {
  let raw: unknown;
  try {
    raw = JSON.parse(text);
  } catch {
    throw new Error("board.json — не JSON");
  }
  const b = raw as Bundle;
  if (!b || typeof b !== "object") throw new Error("board.json — не объект");
  if (b.format !== BUNDLE_FORMAT) throw new Error(`неизвестный формат «${String(b.format)}» (ожидается ${BUNDLE_FORMAT})`);
  if (!b.board || typeof b.board.name !== "string" || !b.board.name.trim()) throw new Error("нет названия доски");
  const arrays: Array<keyof Bundle> = [
    "members", "lists", "groups", "cards", "labels", "sessions",
    "card_labels", "card_sessions", "tour_testers", "timeline", "attachments",
  ];
  for (const key of arrays) {
    if (!Array.isArray(b[key])) throw new Error(`${key} — не массив`);
  }

  const ids = (rows: Array<{ id: number }>, what: string): Set<number> => {
    const set = new Set<number>();
    for (const r of rows) {
      if (typeof r.id !== "number" || set.has(r.id)) throw new Error(`${what}: плохой или повторный id`);
      set.add(r.id);
    }
    return set;
  };
  const listIds = ids(b.lists, "lists");
  const groupIds = ids(b.groups, "groups");
  const cardIds = ids(b.cards, "cards");
  const labelIds = ids(b.labels, "labels");
  const sessionIds = ids(b.sessions, "sessions");

  const ref = (id: number | null | undefined, set: Set<number>, what: string): void => {
    if (id != null && !set.has(id)) throw new Error(`${what}: ссылка на несуществующий id ${id}`);
  };
  for (const l of b.lists) {
    if (typeof l.title !== "string" || typeof l.rank !== "string") throw new Error("lists: нет title/rank");
    ref(l.group_id, groupIds, "lists.group_id");
  }
  for (const c of b.cards) {
    if (typeof c.description !== "string" || typeof c.rank !== "string") throw new Error("cards: нет description/rank");
    ref(c.list_id, listIds, "cards.list_id");
  }
  for (const cl of b.card_labels) {
    ref(cl.card_id, cardIds, "card_labels.card_id");
    ref(cl.label_id, labelIds, "card_labels.label_id");
    ref(cl.session_id, sessionIds, "card_labels.session_id");
  }
  for (const p of b.card_sessions) {
    ref(p.card_id, cardIds, "card_sessions.card_id");
    ref(p.session_id, sessionIds, "card_sessions.session_id");
  }
  for (const t of b.tour_testers) {
    if ((t.list_id == null) === (t.group_id == null)) throw new Error("tour_testers: нужен ровно один из list_id / group_id");
    ref(t.list_id, listIds, "tour_testers.list_id");
    ref(t.group_id, groupIds, "tour_testers.group_id");
    ref(t.session_id, sessionIds, "tour_testers.session_id");
  }
  for (const e of b.timeline) {
    if (!EVENT_TYPES.has(e.type)) throw new Error(`timeline: неизвестный тип события «${String(e.type)}»`);
    if (e.card_id == null && e.session_id == null) throw new Error("timeline: событие ни на карточке, ни на тесте");
    ref(e.card_id, cardIds, "timeline.card_id");
    ref(e.session_id, sessionIds, "timeline.session_id");
    if (typeof e.payload !== "string") throw new Error("timeline: нет payload");
  }
  ids(b.attachments, "attachments");
  for (const a of b.attachments) {
    ref(a.card_id, cardIds, "attachments.card_id");
    if (typeof a.path !== "string" || typeof a.filename !== "string") throw new Error("attachments: нет path/filename");
  }
  return b;
}

// attachmentsTotal is what the import pre-checks against the importer's quota
// before creating anything: source ciphertext sizes, which re-encryption
// reproduces within an envelope's few dozen bytes.
export function attachmentsTotal(b: Bundle): number {
  return b.attachments.reduce((n, a) => n + (typeof a.size === "number" ? a.size : 0), 0);
}

// contentBytes estimates the whole board's quota footprint: attachments plus
// every text column the server's storageUsageSQL counts, so a text-heavy
// bundle fails the pre-check rather than the upload halfway in.
export function contentBytes(b: Bundle): number {
  const utf8 = (s: string | null): number => s ? new TextEncoder().encode(s).length : 0;
  let n = attachmentsTotal(b);
  for (const l of b.lists) n += utf8(l.title);
  for (const c of b.cards) n += utf8(c.description) + utf8(c.alias) + utf8(c.handout_meta);
  for (const l of b.labels) n += utf8(l.name) + utf8(l.color);
  for (const s of b.sessions) n += utf8(s.meta);
  for (const e of b.timeline) n += utf8(e.payload);
  return n;
}

export const xyBundle = { BUNDLE_FORMAT, BOARD_JSON, EVENT_TYPES, attachmentPath, parseBundle, attachmentsTotal, contentBytes };
