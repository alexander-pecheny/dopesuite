// What a History entry says, in words: the verb, and the lines that say what
// exactly was added, changed, deleted or restored. Both the Group page and the
// Transaction page read History, and both read it through here. There is no DOM
// in this file, so the bun tests can check every line it writes.

import S from "./i18nstrings.js";
import { formatMinor } from "./money.js";

export interface HistoryEntry {
  kind: string;
  before: string;
  after: string;
}

interface SnapshotEntry {
  member_id: number;
  minor: number;
}

interface Snapshot {
  description: string;
  day: string;
  currency: string;
  total_minor: number;
  payments?: SnapshotEntry[];
  shares?: SnapshotEntry[];
}

interface Named {
  id: number;
  name: string;
}

// memberNames maps every member row a page may meet to the name to show for
// it: the current Members as they are, and the Former Members with a marker
// that says they have left. An old bill or History entry can name either.
export function memberNames(members: Named[], former: Named[]): Map<number, string> {
  const out = new Map(members.map((m) => [m.id, m.name]));
  for (const m of former) out.set(m.id, S.page.common.formerMember(m.name));
  return out;
}

export function historyVerb(kind: string): string {
  switch (kind) {
    case "created":
      return S.page.history.created();
    case "edited":
      return S.page.history.edited();
    case "deleted":
      return S.page.history.deleted();
    case "restored":
      return S.page.history.restored();
    case "photo_added":
      return S.page.history.photoAdded();
    case "photo_removed":
      return S.page.history.photoRemoved();
    default:
      return kind;
  }
}

// describeHistory returns one line per thing that changed. An edit is the
// fields that differ, down to each person's payment and share. A Transaction
// that was added, deleted or restored is said in full: its total, who paid and
// who it was for, because that is what the others need to check. A Photo says
// nothing more than its verb. names maps a Member id to the name to show.
export function describeHistory(entry: HistoryEntry, names: Map<number, string>): string[] {
  const before = parse(entry.before);
  const after = parse(entry.after);
  switch (entry.kind) {
    case "created":
      return after ? summary(after, names) : [];
    case "deleted":
    case "restored":
      return before ? summary(before, names) : after ? summary(after, names) : [];
    case "edited":
      return before && after ? diff(before, after, names) : [];
    default:
      return [];
  }
}

function summary(s: Snapshot, names: Map<number, string>): string[] {
  const out = [S.page.history.total(money(s.total_minor, s.currency))];
  const paid = entries(s.payments, s.currency, names);
  if (paid) out.push(S.page.history.paidBy(paid));
  const shares = entries(s.shares, s.currency, names);
  if (shares) out.push(S.page.history.sharesOf(shares));
  return out;
}

function diff(before: Snapshot, after: Snapshot, names: Map<number, string>): string[] {
  const out: string[] = [];
  if (before.description !== after.description) {
    out.push(S.page.history.fieldDescription(before.description, after.description));
  }
  if (before.day !== after.day) out.push(S.page.history.fieldDay(before.day, after.day));
  if (before.total_minor !== after.total_minor || before.currency !== after.currency) {
    out.push(S.page.history.fieldTotal(
      money(before.total_minor, before.currency),
      money(after.total_minor, after.currency),
    ));
  }
  for (const change of changes(before, after, "payments")) {
    out.push(S.page.history.fieldPaid(nameOf(change.id, names), change.was, change.now));
  }
  for (const change of changes(before, after, "shares")) {
    out.push(S.page.history.fieldShare(nameOf(change.id, names), change.was, change.now));
  }
  // A Claim changes no field a person typed into the header. It changes what
  // is left for the others, which is the number they came to see.
  const was = unclaimed(before);
  const now = unclaimed(after);
  if (was !== now) {
    out.push(S.page.history.fieldUnclaimed(money(was, before.currency), money(now, after.currency)));
  }
  return out;
}

interface Change {
  id: number;
  was: string;
  now: string;
}

// changes compares one table of a Transaction person by person, in the order
// the people appear: first the rows that were there before, then the new ones.
// A row that appears or goes away is said as "nothing" on the missing side. A
// changed currency changes every amount, so every row is said again.
function changes(before: Snapshot, after: Snapshot, table: "payments" | "shares"): Change[] {
  const was = byMember(before[table]);
  const now = byMember(after[table]);
  const ids = [...was.keys(), ...[...now.keys()].filter((id) => !was.has(id))];
  const out: Change[] = [];
  for (const id of ids) {
    const a = was.get(id);
    const b = now.get(id);
    if (a === b && before.currency === after.currency) continue;
    out.push({
      id,
      was: a === undefined ? S.page.history.nothing() : money(a, before.currency),
      now: b === undefined ? S.page.history.nothing() : money(b, after.currency),
    });
  }
  return out;
}

function byMember(list: SnapshotEntry[] | undefined): Map<number, number> {
  const out = new Map<number, number>();
  for (const e of list ?? []) {
    if (!e || !Number.isFinite(e.minor)) continue;
    out.set(e.member_id, (out.get(e.member_id) ?? 0) + e.minor);
  }
  return out;
}

function entries(list: SnapshotEntry[] | undefined, currency: string, names: Map<number, string>): string {
  return (list ?? [])
    .filter((e) => e && Number.isFinite(e.minor))
    .map((e) => `${nameOf(e.member_id, names)} ${formatMinor(e.minor, currency)}`)
    .join(", ");
}

function unclaimed(s: Snapshot): number {
  const claimed = (s.shares ?? []).reduce(
    (sum, e) => sum + (Number.isFinite(e?.minor) ? e.minor : 0), 0);
  return Math.max(0, s.total_minor - claimed);
}

function money(minor: number, currency: string): string {
  return `${formatMinor(minor, currency)} ${currency}`;
}

function nameOf(id: number, names: Map<number, string>): string {
  return names.get(id) ?? S.page.history.somebody();
}

function parse(raw: string): Snapshot | null {
  if (!raw) return null;
  try {
    return JSON.parse(raw) as Snapshot;
  } catch {
    return null;
  }
}
