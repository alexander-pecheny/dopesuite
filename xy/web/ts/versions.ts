// versions.ts — the Version algebra (ADR-0007). A Version is a WHOLE 4s body —
// question, ответ, зачёт, раздатка, автор, all of it. A card's description holds
// its versions concatenated, each introduced by a standalone
// (hidden-comment xy-version: имя) line; the name is optional and the line is
// xy's own metadata, dropped by parseBlocks (chgk.ts owns that rule:
// versionLineName), so every reader but the card editor sees version 1 and
// never knows the rest are there. A card with one version carries no such line
// at all — a plain question is stored exactly as it always was.
//
// The export merges the versions back into ONE question block (composeVersions),
// so a versioned card is still one numbered question: the `?` field carries every
// wording page-broken, and any other field the versions disagree on prints each
// value labelled by its version's NUMBER (never its name — a name is shorthand
// between editors, and «полегче» above a question tells a tester how hard it is
// meant to be before they have tried it).

import { composeFields, extractInlineHandout, parseBlocks, scanDirectives, splitFields, versionLineName } from "./chgk.js";
import type { HiddenSpan } from "./chgk.js";

const PAGEBREAK = "(PAGEBREAK)";
const VERSION_TAG = "xy-version:";


// A name is typed into a prompt, and a stray bracket there would close the
// directive early and spill into the card — so it can hold neither brackets nor
// line breaks.
function cleanName(name: string | null | undefined): string | null {
  return (name || "").replace(/[()]/g, "").replace(/\s+/g, " ").trim() || null;
}

function composeVersionLine(name: string | null): string {
  const clean = cleanName(name);
  return clean ? `(hidden-comment ${VERSION_TAG} ${clean})` : `(hidden-comment ${VERSION_TAG})`;
}

interface Versions { bodies: string[]; names: Array<string | null> }

// readVersions is the one parse: bodies and names, always at least one version.
// Text sitting before the first separator is a version too — a hand-edited card
// must not lose it.
function readVersions(desc: string | null | undefined): Versions {
  const bodies: string[] = [], names: Array<string | null> = [];
  let cur: string[] = [], curName: string | null = null, seen = false;
  const flush = (): void => {
    const body = cur.join("\n").trim();
    if (body !== "" || seen) { bodies.push(body); names.push(curName); }
    cur = [];
  };
  for (const line of (desc || "").split(/\r?\n/)) {
    const name = versionLineName(line);
    if (name === null) { cur.push(line); continue; }
    if (cur.join("\n").trim() !== "" || seen) flush();
    else cur = [];
    curName = name === "" ? null : name;
    seen = true;
  }
  flush();
  if (!bodies.length) { bodies.push(""); names.push(null); }
  return { bodies, names };
}

// joinVersions is the canonical spelling. One unnamed version is written bare, so
// a card that never grew a second wording is byte-identical to what it was.
function joinVersions(v: Versions): string {
  if (v.bodies.length === 1 && !v.names[0]) return v.bodies[0].trim();
  return v.bodies.map((b, i) => composeVersionLine(v.names[i]) + "\n" + b.trim()).join("\n");
}

function splitVersions(desc: string | null | undefined): string[] {
  return readVersions(desc).bodies;
}

function versionCount(desc: string | null | undefined): number {
  return readVersions(desc).bodies.length;
}

function versionBody(desc: string | null | undefined, i: number): string {
  return readVersions(desc).bodies[i] ?? "";
}

function versionName(desc: string | null | undefined, i: number): string | null {
  return readVersions(desc).names[i] ?? null;
}

// A body may not carry a separator of its own: it is ONE version, and a
// separator inside it would split into more the next time the card is read —
// which is how a card grew a version every time it was saved. The editor never
// shows a separator, so one can only arrive by paste or from text written under
// the old scheme, and dropping it is what those meant anyway.
function stripVersionLines(body: string): string {
  return body.split(/\r?\n/).filter((l) => versionLineName(l) === null).join("\n");
}

