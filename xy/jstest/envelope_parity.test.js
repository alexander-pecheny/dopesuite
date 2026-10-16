// The Envelope has two implementations — crypto.ts and Go's chgksuite/xyenvelope.
// This half opens what Go sealed; chgksuite/xyenvelope/envelope_test.go opens
// what this sealed. Corpus: chgksuite/xyenvelope/testdata/envelope.json, written
// by scripts/gen_envelope_fixture.js (TS half) and `go test ./xyenvelope -run
// TestEnvelopeParity -update` in chgksuite/ (Go half).
import { test } from "node:test";
import assert from "node:assert/strict";
import { xyCrypto } from "../web/assets/static/dist/crypto.js";
import fixture from "../../chgksuite/xyenvelope/testdata/envelope.json" with { type: "json" };

test("the browser opens what xy-cli sealed", async () => {
  assert.ok(fixture.go_sealed.length, "no Go-sealed fields — run the -update pass");
  const dk = await xyCrypto.unlockBoard(fixture.passphrase, fixture.keymeta);
  for (const { plain, enc } of fixture.go_sealed) {
    assert.equal(await xyCrypto.decField(dk, enc), plain);
  }
});
