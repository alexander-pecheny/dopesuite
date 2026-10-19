// appeals.ts — the model of an appeals List (ADR-0021), all pure: what an
// appeal Card holds, the List's settings, the votes' tally and the status it
// gives, what a refresh from the rating site adds and changes, and what an
// export would send back. appealspanel.ts and the card editor draw it.

import S from "./i18nstrings.js";
import { xyRank } from "./rank.js";

// One appeal as api.rating.chgk.info returns it.
export interface SiteAppeal {
  id: number;
  type: string;
  questionNumber: number;
  answer?: string | null;
  appeal: string;
  status: string;
  comment?: string | null;
  controversial?: number | null;
  overriddenById?: number | null;
  issuedAt?: string;
}

// One controversial answer as the site returns it.
export interface SiteControversial {
  id: number;
  questionNumber: number;
  answer: string;
  status: string;
  comment?: string | null;
  appealJuryComment?: string | null;
}

// A controversial as an appeal Card keeps it: the answers on its question.
export interface Controversial {
  id: number;
  answer: string;
  status: string;
  comment: string;
}

export type Status = "" | "A" | "D";

// What an appeal Card's content is, as JSON.
export interface Appeal {
  id: number;
  // A asks to accept an answer, R for removing the question, N for removing it over the
  // host's mistake.
  type: string;
  question: number;
  answer: string;
  controversial: number | null;
  text: string;
  issuedAt: string;
  // When a refresh found the site's text different from the card's.
  textChangedAt: string | null;
  // What the site says about the appeal as of the last refresh or export.
  site: { status: string; comment: string; overriddenBy: number | null };
  controversials: Controversial[];
  // The jury's verdict. A status of "" follows the votes; "A" or "D" was set by
  // hand and wins over them.
  verdict: { status: Status; text: string };
}

// An appeals List's settings, kept in lists.meta.
export interface AppealsMeta {
  tournamentId: number | null;
  // The List holding the packet. When it is in a List Group, the whole group
  // is the packet.
  packetListId: number | null;
}

export type VoteValue = "for" | "against";
export interface Vote { cardId: number; userId: number; value: string }

const num = (v: unknown): number | null => (typeof v === "number" && Number.isFinite(v) ? v : null);
const str = (v: unknown): string => (typeof v === "string" ? v : "");
const status = (v: unknown): Status => (v === "A" || v === "D" ? v : "");

export function parseMeta(raw: string | null | undefined): AppealsMeta {
  try {
    const o = JSON.parse(raw || "{}") as Record<string, unknown>;
    return { tournamentId: num(o.tournament_id), packetListId: num(o.packet_list_id) };
  } catch { return { tournamentId: null, packetListId: null }; }
}

export function composeMeta(m: AppealsMeta): string {
  return JSON.stringify({ tournament_id: m.tournamentId, packet_list_id: m.packetListId });
}

// relinkPacket is the settings of an appeals list arriving on another board:
// the packet link follows the packet if it came along, and is dropped if not.
export function relinkPacket(raw: string, lists: ReadonlyMap<number, number>): string {
  const m = parseMeta(raw);
  return composeMeta({ ...m, packetListId: m.packetListId != null ? lists.get(m.packetListId) ?? null : null });
}

// parseAppeal reads a Card's content back, null for one that is not an appeal.
// The JSON is written by composeAppeal on one line, so the Version splitter in
// the card editor never finds a separator inside it.
export function parseAppeal(desc: string): Appeal | null {
  let o: Record<string, unknown>;
  try { o = JSON.parse(desc) as Record<string, unknown>; } catch { return null; }
  const id = num(o?.id);
  if (id == null) return null;
  const site = (o.site || {}) as Record<string, unknown>;
  const verdict = (o.verdict || {}) as Record<string, unknown>;
  return {
    id, type: str(o.type), question: num(o.question) ?? 0, answer: str(o.answer),
    controversial: num(o.controversial), text: str(o.text), issuedAt: str(o.issued_at),
    textChangedAt: str(o.text_changed_at) || null,
    site: { status: str(site.status), comment: str(site.comment), overriddenBy: num(site.overridden_by) },
    controversials: Array.isArray(o.controversials)
      ? (o.controversials as Array<Record<string, unknown>>).map((c) => ({ id: num(c.id) ?? 0, answer: str(c.answer), status: str(c.status), comment: str(c.comment) }))
      : [],
    verdict: { status: status(verdict.status), text: str(verdict.text) },
  };
}