function setVersionBody(desc: string | null | undefined, i: number, body: string): string {
  const v = readVersions(desc);
  if (i < 0 || i >= v.bodies.length) return desc || "";
  v.bodies[i] = stripVersionLines(body);
  return joinVersions(v);
}

// setVersionName renames one version, or clears the name when given an empty
// string.
function setVersionName(desc: string | null | undefined, i: number, name: string): string {
  const v = readVersions(desc);
  if (i < 0 || i >= v.bodies.length) return desc || "";
  v.names[i] = cleanName(name);
  return joinVersions(v);
}

// addVersion clones version `i` whole and inserts the copy after it. Cloning
// rather than starting blank is what «Добавить версию» is for: a version is a
// rewording of what is already there. The copy is unnamed — two tabs reading
// «полегче» tell the editor nothing.
function addVersion(desc: string | null | undefined, i: number): { desc: string; index: number } {
  const v = readVersions(desc);
  const at = Math.min(Math.max(i, 0), v.bodies.length - 1);
  v.bodies.splice(at + 1, 0, v.bodies[at]);
  v.names.splice(at + 1, 0, null);
  return { desc: joinVersions(v), index: at + 1 };
}

// removeVersion drops one version. The last one cannot go — a card with no
// version is not a card — so it is returned unchanged.
function removeVersion(desc: string | null | undefined, i: number): { desc: string; index: number } {
  const v = readVersions(desc);
  if (v.bodies.length < 2 || i < 0 || i >= v.bodies.length) return { desc: desc || "", index: i };
  v.bodies.splice(i, 1);
  v.names.splice(i, 1);
  return { desc: joinVersions(v), index: Math.max(0, i - 1) };
}

// promoteVersion moves one version to the front, name and all. Order is what the
// board previews and what the export numbers, so «the good one goes first» is a
// real edit, not a display preference.
function promoteVersion(desc: string | null | undefined, i: number): { desc: string; index: number } {
  const v = readVersions(desc);
  if (i <= 0 || i >= v.bodies.length) return { desc: desc || "", index: i };
  v.bodies.unshift(v.bodies.splice(i, 1)[0]);
  v.names.unshift(v.names.splice(i, 1)[0]);
  return { desc: joinVersions(v), index: 0 };
}

// ── the export: many bodies, one question ───────────────────────────────────

// rawQuestion is the question block as written, handout bracket included —
// splitFields lifts the bracket out into its own field, and for the merge each
// version has to keep its own.
function rawQuestion(desc: string | null | undefined, keepVersionLines = false): string {
  const b = parseBlocks(desc, keepVersionLines).find((x) => x.type === "question" || x.type === "pre");
  return b ? b.text : "";
}

const versionLabel = (i: number): string => `версия ${i + 1}: `;

// mergeField prints one value when every version agrees and one labelled value
// per version when they do not. A field one version simply lacks counts as
// disagreement — inheriting it silently would put words in that version's mouth.
function mergeField(values: Array<string | null>): string | null {
  if (values.every((v) => v === values[0])) return values[0];
  const out: string[] = [];
  values.forEach((v, i) => { if (v !== null) out.push(versionLabel(i) + v); });
  return out.length ? out.join("\n") : null;
}

const listKey = (v: ReadonlyArray<string> | null): string => JSON.stringify(v);

function mergeSources(values: Array<string[] | null>): string[] | null {
  if (values.every((v) => listKey(v) === listKey(values[0]))) return values[0];
  const out: string[] = [];
  values.forEach((v, i) => { if (v) out.push(versionLabel(i) + v.filter((s) => s !== "").join("; ")); });
  return out.length ? out : null;
}

