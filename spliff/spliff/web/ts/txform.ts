// The Transaction editor's model, with no DOM in it.
//
// A bill answers two questions — who handed the money over, and who it was for
// — and the editor asks each of them as a list of rows: a person, an amount.
// That is also exactly what is stored (spliff/CONTEXT.md), so this file is
// mostly the arithmetic the rows are checked and filled with; an even split is
// a convenience that writes amounts into them, not a second kind of bill.
//
// The allocation is the browser's copy of spliff/spliff/domain/split, and it
// must agree with it minor unit for minor unit — the ordering rule (payers by
// descending Payment, then row order) is what makes two phones showing one bill
// show the same numbers.

export interface Entry {
  member_id: number;
  minor: number;
}

export interface Draft {
  payments: Entry[];
  shares: Entry[];
}

/**
 * One line of either table. `member` is 0 while nobody is picked, and `minor`
 * is null while the amount cell is empty — a row somebody has not finished
 * yet, which is a different thing from a row that says nothing was paid.
 */
export interface Row {
  member: number;
  minor: number | null;
}

export interface FormState {
  totalMinor: number;
  /** Every Member of the Group, in join order — the tie-break for the split. */
  members: number[];
  /** The rows under "Who paid", in the order they are on the page. */
  payments: Row[];
  /** The rows under "For whom", in the order they are on the page. */
  shares: Row[];
}

/** The rows that actually say something: somebody, and more than nothing. */
function filled(rows: Row[]): Row[] {
  return rows.filter((row) => row.member > 0 && (row.minor ?? 0) > 0);
}

/**
 * paidLeft is what the total still has no payer for — negative when the
 * payments overshoot it. The line under "Who paid" is this number in words.
 */
export function paidLeft(state: FormState): number {
  let sum = 0;
  for (const row of filled(state.payments)) sum += row.minor ?? 0;
  return state.totalMinor - sum;
}

/**
 * payerOrder is the order spare minor units are handed out in: the payers by
 * descending Payment, ties by the order they appear in members (join order).
 */
export function payerOrder(state: FormState): number[] {
  const paid = new Map<number, number>();
  for (const row of filled(state.payments)) {
    paid.set(row.member, (paid.get(row.member) ?? 0) + (row.minor ?? 0));
  }
  return [...paid.keys()].sort((a, b) => {
    const diff = (paid.get(b) ?? 0) - (paid.get(a) ?? 0);
    if (diff !== 0) return diff;
    return state.members.indexOf(a) - state.members.indexOf(b);
  });
}

/**
 * allocate splits total across members by largest remainder. Each Member's
 * weight is `nums[i] / den` — one denominator for the whole split, which is
 * what the caller actually has (n for an even split) and which keeps every
 * comparison an integer one.
 *
 * The arithmetic runs in BigInt so that a fraction of a minor unit is exact and
 * not a float's idea of one. Leftovers go to the biggest remainders, ties by
 * the order in `priority` and then by row order.
 */
export function allocate(
  total: number,
  members: number[],
  nums: bigint[],
  den: bigint,
  priority: number[],
): number[] {
  const n = members.length;
  const out = new Array<number>(n).fill(0);
  if (n === 0 || den === ZERO) return out;

  const big = BigInt(total);
  const remainders: bigint[] = [];
  let allocated = ZERO;
  let sum = ZERO;

  for (let i = 0; i < n; i++) {
    const exact = big * nums[i];
    const whole = exact / den;
    out[i] = Number(whole);
    remainders.push(exact - whole * den);
    allocated += whole;
    sum += nums[i];
  }

  // What the shares SHOULD add up to: the whole total when the weights cover
  // it, which an even split always does.
  const target = roundNearest(big * sum, den);
  let leftover = Number(target - allocated);

  const rank = rankOf(members, priority);
  const order = members.map((_, i) => i).sort((a, b) => {
    if (remainders[b] !== remainders[a]) return remainders[b] > remainders[a] ? 1 : -1;
    return rank[a] - rank[b];
  });
  for (let k = 0; k < order.length && leftover > 0; k++, leftover--) {
    out[order[k]]++;
  }
  return out;
}

