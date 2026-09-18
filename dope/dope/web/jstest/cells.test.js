import {test} from "node:test";
import assert from "node:assert/strict";
import * as T from "./dist/cells.js";

globalThis.window = {};
globalThis.document = {createElement: () => ({className: "", textContent: ""})};

test("questionNumberNode shrinks the number from 100 on", () => {
  assert.equal(T.questionNumberNode(99).className, "", "two digits keep the header's own size");
  assert.equal(T.questionNumberNode(99).textContent, "99");
  assert.equal(T.questionNumberNode(100).className, "q-num-wide", "three digits are printed smaller");
  assert.equal(T.questionNumberNode(120).textContent, "120");
});
