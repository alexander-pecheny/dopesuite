// diff.js — a tiny word-level diff used to highlight what changed between two
// versions of a card description (timeline desc_edit events). Token-level LCS
// over word/whitespace runs, so insertions and deletions are shown inline
// instead of as two opaque before/after blocks.
//
// ES module + window.xyDiff global. Pure, dependency-free; unit-tested in
// jstest/diff.test.js.

// tokenize splits text into alternating word and whitespace runs, preserving
// everything so the original is exactly reconstructable by concatenation.
function tokenize(s) {
  return (s || "").match(/\s+|[^\s]+/g) || [];
}

// lcs builds the longest-common-subsequence length table for token arrays a, b.
function lcsTable(a, b) {
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
function diffTokens(before, after) {
  const a = tokenize(before), b = tokenize(after);
  const dp = lcsTable(a, b);
  const ops = [];
  const push = (type, text) => {
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
function takeWords(toks, n, fromEnd) {
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

// briefOps elides the unchanged bulk of a diff, keeping `context` words either
// side of every change and replacing what it drops with a "gap" op. A question
// is mostly untouched text between small edits; showing all of it to reveal two
// swapped words is what the краткий view exists to avoid.
//
// The context around a change belongs to the change: for an equal run the words
// nearest the PREVIOUS change (its head) and the NEXT one (its tail) are kept,
// and only the middle is dropped. The leading run has nothing before it and the
// trailing run nothing after, so each keeps only its inner side.
function briefOps(ops, context = 4) {
  const out = [];
  // A gap is emitted even as the very first op — the leading run of a long
  // question is exactly what gets dropped, and starting mid-sentence with no …
  // reads as if that were the whole text.
  const pushGap = () => {
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

export const xyDiff = { tokenize, diffTokens, briefOps };
if (typeof window !== "undefined") window.xyDiff = xyDiff;
