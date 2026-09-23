// seen.ts — who saw a question (#90, ADR-0019). By default that is everyone at
// every Session the card has a Playing with. Two hand corrections change it, and
// both are stored on the card, encrypted, in cards.seen_enc:
//
//   - extra: people who saw the question outside any Session, such as the
//     people who saw it in another pool before it moved to this board.
//   - absent: testers of a Session who missed this one question, such as
//     somebody who came late and missed questions 1–3. Keyed by the Session's
//     `key`, which survives a Transfer (ADR-0003), so the absence travels with
//     the card and still names the same sitting on the other board.
//
// A person is their trimmed name, the same identity whoSaw and the Tester List
// already dedupe by. All pure.

import type { SessionMeta, Tester } from "./sessions.js";

export interface CardSeen {
  extra: Tester[];
  absent: Record<string, string[]>;
}

// One Playing as the fold needs it: which sitting, and who was there.
export interface SeenPlaying {
  ref: string;
  testers: ReadonlyArray<Tester>;
}

export const nameOf = (t: { text?: string | null }): string => (t.text || "").trim();

function cleanTester(v: unknown): Tester | null {
  if (!v || typeof v !== "object") return null;
  const o = v as Record<string, unknown>;
  const text = typeof o.text === "string" ? o.text.trim() : "";
  if (!text) return null;
  return { text, type: o.type === "team" ? "team" : "player" };
}

export function emptySeen(): CardSeen {
  return { extra: [], absent: {} };
}

// parseCardSeen reads the decrypted blob. null, garbage or a missing field all
// read as "no corrections", which is what a card without the column means.
export function parseCardSeen(raw: string | null | undefined): CardSeen {
  if (!raw) return emptySeen();
  let obj: Record<string, unknown> = {};
  try {
    const p: unknown = JSON.parse(raw);
    if (p && typeof p === "object") obj = p as Record<string, unknown>;
  } catch (_) {
    return emptySeen();
  }
  const extra = Array.isArray(obj.extra) ? obj.extra.map(cleanTester).filter((t): t is Tester => t != null) : [];
  const absent: Record<string, string[]> = {};
  if (obj.absent && typeof obj.absent === "object") {
    for (const [ref, names] of Object.entries(obj.absent as Record<string, unknown>)) {
      if (!Array.isArray(names)) continue;
      const clean = names.filter((n): n is string => typeof n === "string").map((n) => n.trim()).filter(Boolean);
      if (clean.length) absent[ref] = clean;
    }
  }
  return { extra, absent };
}

// serializeCardSeen is "" when there is nothing to store, which the PATCH reads
// as "clear the column".
export function serializeCardSeen(s: CardSeen): string {
  const absent: Record<string, string[]> = {};
  for (const [ref, names] of Object.entries(s.absent)) if (names.length) absent[ref] = names;
  if (!s.extra.length && !Object.keys(absent).length) return "";
  return JSON.stringify({
    ...(s.extra.length ? { extra: s.extra } : {}),
    ...(Object.keys(absent).length ? { absent } : {}),
  });
}

// sessionRef names a Session in `absent`. A session made before keys existed
// falls back to its row id; the card writes a key into such a session before it
// records an absence there (board.ts), so the fallback only covers reading.
export function sessionRef(id: number, meta: Pick<SessionMeta, "key"> | null): string {
  return (meta && meta.key) || `#${id}`;
}

// A person the card lists, and why.
export interface SeenPerson {
  tester: Tester;
  // The Sessions they saw it at, as refs. Empty for somebody added by hand.
  at: string[];
  byHand: boolean;
  // They were at one of the card's Sessions and missed this question there, and
  // did not see it any other way.
  absent: boolean;
}

// seenPeople is the whole picture the card draws: everyone who saw the question,
// plus the absent testers, who stay listed so the absence can be undone.
export function seenPeople(playings: ReadonlyArray<SeenPlaying>, seen: CardSeen): SeenPerson[] {
  const byName = new Map<string, SeenPerson>();
  const get = (t: Tester): SeenPerson => {
    const name = nameOf(t);
    let p = byName.get(name);
    if (!p) {
      p = { tester: { text: name, type: t.type === "team" ? "team" : "player" }, at: [], byHand: false, absent: false };
      byName.set(name, p);
    }
    return p;
  };
  const missed = new Set<string>();
  for (const pl of playings) {
    const gone = new Set(seen.absent[pl.ref] || []);
    for (const t of pl.testers) {
      const name = nameOf(t);
      if (!name) continue;
      if (gone.has(name)) { missed.add(name); get(t); continue; }
      get(t).at.push(pl.ref);
    }
  }
  for (const t of seen.extra) {
    if (nameOf(t)) get(t).byHand = true;
  }
  for (const p of byName.values()) p.absent = missed.has(nameOf(p.tester)) && !p.at.length && !p.byHand;
  return [...byName.values()];
}

// seenBy is who saw the question: the Playings' testers minus the absences, plus
// the people added by hand.
export function seenBy(playings: ReadonlyArray<SeenPlaying>, seen: CardSeen): Tester[] {
  return seenPeople(playings, seen).filter((p) => !p.absent).map((p) => p.tester);
}

// withSeen records that these people saw the question. Somebody absent from a
// Session comes back to it; somebody the Playings do not cover is added by hand.
// Absences for Sessions the card is no longer at are dropped on the way.
export function withSeen(seen: CardSeen, people: ReadonlyArray<Tester>, playings: ReadonlyArray<SeenPlaying>): CardSeen {
  const adding = new Map<string, Tester>();
  for (const t of people) {
    const name = nameOf(t);
    if (name) adding.set(name, { text: name, type: t.type === "team" ? "team" : "player" });
  }
  const absent: Record<string, string[]> = {};
  const covered = new Set<string>();
  for (const pl of playings) {
    const left = (seen.absent[pl.ref] || []).filter((n) => !adding.has(n));
    if (left.length) absent[pl.ref] = left;
    for (const t of pl.testers) if (!left.includes(nameOf(t))) covered.add(nameOf(t));
  }
  const extra = [...seen.extra];
  const have = new Set(extra.map(nameOf));
  for (const [name, t] of adding) {
    if (covered.has(name) || have.has(name)) continue;
    extra.push(t);
    have.add(name);
  }
  return { extra, absent };
}

// withoutSeen records that these people did not see the question: a hand-added
// one is taken off, and a tester of any of the card's Sessions is marked absent
// from it.
export function withoutSeen(seen: CardSeen, names: ReadonlyArray<string>, playings: ReadonlyArray<SeenPlaying>): CardSeen {
  const drop = new Set(names.map((n) => n.trim()).filter(Boolean));
  const absent: Record<string, string[]> = {};
  for (const pl of playings) {
    const list = [...(seen.absent[pl.ref] || [])];
    for (const t of pl.testers) {
      const name = nameOf(t);
      if (drop.has(name) && !list.includes(name)) list.push(name);
    }
    if (list.length) absent[pl.ref] = list;
  }
  return { extra: seen.extra.filter((t) => !drop.has(nameOf(t))), absent };
}

// A tour's Declaration, stored as a JSON list of testers (schema v26).
export function parseDeclaration(raw: string): Tester[] {
  try {
    const p: unknown = JSON.parse(raw);
    return Array.isArray(p) ? p.map(cleanTester).filter((t): t is Tester => t != null) : [];
  } catch (_) {
    return [];
  }
}

export function serializeDeclaration(people: ReadonlyArray<Tester>): string {
  return JSON.stringify(people.map((t) => ({ text: nameOf(t), type: t.type === "team" ? "team" : "player" })).filter((t) => t.text));
}
