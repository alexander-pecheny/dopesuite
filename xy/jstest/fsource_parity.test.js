// The Go/TS 4s parity corpus (chgksuite/fsource/testdata/parity.json, Go
// the oracle): the marker split of every fixture line, the (img …) references,
// and the .hndt document the browser writes and the server parses.
import { test } from "node:test";
import assert from "node:assert/strict";
import { chgk4s } from "../web/assets/static/dist/chgk/chgk.js";
import { xyHndt } from "../web/assets/static/dist/hndt.js";
import { xyListExport } from "../web/assets/static/dist/listexport.js";
import { MARKERS } from "../web/assets/static/dist/chgk/markers_gen.js";
import corpus from "../../chgksuite/fsource/testdata/parity.json" with { type: "json" };

test("splitMarker agrees with fsource.SplitMarker on every fixture line", () => {
  assert.ok(corpus.lines.length > 50, "the corpus is seeded from the fixtures");
  for (const { line, prefix, rest } of corpus.lines) {
    assert.deepEqual(chgk4s.splitMarker(line), { prefix, rest }, JSON.stringify(line));
  }
});

test("every marker the generated table names is a marked line, and only those", () => {
  const types = new Set(MARKERS.map(([, t]) => t));
  assert.ok(types.has("battle") && types.has("setcounter") && types.has("handout"));
  for (const [marker, type] of MARKERS) {
    const [b] = chgk4s.parseBlocks(`${marker} текст`);
    assert.equal(b.type, type, marker);
    assert.equal(b.text, "текст");
  }
  assert.equal(chgk4s.parseBlocks("просто строка")[0].type, "pre");
});

test("imgRefs agrees with the inline tokenizer's img runs", () => {
  for (const { source, refs } of corpus.imgRefs) {
    assert.deepEqual(chgk4s.imgRefs(source), refs, JSON.stringify(source));
  }
});

test("the browser writes the corpus .hndt byte for byte, and reads its settings back", () => {
  const { cards, numbers, metas, text, blocks } = corpus.hndt;
  const out = xyListExport.hndtOf(cards.map((c) => ({ ...c, handoutMeta: metas[c.id] ?? null })));
  assert.deepEqual(out.numbers, numbers);
  assert.equal(out.source, text);
  assert.deepEqual(xyHndt.parseHndtMetaByQuestion(text), { "1": "columns: 3", "3": "columns: 2\nfont_size: 14", "4": "columns: 3" });
  assert.equal(blocks.length, 3, "and the server parsed it into one block per handout");
});

test("a backslashed marker line continues its field, one backslash lighter, as fsource.UnescapeLine reads it", () => {
  assert.ok(corpus.escapes.some((e) => e.escaped), "the corpus holds escaped lines");
  for (const { line, text } of corpus.escapes) {
    if (chgk4s.startsBlock(line)) continue;
    assert.equal(chgk4s.parseBlocks(`? q\n${line}`)[0].text, `q\n${text}`.trimEnd(), line);
  }
});
