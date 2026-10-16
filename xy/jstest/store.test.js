import { test } from "node:test";
import assert from "node:assert/strict";
import { renameLegacyFields } from "../web/assets/static/dist/store.js";

test("a write queued before v29 is rewritten to the new field names", () => {
  const op = {
    seq: 4, kind: "patchCard", method: "PATCH", path: "/api/cards/-3",
    body: { description_enc: "D", desc_event_enc: "E", alias_enc: "", rank: "a0" },
  };
  assert.deepEqual(renameLegacyFields(op), {
    seq: 4, kind: "patchCard", method: "PATCH", path: "/api/cards/-3",
    body: { rank: "a0", description: "D", desc_event: "E", alias: "" },
  });
});

test("nested events and arrays are renamed too, and other values are kept", () => {
  const body = { events: [{ type: "label_add", payload_enc: "P" }], list_ids: [1, 2], reply_to_id: null };
  assert.deepEqual(renameLegacyFields(body), { events: [{ type: "label_add", payload: "P" }], list_ids: [1, 2], reply_to_id: null });
});

test("a snapshot that has both names keeps the new one", () => {
  const snap = { name: "Доска", name_enc: "", schema_version: 2, labels: [{ id: 1, name_enc: "N", color_enc: "C" }] };
  assert.deepEqual(renameLegacyFields(snap), { name: "Доска", schema_version: 2, labels: [{ id: 1, name: "N", color: "C" }] });
});

test("a record with no old names comes back equal", () => {
  const op = { kind: "createList", body: { title: "T", rank: "a0", type: "normal" } };
  assert.deepEqual(renameLegacyFields(op), op);
});
