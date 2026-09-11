// copyname.ts — what a clone is called before the reader renames it. Both copy
// surfaces (the board page's ☰ panel and the / list's tile button) prefill the
// name field, and the plain "X (copy)" is the wrong prefill the second time
// round: the reader ends up with two boards of one name and no way to tell them
// apart in the list. So the suffix counts — copy, copy 2, copy 3 — stopping at
// the first name no board already has. The catalog holds the actual wording.
//
// Nothing enforces this. The server has never required board names to be
// unique and still does not; this is the prefill being helpful, and the reader
// may type whatever they like over it.

import { xyApp } from "./app.js";
import { xySync } from "./sync.js";
import S from "./i18nstrings.js";

// How far the counter goes before giving up and handing back the plain suffix.
// A reader with thirty clones of one board does not want a thirty-first
// suggestion, they want to type a name.
const MAX_TRIES = 30;

interface NamedBoard {
  name?: string;
  schema_version?: number;
}

// suggestCopyName: the first suffixed name that `taken` does not hold. Names
// are compared trimmed, because that is how they are stored — the create and
// rename handlers both trim.
export function suggestCopyName(base: string, taken: Iterable<string>): string {
  const used = new Set<string>();
  for (const t of taken) used.add(t.trim());
  const plain = base + S.board.copy.nameSuffix();
  if (!used.has(plain.trim())) return plain;
  for (let n = 2; n <= MAX_TRIES; n++) {
    const candidate = base + S.board.copy.nameSuffixN(String(n));
    if (!used.has(candidate.trim())) return candidate;
  }
  return plain;
}

// takenBoardNames: the names this reader's boards already carry. The server is
// asked first — another device may have cloned since this one last looked — and
// the cached list answers when it cannot be reached, so an offline reader still
// gets a sensible suggestion instead of a collision.
//
// A legacy (schema_version 1) board keeps its name in name_enc and this device
// may not hold its key, so it contributes nothing: a clone of one can still
// collide, which costs a rename and nothing else.
export async function takenBoardNames(): Promise<string[]> {
  let rows: NamedBoard[] | null = null;
  try {
    rows = (await xyApp.fetchJSON("/api/boards")) as NamedBoard[];
  } catch {
    try {
      rows = ((await xySync.getBoardList()) as NamedBoard[] | undefined) ?? null;
    } catch { /* no list anywhere — suggest the plain suffix */ }
  }
  if (!rows) return [];
  return rows.filter((b) => (b.schema_version ?? 0) >= 2 && b.name).map((b) => b.name!);
}