export function composeAppeal(a: Appeal): string {
  return JSON.stringify({
    id: a.id, type: a.type, question: a.question, answer: a.answer, controversial: a.controversial,
    text: a.text, issued_at: a.issuedAt, text_changed_at: a.textChangedAt,
    site: { status: a.site.status, comment: a.site.comment, overridden_by: a.site.overriddenBy },
    controversials: a.controversials,
    verdict: a.verdict,
  });
}

export interface Tally { for: number; against: number }

export function tally(votes: ReadonlyArray<Vote>, cardId: number): Tally {
  const t = { for: 0, against: 0 };
  for (const v of votes) {
    if (v.cardId !== cardId) continue;
    if (v.value === "for") t.for++;
    else if (v.value === "against") t.against++;
  }
  return t;
}

// verdictStatus is what the jury decided: the status set by hand, or else the
// majority of the votes. A tie, or no votes, decides nothing.
export function verdictStatus(a: Appeal, t: Tally): { status: Status; byHand: boolean } {
  if (a.verdict.status) return { status: a.verdict.status, byHand: true };
  if (t.for > t.against) return { status: "A", byHand: false };
  if (t.against > t.for) return { status: "D", byHand: false };
  return { status: "", byHand: false };
}

// order is the List's order: by question, then the appealed answer, then the
// order the appeals were filed in. Removal appeals have no answer and lead
// their question.
export function compareAppeals(a: Appeal, b: Appeal): number {
  return a.question - b.question || a.answer.localeCompare(b.answer, "ru") || a.id - b.id;
}

function controversialsOn(question: number, all: ReadonlyArray<SiteControversial>): Controversial[] {
  return all
    .filter((c) => c.questionNumber === question)
    .map((c) => ({ id: c.id, answer: str(c.answer), status: str(c.status), comment: str(c.appealJuryComment) || str(c.comment) }))
    .sort((x, y) => x.answer.localeCompare(y.answer, "ru"));
}

function siteOf(s: SiteAppeal): Appeal["site"] {
  return { status: str(s.status), comment: clean(s.comment), overriddenBy: num(s.overriddenById) };
}

// The site keeps the text it was typed with, \r\n included.
const clean = (s: string | null | undefined): string => str(s).replace(/\r\n?/g, "\n").trim();

// fromSite is a new Card's content. A verdict the site already has is taken
// as set by hand, so the export finds nothing to send for it.
export function fromSite(s: SiteAppeal, controversials: ReadonlyArray<SiteControversial>): Appeal {
  const site = siteOf(s);
  return {
    id: s.id, type: str(s.type), question: s.questionNumber, answer: clean(s.answer),
    controversial: num(s.controversial), text: clean(s.appeal), issuedAt: str(s.issuedAt), textChangedAt: null,
    site, controversials: controversialsOn(s.questionNumber, controversials),
    verdict: { status: status(site.status), text: site.status === "A" || site.status === "D" ? site.comment : "" },
  };
}

export interface Held { cardId: number; rank: string; appeal: Appeal }

export interface RefreshPlan {
  // New appeals, each with the rank that puts it in order.
  create: Array<{ appeal: Appeal; rank: string }>;
  // Existing Cards whose content changed. Votes and the verdict are never
  // touched: only what the site says.
  update: Array<{ cardId: number; appeal: Appeal }>;
}

