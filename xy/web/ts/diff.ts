// diff.ts — a tiny word-level diff used to highlight what changed between two
// versions of a card description (timeline desc_edit events). Token-level LCS
// over word/whitespace runs, so insertions and deletions are shown inline
// instead of as two opaque before/after blocks.
//
// Pure, dependency-free; unit-tested in jstest/diff.test.js.

export interface DiffOp {
  type: "eq" | "add" | "del";
  text: string;
}

// briefOps output: diff ops plus the "gap" markers that stand in for elided text.
export interface BriefOp {
  type: "eq" | "add" | "del" | "gap";
  text: string;
}

// tokenize splits text into alternating word and whitespace runs, preserving
// everything so the original is exactly reconstructable by concatenation.
function tokenize(s: string | null | undefined): string[] {
  return (s || "").match(/\s+|[^\s]+/g) || [];
}

// lcs builds the longest-common-subsequence length table for token arrays a, b.
function lcsTable(a: string[], b: string[]): Uint32Array[] {
  const n = a.length, m = b.length;
  const dp = Array.from({ length: n + 1 }, () => new Uint32Array(m + 1));
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      dp[i][j] = a[i] === b[j] ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1]);
    }
  }
  return dp;
}

// diffTokens returns a list of {type: "eq"|"add"|"del", text} ops that turn
// `before` into `after`. Adjacent ops of the same type are coalesced.
function diffTokens(before: string | null | undefined, after: string | null | undefined): DiffOp[] {
  const a = tokenize(before), b = tokenize(after);
  const dp = lcsTable(a, b);
  const ops: DiffOp[] = [];
  const push = (type: DiffOp["type"], text: string): void => {
    const last = ops[ops.length - 1];
    if (last && last.type === type) last.text += text;
    else ops.push({ type, text });
  };
  let i = 0, j = 0;
  while (i < a.length && j < b.length) {
    if (a[i] === b[j]) { push("eq", a[i]); i++; j++; }
    else if (dp[i + 1][j] >= dp[i][j + 1]) { push("del", a[i]); i++; }
    else { push("add", b[j]); j++; }
  }
  while (i < a.length) { push("del", a[i]); i++; }
  while (j < b.length) { push("add", b[j]); j++; }
  return ops;
}

// takeWords returns the leading (or, with fromEnd, trailing) `n` word tokens of
// a token run, whitespace included, so the slice reads as natural text.
function takeWords(toks: string[], n: number, fromEnd: boolean): string {
  if (n <= 0) return "";
  let count = 0;
  if (fromEnd) {
    for (let i = toks.length - 1; i >= 0; i--) {
      if (/\S/.test(toks[i]) && ++count === n) return toks.slice(i).join("");
    }
  } else {
    for (let i = 0; i < toks.length; i++) {
      if (/\S/.test(toks[i]) && ++count === n) return toks.slice(0, i + 1).join("");
    }
  }
  return toks.join(""); // fewer than n words — the whole run IS the context
}

// clusterChanges merges a run of changes separated only by whitespace into one
// deleted chunk followed by one inserted chunk.
//
// A rewritten phrase rarely replaces every word: the LCS latches onto whatever
// short tokens survive ("не", a preposition) and the result alternates
// del/add/del/add, which in the INLINE view reads as an unparseable barcode.
// The two-pane view does not need this — each side has its own pane — so this
// runs for the краткий view only, where old and new share one line.
//
// Pieces are rejoined with single spaces: the whitespace between them belonged
// to the ops being absorbed, and a summary line does not owe the original its
// line breaks.
function clusterChanges(ops: DiffOp[]): DiffOp[] {
  const isWs = (op: DiffOp): boolean => op.type === "eq" && !/\S/.test(op.text);
  const join = (parts: string[]): string => parts.map((t) => t.trim()).filter(Boolean).join(" ");
  const out: DiffOp[] = [];
  let i = 0;
  while (i < ops.length) {
    if (ops[i].type === "eq") { out.push(ops[i]); i++; continue; }
    const dels: string[] = [], adds: string[] = [];
    let j = i, lastChange = i;
    while (j < ops.length && (ops[j].type !== "eq" || isWs(ops[j]))) {
      if (ops[j].type === "del") { dels.push(ops[j].text); lastChange = j; }
      else if (ops[j].type === "add") { adds.push(ops[j].text); lastChange = j; }
      j++;
    }
    const del = join(dels), add = join(adds);
    if (del) out.push({ type: "del", text: del });
    if (add) out.push({ type: "add", text: add });
    // whitespace trailing the last real change separates the cluster from what
    // follows, so it belongs outside it
    for (const op of ops.slice(lastChange + 1, j)) out.push(op);
    i = j;
  }
  return out;
}

// briefOps elides the unchanged bulk of a diff, keeping `context` words either
// side of every change and replacing what it drops with a "gap" op. A question
// is mostly untouched text between small edits; showing all of it to reveal two
// swapped words is what the краткий view exists to avoid.
//
// The context around a change belongs to the change: for an equal run the words
// nearest the PREVIOUS change (its head) and the NEXT one (its tail) are kept,
// and only the middle is dropped. The leading run has nothing before it and the
// trailing run nothing after, so each keeps only its inner side.
function briefOps(rawOps: DiffOp[], context = 4): BriefOp[] {
  const ops = clusterChanges(rawOps);
  const out: BriefOp[] = [];
  // A gap is emitted even as the very first op — the leading run of a long
  // question is exactly what gets dropped, and starting mid-sentence with no …
  // reads as if that were the whole text.
  const pushGap = (): void => {
    const last = out[out.length - 1];
    if (!last || last.type !== "gap") out.push({ type: "gap", text: "…" });
  };
  ops.forEach((op, idx) => {
    if (op.type !== "eq") { out.push(op); return; }
    const toks = tokenize(op.text);
    const words = toks.filter((t) => /\S/.test(t)).length;
    const keepHead = idx === 0 ? 0 : context;
    const keepTail = idx === ops.length - 1 ? 0 : context;
    if (words <= keepHead + keepTail) { out.push(op); return; } // nothing worth dropping
    const head = takeWords(toks, keepHead, false);
    const tail = takeWords(toks, keepTail, true);
    if (head) out.push({ type: "eq", text: head });
    pushGap();
    if (tail) out.push({ type: "eq", text: tail });
  });
  return out;
}

export const xyDiff = { tokenize, diffTokens, clusterChanges, briefOps };
