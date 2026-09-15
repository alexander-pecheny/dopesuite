// The Transaction editor's model, with no DOM in it.
//
// The app stores amounts and nothing else: an even split and a percentage split
// are ways of typing them (spliff/CONTEXT.md), and this is where the typing
// becomes the amounts. It is the browser's copy of spliff/spliff/domain/split,
// and it must agree with it minor unit for minor unit — the ordering rule
// (payers by descending Payment, then split order) is what makes two phones
// showing one bill show the same numbers.

export type Mode = "simple" | "even" | "percent" | "exact" | "claim" | "settlement";

export interface Entry {
  member_id: number;
  minor: number;
}

export interface Draft {
  payments: Entry[];
  shares: Entry[];
}

export interface FormState {
  mode: Mode;
  totalMinor: number;
  /** Every Member of the Group, in join order. */
  members: number[];
  /** What each Member put in, by member id. Absent means nothing. */
  paid: Map<number, number>;
  /** Who the split is across, in split order. */
  chosen: number[];
  /** The percentage typed against each chosen Member, as a string. */
  percents: Map<number, string>;
  /** The exact amount typed against each chosen Member. */
  exact: Map<number, number>;
  /** Who is filling the form in — the one whose Share "claim your part" sets. */
  me: number;
  /** The Member a settlement hands the money to. */
  payee: number;
}

/**
 * payerOrder is the order spare minor units are handed out in: the payers by
 * descending Payment, ties by the order they appear in members (join order),
 * then everybody else in split order.
 */
export function payerOrder(state: FormState): number[] {
  const payers = state.members.filter((id) => (state.paid.get(id) ?? 0) > 0);
  return payers.sort((a, b) => {
    const diff = (state.paid.get(b) ?? 0) - (state.paid.get(a) ?? 0);
    if (diff !== 0) return diff;
    return state.members.indexOf(a) - state.members.indexOf(b);
  });
}

/**
 * allocate splits total across members by largest remainder. Each Member's
 * weight is `nums[i] / den` — one denominator for the whole split, which is
 * what both callers actually have (n for an even split, a million for a
 * percentage one) and which keeps every comparison an integer one.
 *
 * The arithmetic runs in BigInt so that a fraction of a minor unit is exact and
 * not a float's idea of one. Leftovers go to the biggest remainders, ties by
 * the order in `priority` and then by split order.
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
  // it, less when a percentage split leaves part of it Unclaimed.
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

/** Even splits total equally across the chosen Members. */
export function allocateEven(total: number, chosen: number[], priority: number[]): number[] {
  const n = BigInt(chosen.length || 1);
  return allocate(total, chosen, chosen.map(() => BigInt(1)), n, priority);
}

/** The denominator a percentage is taken over: four decimal places of a
 * percent, which is finer than any form offers and keeps the weight exact. */
const PERCENT_DEN = BigInt(1000000);

/**
 * allocateByPercent splits total by percentage. Percentages that do not reach
 * 100 leave the rest Unclaimed, which is a normal state; summing past 100 is a
 * refusal, because a Share may never exceed the total.
 */
export function allocateByPercent(
  total: number,
  chosen: number[],
  percents: Map<number, string>,
  priority: number[],
): number[] | null {
  const nums: bigint[] = [];
  let sum = 0;
  for (const id of chosen) {
    const raw = (percents.get(id) ?? "").trim().replace(",", ".");
    const value = raw === "" ? 0 : Number(raw);
    if (!Number.isFinite(value) || value < 0) return null;
    sum += value;
    nums.push(BigInt(Math.round(value * 10000)));
  }
  if (sum > 100.0000001) return null;
  return allocate(total, chosen, nums, PERCENT_DEN, priority);
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
  | "bad_percent"
  | "no_members"
  | "no_payee";

export interface DraftResult {
  draft?: Draft;
  error?: DraftError;
}

/**
 * buildDraft turns the form's state into the one model the server stores: a
 * list of Payments and a list of Shares. Every mode ends here — the modes are
 * the form's, the model is the app's.
 */
export function buildDraft(state: FormState): DraftResult {
  const payments: Entry[] = [];
  for (const id of state.members) {
    const minor = state.paid.get(id) ?? 0;
    if (minor > 0) payments.push({ member_id: id, minor });
  }
  if (payments.length === 0) return { error: "no_payer" };
  if (totalOf(payments) !== state.totalMinor) return { error: "payments_mismatch" };

  const priority = payerOrder(state);
  let shares: Entry[] = [];

  switch (state.mode) {
    case "even": {
      if (state.chosen.length === 0) return { error: "no_members" };
      const amounts = allocateEven(state.totalMinor, state.chosen, priority);
      shares = zip(state.chosen, amounts);
      break;
    }
    case "percent": {
      if (state.chosen.length === 0) return { error: "no_members" };
      const amounts = allocateByPercent(state.totalMinor, state.chosen, state.percents, priority);
      if (!amounts) return { error: "bad_percent" };
      shares = zip(state.chosen, amounts);
      break;
    }
    case "exact": {
      for (const id of state.chosen) {
        const minor = state.exact.get(id) ?? 0;
        if (minor < 0) return { error: "bad_percent" };
        if (minor > 0) shares.push({ member_id: id, minor });
      }
      break;
    }
    case "claim": {
      // "I paid, claim your part": the payer states the total and their own
      // Share, and the rest stays Unclaimed until somebody takes it.
      const mine = state.exact.get(state.me) ?? 0;
      if (mine > 0) shares.push({ member_id: state.me, minor: mine });
      break;
    }
    case "simple":
    case "settlement": {
      // "A paid X for B" and a Settlement are the same shape: one side answers
      // for the whole of it. The two modes differ in what they MEAN, which is
      // the form's business and not the model's.
      if (!state.payee) return { error: "no_payee" };
      shares = [{ member_id: state.payee, minor: state.totalMinor }];
      break;
    }
  }

  if (totalOf(shares) > state.totalMinor) return { error: "shares_overdraw" };
  return { draft: { payments, shares } };
}

function zip(members: number[], amounts: number[]): Entry[] {
  const out: Entry[] = [];
  members.forEach((id, i) => {
    if (amounts[i] > 0) out.push({ member_id: id, minor: amounts[i] });
  });
  return out;
}