// Authors merge into ONE "@" block: a second marker would be a second author
// line, and chgksuite reads that as a different question's author.
function mergeAuthors(values: Array<string[] | null>): string[] | null {
  if (values.every((v) => listKey(v) === listKey(values[0]))) return values[0];
  const out: string[] = [];
  values.forEach((v, i) => { if (v) out.push(versionLabel(i) + v.join(", ")); });
  return out.length ? [out.join("\n")] : null;
}

// composeVersions is what every export renders: the versions folded back into a
// single question block. Structural leftovers (a "№" directive, anything the
// field editor does not model) come from version 1 — they belong to the question,
// not to a wording of it.
function composeVersions(desc: string | null | undefined): string {
  const bodies = splitVersions(desc);
  // The BODY, not the description: a lone version keeps its separator line when
  // it was named (delete one of a named pair and the survivor still carries its
  // name), and a name reaches no export.
  if (bodies.length < 2) return bodies[0] ?? "";
  const fs = bodies.map((b) => splitFields(b));
  const f0 = fs[0];
  return composeFields({
    preMarkup: f0.preMarkup,
    handout: null, // each version's own bracket rides inside its question text
    question: bodies
      .map((b, i) => `Версия ${i + 1}: ${rawQuestion(b).trim()}`)
      .join("\n" + PAGEBREAK + "\n"),
    answer: mergeField(fs.map((f) => f.answer)),
    zachet: mergeField(fs.map((f) => f.zachet)),
    nezachet: mergeField(fs.map((f) => f.nezachet)),
    comment: mergeField(fs.map((f) => f.comment)),
    sources: mergeSources(fs.map((f) => f.sources)),
    authors: mergeAuthors(fs.map((f) => f.authors)),
    authorLabel: f0.authorLabel,
    extra: f0.extra,
  });
}

// ── the cards written under the old scheme (ADR-0005) ───────────────────────
// A version used to be a run of question text between (PAGEBREAK) directives,
// with everything else shared. convertLegacyVersions turns one into whole bodies
// that clone the shared fields, and returns null when there is nothing to do.

function legacyNameSpans(segment: string): HiddenSpan[] {
  return scanDirectives(segment).hidden.filter((h) => h.body.startsWith(VERSION_TAG));
}

function stripLegacyName(segment: string): string {
  let out = segment;
  for (const h of legacyNameSpans(segment).reverse()) out = out.slice(0, h.start) + out.slice(h.end);
  return out;
}

function convertLegacyVersions(desc: string | null | undefined): string | null {
  // A card in the new form always OPENS with a separator, so that first line is
  // what tells the two apart. Anything else that looks like one is the old
  // scheme's name, written inside the question text — and a (PAGEBREAK) in a card
  // that opens with a separator is a genuine page break, which is the whole point
  // of not overloading the directive any more.
  const first = (desc || "").split(/\r?\n/).find((l) => l.trim() !== "");
  if (first !== undefined && versionLineName(first) !== null) return null;
  const q = rawQuestion(desc, true);
  if (!q.includes(PAGEBREAK)) return null;
  const f = splitFields(desc);
  // The раздатка was shared, and it physically sat in the first version's text —
  // so it is lifted out here and composed back into EVERY body. Leave it in the
  // text and versions 2+ lose the picture their question asks about.
  const inline = extractInlineHandout(q);
  const v: Versions = { bodies: [], names: [] };
  for (const part of (inline ? inline.rest : q).split(PAGEBREAK)) {
    const named = legacyNameSpans(part)[0];
    v.names.push(named ? cleanName(named.body.slice(VERSION_TAG.length)) : null);
    v.bodies.push(composeFields({
      ...f,
      handout: inline ? inline.handout : f.handout,
      question: stripLegacyName(part).trim(),
    }));
  }
  return joinVersions(v);
}


export const xyVersions = {
  splitVersions, versionCount, versionBody, versionName, setVersionBody, setVersionName,
  addVersion, removeVersion, promoteVersion, composeVersions, convertLegacyVersions,
};
