// The Person Directory: the tester names this device has seen, gathered from
// every board whose key it holds. A suggestion source for typing a session's
// testers — never an identity, never synced, never a server entity.
//
// Deliberately outside the ciphertext-only rule: the same device caches every
// unlocked board's DK in `xy-keys`, so anything that can read this can already
// decrypt the questions. Names are not the secret here. Forgetting a board's
// password purges its entry (forget below) — that is the one case where the
// cache would outlive the key that justified it.

import type { Tester } from "./sessions.js";

const KEY = "xy.people";

interface Entry {
  board: string; // the board's plaintext name, shown as "where this came from"
  names: Tester[];
}

type Store = Record<string, Entry>; // board id → entry

function read(): Store {
  try {
    const raw = localStorage.getItem(KEY);
    if (!raw) return {};
    const parsed: unknown = JSON.parse(raw);
    return parsed && typeof parsed === "object" ? (parsed as Store) : {};
  } catch (_) {
    return {};
  }
}

function write(store: Store): void {
  try {
    localStorage.setItem(KEY, JSON.stringify(store));
  } catch (_) { /* a full or disabled localStorage costs a suggestion, nothing more */ }
}

export function remember(boardId: number, boardName: string, names: ReadonlyArray<Tester>): void {
  const seen = new Set<string>();
  const uniq: Tester[] = [];
  for (const t of names) {
    const text = (t.text || "").trim();
    if (!text) continue;
    const type = t.type === "team" ? "team" : "player";
    const k = type + "\u0000" + text;
    if (seen.has(k)) continue;
    seen.add(k);
    uniq.push({ text, type });
  }
  const store = read();
  if (!uniq.length) {
    delete store[String(boardId)];
  } else {
    store[String(boardId)] = { board: boardName, names: uniq };
  }
  write(store);
}

// forget drops a board's names. Wired to «Забыть пароль доски»: once the DK is
// gone the board's content is ciphertext with no key on this device, and its
// names should not still be sitting in the clear.
export function forget(boardId: number): void {
  const store = read();
  delete store[String(boardId)];
  write(store);
}

export interface Suggestion {
  text: string;
  type: "player" | "team";
  board: string; // "" when the name is from the board being edited
}

export function suggest(boardId: number, query: string, limit = 8): Suggestion[] {
  const q = query.trim().toLowerCase();
  const store = read();
  const mine: Suggestion[] = [];
  const others: Suggestion[] = [];
  const seen = new Set<string>();
  for (const [id, entry] of Object.entries(store)) {
    const own = id === String(boardId);
    for (const t of entry.names || []) {
      const text = t.text || "";
      if (q && !text.toLowerCase().includes(q)) continue;
      if (seen.has(text)) continue;
      seen.add(text);
      const s: Suggestion = { text, type: t.type === "team" ? "team" : "player", board: own ? "" : entry.board };
      (own ? mine : others).push(s);
    }
  }
  const byText = (a: Suggestion, b: Suggestion): number => a.text.localeCompare(b.text, "ru");
  return [...mine.sort(byText), ...others.sort(byText)].slice(0, limit);
}
