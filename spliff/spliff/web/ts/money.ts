// The browser's half of spliff/spliff/domain/money: the same exponent table and
// the same refusal to let a float near an amount. The page needs it because a
// derived split is computed here — the server stores amounts, never percentages
// — and two phones showing different numbers for one bill is the failure this
// whole arrangement exists to prevent.

// Every ISO 4217 code whose exponent is NOT 2. Everything else is two decimal
// places, so listing the exceptions keeps the table short and honest.
const EXPONENTS: Record<string, number> = {
  BIF: 0, CLP: 0, DJF: 0, GNF: 0, ISK: 0, JPY: 0, KMF: 0, KRW: 0, PYG: 0,
  RWF: 0, UGX: 0, UYI: 0, VND: 0, VUV: 0, XAF: 0, XOF: 0, XPF: 0,
  BHD: 3, IQD: 3, JOD: 3, KWD: 3, LYD: 3, OMR: 3, TND: 3,
};

export function normalise(currency: string): string {
  return currency.trim().toUpperCase();
}

export function exponentOf(currency: string): number {
  return EXPONENTS[normalise(currency)] ?? 2;
}

export function scaleOf(currency: string): number {
  return 10 ** exponentOf(currency);
}

// formatMinor writes a count of minor units in the currency's own precision,
// with a plain "." separator. The currency code is never part of it: each place
// that shows an amount decides for itself whether the code is said elsewhere.
export function formatMinor(minor: number, currency: string): string {
  const exp = exponentOf(currency);
  const sign = minor < 0 ? "-" : "";
  const abs = Math.abs(minor);
  if (exp === 0) return sign + String(abs);
  const scale = 10 ** exp;
  const whole = Math.floor(abs / scale);
  const frac = abs % scale;
  return `${sign}${whole}.${String(frac).padStart(exp, "0")}`;
}

// parseAmount reads what a person typed. It answers null rather than a wrong
// number: more decimals than the currency has is a refusal, not a rounding —
// there is no such thing as a tenth of a yen.
export function parseAmount(text: string, currency: string): number | null {
  let raw = text.trim().replace(/[\s ]/g, "").replace(",", ".");
  if (raw === "") return null;
  let negative = false;
  if (raw.startsWith("-")) {
    negative = true;
    raw = raw.slice(1);
  } else if (raw.startsWith("+")) {
    raw = raw.slice(1);
  }
  const exp = exponentOf(currency);
  const match = /^(\d*)(?:\.(\d*))?$/.exec(raw);
  if (!match) return null;
  const whole = match[1] ?? "";
  const frac = match[2] ?? "";
  if (whole === "" && frac === "") return null;
  if (frac.length > exp) return null;
  const minor = Number((whole || "0") + frac.padEnd(exp, "0"));
  if (!Number.isSafeInteger(minor)) return null;
  return negative ? -minor : minor;
}
