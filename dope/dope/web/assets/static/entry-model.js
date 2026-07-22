// entry-model.js — the pure kernel of od.js's entry grid (window.DopeEntryModel).
//
// The OD host types team placements into a grid; the fiddly, bug-prone bits are
// parsing a pasted spreadsheet block, coercing each pasted cell to a team
// number, and computing a column's "invert" (the teams NOT yet placed). Those
// are pure functions with no DOM or app state, pulled out here so they are
// unit-tested; od.js keeps the grid rendering, selection and persistence and
// calls into this.
(function () {
  "use strict";

  // parseClipboard splits pasted spreadsheet text into a grid of cell strings:
  // rows by newline (CRLF/CR normalized), columns by tab. A single trailing
  // blank line (Excel/Sheets append one) is dropped.
  function parseClipboard(text) {
    var normalized = String(text || "").replace(/\r\n/g, "\n").replace(/\r/g, "\n");
    var lines = normalized.split("\n");
    if (lines.length > 1 && lines[lines.length - 1] === "") lines.pop();
    return lines.map(function (line) { return line.split("\t"); });
  }

  // coerceValue maps one pasted cell to a team number: a bare integer passes
  // through; otherwise it is matched (case-insensitively, ru locale) against a
  // team label and resolved to that team's number; anything else is 0 (empty).
  // teams is [{label, number}].
  function coerceValue(raw, teams) {
    var value = String(raw || "").trim();
    if (value === "") return 0;
    if (/^\d+$/.test(value)) return Number(value);
    var lower = value.toLocaleLowerCase("ru");
    for (var i = 0; i < teams.length; i++) {
      if (String(teams[i].label).toLocaleLowerCase("ru") === lower) return teams[i].number;
    }
    return 0;
  }

  // invertColumn computes the invert of a question column: the team numbers NOT
  // currently entered, ascending, packed into a fresh array of teamCount slots.
  // Returns null when the result equals the current column (a no-op).
  function invertColumn(current, allNumbers, teamCount) {
    var cur = current || [];
    var present = new Set(cur.filter(function (v) { return Number.isInteger(v) && v > 0; }));
    var complement = allNumbers.filter(function (n) { return !present.has(n); }).sort(function (a, b) { return a - b; });
    var next = new Array(teamCount).fill(0);
    complement.forEach(function (n, i) { if (i < next.length) next[i] = n; });
    var same = cur.length === next.length;
    for (var i = 0; same && i < next.length; i++) {
      if ((cur[i] || 0) !== next[i]) same = false;
    }
    return same ? null : next;
  }

  window.DopeEntryModel = { parseClipboard: parseClipboard, coerceValue: coerceValue, invertColumn: invertColumn };
})();