const ZERO = BigInt(0);
const TWO = BigInt(2);

function roundNearest(num: bigint, den: bigint): bigint {
  return (TWO * num + den) / (TWO * den);
}

function rankOf(members: number[], priority: number[]): number[] {
  const place = new Map<number, number>();
  priority.forEach((id, i) => {
    if (!place.has(id)) place.set(id, i);
  });
  return members.map((id, i) => place.get(id) ?? priority.length + i);
}

/** Even splits total equally across the given Members. */
export function allocateEven(total: number, members: number[], priority: number[]): number[] {
  const n = BigInt(members.length || 1);
  return allocate(total, members, members.map(() => BigInt(1)), n, priority);
}

/**
 * evenShares is what "Split evenly" writes into the "For whom" table: the total
 * shared out across the rows that name somebody, aligned with `state.shares` so
 * the page can drop each amount into the cell it came from. A row with nobody
 * picked gets nothing — it is not a person yet.
 *
 * Two rows naming the same Member are two rows: each gets its own equal part,
 * which adds up to the same thing the one row would have had. The editor
 * refuses to SAVE the duplicate; it does not have to refuse to divide.
 */
export function evenShares(state: FormState): number[] {
  const at: number[] = [];
  const members: number[] = [];
  state.shares.forEach((row, i) => {
    if (row.member <= 0) return;
    at.push(i);
    members.push(row.member);
  });
  const amounts = allocateEven(state.totalMinor, members, payerOrder(state));
  const out = new Array<number>(state.shares.length).fill(0);
  at.forEach((index, k) => {
    out[index] = amounts[k];
  });
  return out;
}

export function totalOf(entries: Entry[]): number {
  return entries.reduce((sum, e) => sum + e.minor, 0);
}

/** unclaimed is the part of the total no Share accounts for. */
export function unclaimed(total: number, shares: Entry[]): number {
  return Math.max(0, total - totalOf(shares));
}

export type DraftError =
  | "no_payer"
  | "payments_mismatch"
  | "shares_overdraw"
  | "negative_amount"
  | "no_person"
  | "duplicate_member";

export interface DraftResult {
  draft?: Draft;
  error?: DraftError;
}

/**
 * buildDraft turns the two tables into the one model the server stores: a list
 * of Payments and a list of Shares. A row that says nothing — nobody picked and
 * no amount typed — is not an error: it is the empty row the table always ends
 * with, and it simply does not become an Entry.
 */
export function buildDraft(state: FormState): DraftResult {
  const payments = entriesOf(state.payments);
  if (payments.error) return { error: payments.error };
  if (payments.entries.length === 0) return { error: "no_payer" };
  if (totalOf(payments.entries) !== state.totalMinor) return { error: "payments_mismatch" };

  const shares = entriesOf(state.shares);
  if (shares.error) return { error: shares.error };
  if (totalOf(shares.entries) > state.totalMinor) return { error: "shares_overdraw" };

  return { draft: { payments: payments.entries, shares: shares.entries } };
}

function entriesOf(rows: Row[]): { entries: Entry[]; error?: DraftError } {
  const entries: Entry[] = [];
  const seen = new Set<number>();
  for (const row of rows) {
    const minor = row.minor;
    if (minor !== null && minor < 0) return { entries, error: "negative_amount" };
    // An amount against nobody is a row somebody meant to finish. Silence would
    // drop it on save, which is the one thing the editor must never do.
    if (row.member <= 0) {
      if (minor !== null && minor !== 0) return { entries, error: "no_person" };
      continue;
    }
    if (minor === null || minor === 0) continue;
    if (seen.has(row.member)) return { entries, error: "duplicate_member" };
    seen.add(row.member);
    entries.push({ member_id: row.member, minor });
  }
  return { entries };
}