// planRefresh merges what the site has now into what the List holds.
export function planRefresh(held: ReadonlyArray<Held>, site: ReadonlyArray<SiteAppeal>, controversials: ReadonlyArray<SiteControversial>, now: string): RefreshPlan {
  const byId = new Map(held.map((h) => [h.appeal.id, h]));
  const plan: RefreshPlan = { create: [], update: [] };
  for (const s of site) {
    const h = byId.get(s.id);
    if (!h) continue;
    const next: Appeal = { ...h.appeal, site: siteOf(s), controversials: controversialsOn(h.appeal.question, controversials) };
    const text = clean(s.appeal);
    if (text !== h.appeal.text) { next.text = text; next.textChangedAt = now; }
    if (composeAppeal(next) !== composeAppeal(h.appeal)) plan.update.push({ cardId: h.cardId, appeal: next });
  }
  // A new appeal goes before the first held card that sorts after it, so a
  // list the jury has already moved around keeps its order.
  const sorted: Array<{ rank: string; appeal: Appeal }> = [...held].sort((x, y) => (x.rank < y.rank ? -1 : x.rank > y.rank ? 1 : 0));
  const fresh = site.filter((s) => !byId.has(s.id)).map((s) => fromSite(s, controversials)).sort(compareAppeals);
  for (const a of fresh) {
    let i = sorted.findIndex((h) => compareAppeals(h.appeal, a) > 0);
    if (i < 0) i = sorted.length;
    const lo = i > 0 ? sorted[i - 1].rank : null;
    const hi = i < sorted.length ? sorted[i].rank : null;
    // Two cards may share a rank, and then there is no key between them.
    let rank: string;
    try { rank = xyRank.keyBetween(lo, hi); } catch { rank = xyRank.keyBetween(lo, null); }
    sorted.splice(i, 0, { rank, appeal: a });
    plan.create.push({ appeal: a, rank });
  }
  return plan;
}

export interface Outgoing {
  cardId: number;
  appealId: number;
  status: "A" | "D";
  comment: string;
  siteStatus: string;
  siteComment: string;
}

// planExport is every decided appeal whose verdict the site does not have yet.
export function planExport(held: ReadonlyArray<Held>, votes: ReadonlyArray<Vote>, site: ReadonlyArray<SiteAppeal>): Outgoing[] {
  const now = new Map(site.map((s) => [s.id, s]));
  const out: Outgoing[] = [];
  for (const h of held) {
    const { status: st } = verdictStatus(h.appeal, tally(votes, h.cardId));
    const s = now.get(h.appeal.id);
    if (!st || !s) continue;
    const comment = h.appeal.verdict.text.trim();
    const siteComment = clean(s.comment);
    if (s.status === st && siteComment === comment) continue;
    out.push({ cardId: h.cardId, appealId: h.appeal.id, status: st, comment, siteStatus: str(s.status), siteComment });
  }
  return out.sort((x, y) => x.appealId - y.appealId);
}

// verdictRows is the verdict table the rating site takes as a file upload: one
// row per appeal, an undecided one with an empty status. It is for juries
// whose token cannot write.
export function verdictRows(held: ReadonlyArray<Held>, votes: ReadonlyArray<Vote>): (string | number)[][] {
  const rows = held.map(({ cardId, appeal: a }) =>
    [a.id, a.type, a.question, a.answer, verdictStatus(a, tally(votes, cardId)).status, a.verdict.text.trim()]);
  const head = [S.appeals.xlsx.headId(), S.appeals.xlsx.headType(), S.appeals.xlsx.headQuestion(), S.appeals.xlsx.headAnswer(), S.appeals.xlsx.headStatus(), S.appeals.xlsx.headComment()];
  return [head, ...rows];
}

// questionCard finds question N of the packet among its numbered Cards.
export function questionCard<C>(cards: ReadonlyArray<C>, numbers: ReadonlyArray<string | null>, question: number): C | null {
  const i = numbers.findIndex((n) => n === String(question));
  return i < 0 ? null : cards[i];
}

export const xyAppeals = {
  parseMeta, composeMeta, relinkPacket, parseAppeal, composeAppeal, tally, verdictStatus, compareAppeals,
  fromSite, planRefresh, planExport, verdictRows, questionCard,
};
