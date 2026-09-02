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
