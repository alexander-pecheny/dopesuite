// entry-model.ts — the pure kernel of od.js's entry grid (DopeEntryModel).
//
// The OD host types team placements into a grid; the fiddly, bug-prone bits are
// parsing a pasted spreadsheet block, coercing each pasted cell to a team
// number, and computing a column's "invert" (the teams NOT yet placed). Those
// are pure functions with no DOM or app state, pulled out here so they are
// unit-tested; od.js keeps the grid rendering, selection and persistence and
// calls into this.

export interface EntryTeam {
  label: string;
  number: number;
}

// parseClipboard splits pasted spreadsheet text into a grid of cell strings:
// rows by newline (CRLF/CR normalized), columns by tab. A single trailing
// blank line (Excel/Sheets append one) is dropped.
export function parseClipboard(text: string | null | undefined): string[][] {
  const normalized = String(text || "").replace(/\r\n/g, "\n").replace(/\r/g, "\n");
  const lines = normalized.split("\n");
  if (lines.length > 1 && lines[lines.length - 1] === "") lines.pop();
  return lines.map((line) => line.split("\t"));
}

// coerceValue maps one pasted cell to a team number: a bare integer passes
// through; otherwise it is matched (case-insensitively, ru locale) against a
// team label and resolved to that team's number; anything else is 0 (empty).
// teams is [{label, number}].
export function coerceValue(raw: string | null | undefined, teams: readonly EntryTeam[]): number {
  const value = String(raw || "").trim();
  if (value === "") return 0;
  if (/^\d+$/.test(value)) return Number(value);
  const lower = value.toLocaleLowerCase("ru");
  for (const team of teams) {
    if (String(team.label).toLocaleLowerCase("ru") === lower) return team.number;
  }
  return 0;
}

// invertColumn computes the invert of a question column: the team numbers NOT
// currently entered, ascending, packed into a fresh array of teamCount slots.
// Returns null when the result equals the current column (a no-op).
export function invertColumn(
  current: readonly number[] | null | undefined,
  allNumbers: readonly number[],
  teamCount: number,
): number[] | null {
  const cur = current || [];
  const present = new Set(cur.filter((v) => Number.isInteger(v) && v > 0));
  const complement = allNumbers.filter((n) => !present.has(n)).sort((a, b) => a - b);
  const next = new Array<number>(teamCount).fill(0);
  complement.forEach((n, i) => {
    if (i < next.length) next[i] = n;
  });
  let same = cur.length === next.length;
  for (let i = 0; same && i < next.length; i++) {
    if ((cur[i] || 0) !== next[i]) same = false;
  }
  return same ? null : next;
}

export const DopeEntryModel = { parseClipboard, coerceValue, invertColumn };
