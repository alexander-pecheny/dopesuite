// Regenerates internal/xycli/testdata/envelope.json — the TS half of the
// Envelope parity corpus: what crypto.ts sealed, which the Go port must open
// (internal/xycli/envelope_test.go). The Go half of the same file is written by
// `go test ./internal/xycli -run TestEnvelopeParity -update`, and jstest/
// envelope_parity.test.js opens that. Run from xy/:
//   deno run --allow-read --allow-write scripts/gen_envelope_fixture.js
import { xyCrypto } from "../web/assets/static/dist/crypto.js";

const OUT = new URL("../internal/xycli/testdata/envelope.json", import.meta.url);
const passphrase = "correct horse battery staple";
const { keymeta, dk } = await xyCrypto.createBoardKeys(passphrase);
const plain = [
  "первый вопрос",
  "Вопрос 1: что это?\nОтвет: Envelope\n",
  "",
  "emoji 🎲 и «ёлочки» — тире",
];
const ts_sealed = [];
for (const p of plain) ts_sealed.push({ plain: p, enc: await xyCrypto.encField(dk, p) });

let go_sealed = [];
try {
  go_sealed = JSON.parse(await Deno.readTextFile(OUT)).go_sealed ?? [];
} catch (_) { /* first run */ }

await Deno.writeTextFile(OUT, JSON.stringify({ passphrase, keymeta, ts_sealed, go_sealed }, null, 1) + "\n");
console.log("wrote", OUT.pathname);
