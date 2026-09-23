// The seating — who a team sent to play one theme, and how their names read
// in a cell five question columns wide. EK seats one player and prints him
// whole; Erudit-Sextet seats up to three, and three full names never fit, so
// each is cut to the shortest surname prefix that still tells him from every
// other player on that team's roster. The full names stay one click away, in
// the popover the cell already carries.
//
// Pure, so ek.ts and score-table.ts's live patch print the same string and
// jstest can hold the rule to worked examples.

// The ellipsis a cut surname ends in. Two dots, not the character: the cell's
// font renders them at the same weight as the name.
const CUT = "..";

// The shortest prefix a cut surname may be. Below three letters a Russian
// surname says nothing at all — two letters could be half the roster.
const MIN_PREFIX = 3;

// surnameOf is the family name inside a display name. The roster stores the
// given name and the surname separately and joins them in that order
// (store.JoinPlayerName), so the surname is the last word; a name of one word
// is all there is.
export function surnameOf(name: string): string {
  const words = String(name || "").trim().split(/\s+/).filter(Boolean);
  return words.length === 0 ? "" : words[words.length - 1];
}

// seatName is one seated player's name as the closed cell prints it: his
// surname cut to the shortest prefix (at least three letters) that no other
// player on the roster shares, and printed whole when no cut is shorter than
// the surname itself.
export function seatName(name: string, roster: ReadonlyArray<string>): string {
  const surname = surnameOf(name);
  if (!surname) return "";
  const others = roster
    .filter((member) => String(member || "").trim() !== String(name || "").trim())
    .map(surnameOf)
    .filter(Boolean);
  const lower = surname.toLowerCase();
  for (let length = MIN_PREFIX; length < surname.length; length++) {
    const prefix = lower.slice(0, length);
    if (!others.some((other) => other.toLowerCase().slice(0, length) === prefix)) {
      return surname.slice(0, length) + CUT;
    }
  }
  return surname;
}

// seatingLabel is the whole seating as one line — every seated player's cut
// surname, in the order the state lists them, which carries no meaning.
export function seatingLabel(seated: ReadonlyArray<string>, roster: ReadonlyArray<string>): string {
  return seated.map((name) => seatName(name, roster)).filter(Boolean).join(" ");
}

// seatedNames is a theme's seating with the blanks dropped — what a cell
// prints and what the statistics count as having played the theme.
export function seatedNames(players: ReadonlyArray<string | null | undefined> | null | undefined): string[] {
  return (players || []).map((name) => String(name || "").trim()).filter(Boolean);
}
