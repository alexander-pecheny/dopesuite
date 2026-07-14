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

export const xyDiff = { tokenize, diffTokens };
if (typeof window !== "undefined") window.xyDiff = xyDiff;
