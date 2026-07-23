// rank.ts — fractional indexing (LexoRank-style) so a drag updates only the
// moved item's rank. A faithful port of the public-domain `fractional-indexing`
// algorithm (rocicorp/fractional-indexing, MIT). Keys are base-62 strings that
// sort lexicographically; `keyBetween(a, b)` returns a key strictly between a
// and b (null = open end).

const DIGITS = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz";
const ZERO = DIGITS[0];

function midpoint(a: string, b: string | null): string {
  if (b !== null && a >= b) throw new Error(`${a} >= ${b}`);
  if (a.slice(-1) === ZERO || (b && b.slice(-1) === ZERO)) throw new Error("trailing zero");
  if (b) {
    let n = 0;
    while ((a[n] || ZERO) === b[n]) n++;
    if (n > 0) return b.slice(0, n) + midpoint(a.slice(n), b.slice(n));
  }
  const digitA = a ? DIGITS.indexOf(a[0]) : 0;
  const digitB = b !== null ? DIGITS.indexOf(b[0]) : DIGITS.length;
  if (digitB - digitA > 1) {
    const midDigit = Math.round(0.5 * (digitA + digitB));
    return DIGITS[midDigit];
  }
  if (b && b.length > 1) return b.slice(0, 1);
  return DIGITS[digitA] + midpoint(a.slice(1), null);
}

function validateInteger(int: string): void {
  if (int.length !== getIntegerLength(int[0])) throw new Error(`invalid integer part: ${int}`);
}
function getIntegerLength(head: string): number {
  if (head >= "a" && head <= "z") return head.charCodeAt(0) - "a".charCodeAt(0) + 2;
  if (head >= "A" && head <= "Z") return "Z".charCodeAt(0) - head.charCodeAt(0) + 2;
  throw new Error("invalid order key head: " + head);
}
function getIntegerPart(key: string): string {
  const integerPartLength = getIntegerLength(key[0]);
  if (integerPartLength > key.length) throw new Error("invalid order key: " + key);
  return key.slice(0, integerPartLength);
}
function validateOrderKey(key: string): void {
  if (key === "A" + ZERO.repeat(26)) throw new Error("invalid order key: " + key);
  const i = getIntegerPart(key);
  const f = key.slice(i.length);
  if (f.slice(-1) === ZERO) throw new Error("invalid order key: " + key);
}

function incrementInteger(x: string): string | null {
  validateInteger(x);
  const [head, ...digs] = x.split("");
  let carry = true;
  for (let i = digs.length - 1; carry && i >= 0; i--) {
    const d = DIGITS.indexOf(digs[i]) + 1;
    if (d === DIGITS.length) digs[i] = ZERO;
    else { digs[i] = DIGITS[d]; carry = false; }
  }
  if (carry) {
    if (head === "Z") return "a" + ZERO;
    if (head === "z") return null;
    const h = String.fromCharCode(head.charCodeAt(0) + 1);
    if (h > "a") digs.push(ZERO);
    else digs.shift();
    return h + digs.join("");
  }
  return head + digs.join("");
}
function decrementInteger(x: string): string | null {
  validateInteger(x);
  const [head, ...digs] = x.split("");
  let borrow = true;
  for (let i = digs.length - 1; borrow && i >= 0; i--) {
    const d = DIGITS.indexOf(digs[i]) - 1;
    if (d === -1) digs[i] = DIGITS.slice(-1);
    else { digs[i] = DIGITS[d]; borrow = false; }
  }
  if (borrow) {
    if (head === "a") return "Z" + DIGITS.slice(-1);
    if (head === "A") return null;
    const h = String.fromCharCode(head.charCodeAt(0) - 1);
    if (h < "Z") digs.push(DIGITS.slice(-1));
    else digs.shift();
    return h + digs.join("");
  }
  return head + digs.join("");
}

// keyBetween returns a key strictly between a and b (null open ends).
function keyBetween(a: string | null, b: string | null): string {
  if (a !== null) validateOrderKey(a);
  if (b !== null) validateOrderKey(b);
  if (a !== null && b !== null && a >= b) throw new Error(`${a} >= ${b}`);
  if (a === null) {
    if (b === null) return "a" + ZERO;
    const ib = getIntegerPart(b);
    const fb = b.slice(ib.length);
    if (ib === "A" + ZERO.repeat(26)) return ib + midpoint("", fb);
    if (ib < b) return ib;
    const res = decrementInteger(ib);
    if (res === null) throw new Error("cannot decrement any more");
    return res;
  }
  if (b === null) {
    const ia = getIntegerPart(a);
    const fa = a.slice(ia.length);
    const i = incrementInteger(ia);
    return i === null ? ia + midpoint(fa, null) : i;
  }
  const ia = getIntegerPart(a);
  const fa = a.slice(ia.length);
  const ib = getIntegerPart(b);
  const fb = b.slice(ib.length);
  if (ia === ib) return ia + midpoint(fa, fb);
  const i = incrementInteger(ia);
  if (i === null) throw new Error("cannot increment any more");
  if (i < b) return i;
  return ia + midpoint(fa, null);
}

// nKeysBetween returns n evenly distributed keys strictly between a and b.
function nKeysBetween(a: string | null, b: string | null, n: number): string[] {
  if (n === 0) return [];
  if (n === 1) return [keyBetween(a, b)];
  if (b === null) {
    let c = keyBetween(a, b);
    const out = [c];
    for (let i = 0; i < n - 1; i++) { c = keyBetween(c, b); out.push(c); }
    return out;
  }
  if (a === null) {
    let c = keyBetween(a, b);
    const out = [c];
    for (let i = 0; i < n - 1; i++) { c = keyBetween(a, c); out.push(c); }
    out.reverse();
    return out;
  }
  const mid = Math.floor(n / 2);
  const c = keyBetween(a, b);
  return [...nKeysBetween(a, c, mid), c, ...nKeysBetween(c, b, n - mid - 1)];
}

export const xyRank = { keyBetween, nKeysBetween };
