import {test} from "node:test";
import assert from "node:assert/strict";
import {createUndo, isUndoKey, valueAt} from "./dist/undo.js";

// A sheet of bouts: code → document. apply writes into it as the writer would.
function sheet(docs) {
  const steps = [];
  const undo = createUndo({
    current: (code, path) => valueAt(docs[code], path),
    apply: (code, path, value) => set(docs[code], path, value),
    schedule: (close) => steps.push(close),
  });
  // edit is this host's own: recorded, then written.
  const edit = (code, path, value) => {
    undo.record(code, path, valueAt(docs[code], path), value);
    set(docs[code], path, value);
  };
  // gesture ends one host action, the way a task boundary does.
  const gesture = () => steps.splice(0).forEach((close) => close());
  return {undo, edit, gesture};
}

function set(doc, path, value) {
  let node = doc;
  for (const part of path.slice(0, -1)) node = node[part] ??= {};
  node[path[path.length - 1]] = value;
}

test("undo takes back this host's last action, a whole range at once", () => {
  const docs = {A: {marks: ["", "", ""]}};
  const {undo, edit, gesture} = sheet(docs);
  edit("A", ["marks", 0], "right");
  gesture();
  edit("A", ["marks", 1], "wrong");
  edit("A", ["marks", 2], "wrong");
  gesture();
  assert.deepEqual(undo.undo(), {codes: ["A"], skipped: 0});
  assert.deepEqual(docs.A.marks, ["right", "", ""]);
  undo.undo();
  assert.deepEqual(docs.A.marks, ["", "", ""]);
  assert.deepEqual(undo.undo(), null);
});

test("undo never touches another host's edits", () => {
  const docs = {A: {marks: ["", ""]}, B: {marks: [""]}};
  const {undo, edit, gesture} = sheet(docs);
  edit("A", ["marks", 0], "right");
  edit("A", ["marks", 1], "right");
  gesture();
  // Another host marks bout B, which is in nobody's stack here, and then
  // changes one of this host's cells.
  docs.B.marks[0] = "wrong";
  docs.A.marks[1] = "wrong";
  const result = undo.undo();
  assert.deepEqual(result, {codes: ["A"], skipped: 1});
  // This host's own cell goes back; the one the other host changed keeps
  // their mark, and their bout is untouched.
  assert.deepEqual(docs.A.marks, ["", "wrong"]);
  assert.deepEqual(docs.B.marks, ["wrong"]);
});

test("a cell set twice in one action goes back to what it held first", () => {
  const docs = {A: {pin: null}};
  const {undo, edit, gesture} = sheet(docs);
  edit("A", ["pin"], 2);
  edit("A", ["pin"], 3);
  gesture();
  undo.undo();
  assert.deepEqual(docs.A.pin, null);
});

test("only the last 200 actions are kept", () => {
  const docs = {A: {n: 0}};
  const {undo, edit, gesture} = sheet(docs);
  for (let i = 1; i <= 205; i++) {
    edit("A", ["n"], i);
    gesture();
  }
  assert.deepEqual(undo.size(), 200);
});

test("Ctrl+Z and ⌘Z are undo on any layout; Shift is not", () => {
  const key = (init) => ({ctrlKey: false, metaKey: false, shiftKey: false, altKey: false, key: "", code: "", ...init});
  assert.deepEqual(isUndoKey(key({ctrlKey: true, key: "z", code: "KeyZ"})), true);
  assert.deepEqual(isUndoKey(key({metaKey: true, key: "я", code: "KeyZ"})), true);
  assert.deepEqual(isUndoKey(key({ctrlKey: true, shiftKey: true, key: "Z", code: "KeyZ"})), false);
  assert.deepEqual(isUndoKey(key({key: "z", code: "KeyZ"})), false);
});
