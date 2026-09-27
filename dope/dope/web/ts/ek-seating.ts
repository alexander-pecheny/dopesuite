// The seating — who a team sent to play one theme, and how their names read
// in a cell five question columns wide. EK seats one player and prints the
// whole name; Erudit-Sextet seats up to three and prints their surnames in
// full, one line. A line wider than the cell fades out at its edge, and the
// cell's popover then lists the full names.
//
// Pure, so ek.ts and score-table.ts's live patch print the same string and
// jstest can hold the rule to worked examples.

// surnameOf is the family name inside a display name. The roster stores the
// given name and the surname separately and joins them in that order
// (store.JoinPlayerName), so the surname is the last word; a name of one word
// is all there is.
export function surnameOf(name: string): string {
  const words = String(name || "").trim().split(/\s+/).filter(Boolean);
  return words.length === 0 ? "" : words[words.length - 1];
}

// seatingLabel is the whole seating as one line: every seated player's
// surname, in the order the state lists them, which carries no meaning.
export function seatingLabel(seated: ReadonlyArray<string>): string {
  return seated.map(surnameOf).filter(Boolean).join(", ");
}

// seatedNames is a theme's seating with the blanks dropped — what a cell
// prints and what the statistics count as having played the theme.
export function seatedNames(players: ReadonlyArray<string | null | undefined> | null | undefined): string[] {
  return (players || []).map((name) => String(name || "").trim()).filter(Boolean);
}
