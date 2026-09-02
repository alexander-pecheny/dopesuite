import { assertEquals } from "jsr:@std/assert";
import { asPicker, asText } from "../assets/dist/esm/datetime.js";

Deno.test("asPicker reads what a person typed or pasted", () => {
  assertEquals(asPicker("2026-09-04 19:00"), "2026-09-04T19:00");
  assertEquals(asPicker(" 2026-09-04T19:00 "), "2026-09-04T19:00");
  assertEquals(asPicker("2026-09-04 19:00:30"), "2026-09-04T19:00");
  assertEquals(asPicker("2026-09-04"), "");
  assertEquals(asPicker("завтра"), "");
  assertEquals(asPicker(""), "");
});

Deno.test("asText is what the field posts", () => {
  assertEquals(asText("2026-09-04T19:00"), "2026-09-04 19:00");
  assertEquals(asText("2026-09-04T19:00:00"), "2026-09-04 19:00");
});

// mountDatetimeField wires real nodes, so it gets a stub of the three it looks
// for. Nothing here needs layout.
function stubField(withShowPicker) {
  const node = (extra = {}) => ({
    listeners: {},
    value: "",
    focused: false,
    addEventListener(type, fn) {
      (this.listeners[type] ||= []).push(fn);
    },
    fire(type) {
      for (const fn of this.listeners[type] || []) fn({type});
    },
    focus() {
      this.focused = true;
    },
    dispatchEvent() {},
    ...extra,
  });
  let shown = 0;
  const text = node();
  const picker = node(withShowPicker ? {showPicker() { shown += 1; }} : {});
  const button = node();
  const field = {
    querySelector(sel) {
      if (sel.includes("text")) return text;
      if (sel.includes("picker")) return picker;
      return button;
    },
  };
  return {field, text, picker, button, shown: () => shown};
}

Deno.test("the calendar button opens the picker where showPicker exists", async () => {
  const {mountDatetimeField} = await import("../assets/dist/esm/datetime.js");
  const f = stubField(true);
  mountDatetimeField(f.field);
  f.button.fire("click");
  assertEquals(f.shown(), 1);
  assertEquals(f.picker.focused, false);
});

Deno.test("without showPicker the hidden input never takes the caret", async () => {
  const {mountDatetimeField} = await import("../assets/dist/esm/datetime.js");
  const f = stubField(false);
  mountDatetimeField(f.field);
  f.text.fire("focus");
  f.button.fire("click");
  assertEquals(f.picker.focused, false, "focusing the hidden input makes the text field untypeable");
});
