import {assertEquals} from "jsr:@std/assert";
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

Deno.test("undo takes back this host's last action, a whole range at once", () => {
  const docs = {A: {marks: ["", "", ""]}};
  const {undo, edit, gesture} = sheet(docs);
  edit("A", ["marks", 0], "right");
  gesture();
  edit("A", ["marks", 1], "wrong");
  edit("A", ["marks", 2], "wrong");
  gesture();
  assertEquals(undo.undo(), {codes: ["A"], skipped: 0});
  assertEquals(docs.A.marks, ["right", "", ""]);
  undo.undo();
  assertEquals(docs.A.marks, ["", "", ""]);
  assertEquals(undo.undo(), null);
});

Deno.test("undo never touches another host's edits", () => {
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
  assertEquals(result, {codes: ["A"], skipped: 1});
  // This host's own cell goes back; the one the other host changed keeps
  // their mark, and their bout is untouched.
  assertEquals(docs.A.marks, ["", "wrong"]);
  assertEquals(docs.B.marks, ["wrong"]);
});

Deno.test("a cell set twice in one action goes back to what it held first", () => {
  const docs = {A: {pin: null}};
  const {undo, edit, gesture} = sheet(docs);
  edit("A", ["pin"], 2);
  edit("A", ["pin"], 3);
  gesture();
  undo.undo();
  assertEquals(docs.A.pin, null);
});

Deno.test("only the last 200 actions are kept", () => {
  const docs = {A: {n: 0}};
  const {undo, edit, gesture} = sheet(docs);
  for (let i = 1; i <= 205; i++) {
    edit("A", ["n"], i);
    gesture();
  }
  assertEquals(undo.size(), 200);
});

Deno.test("Ctrl+Z and ⌘Z are undo on any layout; Shift is not", () => {
  const key = (init) => ({ctrlKey: false, metaKey: false, shiftKey: false, altKey: false, key: "", code: "", ...init});
  assertEquals(isUndoKey(key({ctrlKey: true, key: "z", code: "KeyZ"})), true);
  assertEquals(isUndoKey(key({metaKey: true, key: "я", code: "KeyZ"})), true);
  assertEquals(isUndoKey(key({ctrlKey: true, shiftKey: true, key: "Z", code: "KeyZ"})), false);
  assertEquals(isUndoKey(key({key: "z", code: "KeyZ"})), false);
});
