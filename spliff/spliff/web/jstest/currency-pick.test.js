import {test} from "node:test";
import assert from "node:assert/strict";
import {COMMON, currencyChoices, isKnownCode, normaliseCode} from "./dist/currency-pick.js";

// A stand-in for what /api/currencies sends: the codes the newest Rate table
// carries, in code order, each with its English name.
const ALL = [
  {code: "AMD", name: "Armenian Dram"},
  {code: "CZK", name: "Czech Koruna"},
  {code: "EUR", name: "Euro"},
  {code: "GBP", name: "Pound Sterling"},
  {code: "GEL", name: "Georgian Lari"},
  {code: "GHS", name: "Ghana Cedi"},
  {code: "GIP", name: "Gibraltar Pound"},
  {code: "JPY", name: "Japanese Yen"},
  {code: "KZT", name: "Kazakhstani Tenge"},
  {code: "PLN", name: "Polish Zloty"},
  {code: "RSD", name: "Serbian Dinar"},
  {code: "RUB", name: "Russian Ruble"},
  {code: "TRY", name: "Turkish Lira"},
  {code: "UAH", name: "Ukrainian Hryvnia"},
  {code: "USD", name: "US Dollar"},
];

const codes = (choices) => choices.map((c) => c.value);

test("typing a code prefix puts that code first", () => {
  // "ge" is GEL's code and also sits inside "Kazakhstani Tenge": the code wins.
  assert.deepEqual(codes(currencyChoices({all: ALL}, "ge")), ["GEL", "KZT"]);
  assert.deepEqual(codes(currencyChoices({all: ALL}, "g")).slice(0, 4), ["GBP", "GEL", "GHS", "GIP"]);
});

test("the code is matched however it is typed", () => {
  assert.deepEqual(codes(currencyChoices({all: ALL}, "GEL")), ["GEL"]);
  assert.deepEqual(codes(currencyChoices({all: ALL}, "  gel ")), ["GEL"]);
});

test("codes come before names, and a name matches anywhere in it", () => {
  // "ru" is a prefix of RUB's code and also sits inside "Czech Koruna" — the
  // code match is the reason RUB leads and CZK follows it.
  assert.deepEqual(codes(currencyChoices({all: ALL}, "ru")), ["RUB", "CZK"]);
  // Nothing's code starts with "dollar", so the name search answers alone.
  assert.deepEqual(codes(currencyChoices({all: ALL}, "dollar")), ["USD"]);
  assert.deepEqual(codes(currencyChoices({all: ALL}, "pound")), ["GBP", "GIP"]);
});

test("a name search finds the money by whose it is", () => {
  assert.deepEqual(codes(currencyChoices({all: ALL}, "georgian")), ["GEL"]);
  assert.deepEqual(codes(currencyChoices({all: ALL}, "tenge")), ["KZT"]);
});

test("a query nothing answers draws nothing", () => {
  assert.deepEqual(codes(currencyChoices({all: ALL}, "zzz")), []);
});

test("a row is the code, bold, with the name beside it", () => {
  const [row] = currencyChoices({all: ALL}, "gel");
  assert.deepEqual(row, {value: "GEL", label: "GEL", hint: "Georgian Lari", strong: true});
});

test("with nothing typed and no history, the common shortlist leads", () => {
  const opening = codes(currencyChoices({all: ALL}, ""));
  // Every code of the shortlist this fixture carries, in shortlist order.
  const wanted = COMMON.filter((c) => ALL.some((x) => x.code === c));
  assert.deepEqual(opening.slice(0, wanted.length), wanted);
});

test("with nothing typed, the group's own currencies lead the shortlist", () => {
  const opening = codes(currencyChoices({all: ALL, recent: ["GEL", "JPY"]}, ""));
  assert.deepEqual(opening.slice(0, 2), ["GEL", "JPY"]);
  // And neither is offered a second time further down.
  assert.deepEqual(opening.filter((c) => c === "GEL").length, 1);
});

test("the opening list runs off the end of the shortlist into the rest", () => {
  const short = currencyChoices({all: ALL}, "", 14);
  assert.deepEqual(short.length, 14);
  assert.deepEqual(new Set(codes(short)).size, 14);
  // AMD is neither recent nor common here, so it can only have come from the rest.
  assert.deepEqual(codes(short).includes("AMD"), true);
});

test("the list never draws more rows than it was asked for", () => {
  assert.deepEqual(currencyChoices({all: ALL}, "", 3).length, 3);
  assert.deepEqual(currencyChoices({all: ALL}, "g", 2).length, 2);
});

test("a currency the rate source has dropped is still the group's own", () => {
  // A Transaction keeps its own currency forever, so the editor has to be able
  // to state one the newest Rate table no longer carries.
  const list = {all: ALL, recent: ["GEL", "BYN"]};
  assert.deepEqual(codes(currencyChoices(list, "")).slice(0, 2), ["GEL", "BYN"]);
  assert.deepEqual(codes(currencyChoices(list, "by")), ["BYN"]);
  assert.deepEqual(isKnownCode(list, "BYN"), true);
});

test("isKnownCode is what the form checks before it submits", () => {
  assert.deepEqual(isKnownCode({all: ALL}, "GEL"), true);
  assert.deepEqual(isKnownCode({all: ALL}, " gel "), true);
  assert.deepEqual(isKnownCode({all: ALL}, "Georgian Lari"), false);
  assert.deepEqual(isKnownCode({all: ALL}, "XYZ"), false);
  assert.deepEqual(isKnownCode({all: ALL}, ""), false);
});

test("normaliseCode is the one spelling of a code", () => {
  assert.deepEqual(normaliseCode("  gel "), "GEL");
  assert.deepEqual(normaliseCode(""), "");
});
