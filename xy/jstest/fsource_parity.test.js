// The Go/TS 4s parity corpus (internal/chgk/fsource/testdata/parity.json, Go
// the oracle): the marker split of every fixture line, the (img …) references,
// and the .hndt document the browser writes and the server parses.
import { test } from "node:test";
import assert from "node:assert/strict";
import { xyChgk } from "../web/assets/static/dist/chgk.js";
import { MARKERS } from "../web/assets/static/dist/markers_gen.js";
import corpus from "../internal/chgk/fsource/testdata/parity.json" with { type: "json" };

test("splitMarker agrees with fsource.SplitMarker on every fixture line", () => {
  assert.ok(corpus.lines.length > 50, "the corpus is seeded from the fixtures");
  for (const { line, prefix, rest } of corpus.lines) {
    assert.deepEqual(xyChgk.splitMarker(line), { prefix, rest }, JSON.stringify(line));
  }
});

test("every marker the generated table names is a marked line, and only those", () => {
  const types = new Set(MARKERS.map(([, t]) => t));
  assert.ok(types.has("battle") && types.has("setcounter") && types.has("handout"));
  for (const [marker, type] of MARKERS) {
    const [b] = xyChgk.parseBlocks(`${marker} текст`);
    assert.equal(b.type, type, marker);
    assert.equal(b.text, "текст");
  }
  assert.equal(xyChgk.parseBlocks("просто строка")[0].type, "pre");
});

test("imgRefs agrees with the inline tokenizer's img runs", () => {
  for (const { source, refs } of corpus.imgRefs) {
    assert.deepEqual(xyChgk.imgRefs(source), refs, JSON.stringify(source));
  }
});

test("generateHndt writes the corpus .hndt byte for byte, and reads its settings back", () => {
  const { cards, numbers, metas, text, blocks } = corpus.hndt;
  assert.equal(xyChgk.generateHndt(cards, numbers, metas), text);
  assert.deepEqual(xyChgk.parseHndtMetaByQuestion(text), { "1": "columns: 3", "3": "columns: 2\nfont_size: 14", "4": "columns: 3" });
  assert.equal(blocks.length, 3, "and the server parsed it into one block per handout");
});
