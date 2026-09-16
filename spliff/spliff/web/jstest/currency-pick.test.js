import {assertEquals} from "https://deno.land/std@0.224.0/assert/mod.ts";
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

Deno.test("typing a code prefix puts that code first", () => {
  // "ge" is GEL's code and also sits inside "Kazakhstani Tenge": the code wins.
  assertEquals(codes(currencyChoices({all: ALL}, "ge")), ["GEL", "KZT"]);
  assertEquals(codes(currencyChoices({all: ALL}, "g")).slice(0, 4), ["GBP", "GEL", "GHS", "GIP"]);
});

Deno.test("the code is matched however it is typed", () => {
  assertEquals(codes(currencyChoices({all: ALL}, "GEL")), ["GEL"]);
  assertEquals(codes(currencyChoices({all: ALL}, "  gel ")), ["GEL"]);
});

Deno.test("codes come before names, and a name matches anywhere in it", () => {
  // "ru" is a prefix of RUB's code and also sits inside "Czech Koruna" — the
  // code match is the reason RUB leads and CZK follows it.
  assertEquals(codes(currencyChoices({all: ALL}, "ru")), ["RUB", "CZK"]);
  // Nothing's code starts with "dollar", so the name search answers alone.
  assertEquals(codes(currencyChoices({all: ALL}, "dollar")), ["USD"]);
  assertEquals(codes(currencyChoices({all: ALL}, "pound")), ["GBP", "GIP"]);
});

Deno.test("a name search finds the money by whose it is", () => {
  assertEquals(codes(currencyChoices({all: ALL}, "georgian")), ["GEL"]);
  assertEquals(codes(currencyChoices({all: ALL}, "tenge")), ["KZT"]);
});

Deno.test("a query nothing answers draws nothing", () => {
  assertEquals(codes(currencyChoices({all: ALL}, "zzz")), []);
});

Deno.test("a row is the code, bold, with the name beside it", () => {
  const [row] = currencyChoices({all: ALL}, "gel");
  assertEquals(row, {value: "GEL", label: "GEL", hint: "Georgian Lari", strong: true});
});

Deno.test("with nothing typed and no history, the common shortlist leads", () => {
  const opening = codes(currencyChoices({all: ALL}, ""));
  // Every code of the shortlist this fixture carries, in shortlist order.
  const wanted = COMMON.filter((c) => ALL.some((x) => x.code === c));
  assertEquals(opening.slice(0, wanted.length), wanted);
});

Deno.test("with nothing typed, the group's own currencies lead the shortlist", () => {
  const opening = codes(currencyChoices({all: ALL, recent: ["GEL", "JPY"]}, ""));
  assertEquals(opening.slice(0, 2), ["GEL", "JPY"]);
  // And neither is offered a second time further down.
  assertEquals(opening.filter((c) => c === "GEL").length, 1);
});

Deno.test("the opening list runs off the end of the shortlist into the rest", () => {
  const short = currencyChoices({all: ALL}, "", 14);
  assertEquals(short.length, 14);
  assertEquals(new Set(codes(short)).size, 14);
  // AMD is neither recent nor common here, so it can only have come from the rest.
  assertEquals(codes(short).includes("AMD"), true);
});

Deno.test("the list never draws more rows than it was asked for", () => {
  assertEquals(currencyChoices({all: ALL}, "", 3).length, 3);
  assertEquals(currencyChoices({all: ALL}, "g", 2).length, 2);
});

Deno.test("a currency the rate source has dropped is still the group's own", () => {
  // A Transaction keeps its own currency forever, so the editor has to be able
  // to state one the newest Rate table no longer carries.
  const list = {all: ALL, recent: ["GEL", "BYN"]};
  assertEquals(codes(currencyChoices(list, "")).slice(0, 2), ["GEL", "BYN"]);
  assertEquals(codes(currencyChoices(list, "by")), ["BYN"]);
  assertEquals(isKnownCode(list, "BYN"), true);
});

Deno.test("isKnownCode is what the form checks before it submits", () => {
  assertEquals(isKnownCode({all: ALL}, "GEL"), true);
  assertEquals(isKnownCode({all: ALL}, " gel "), true);
  assertEquals(isKnownCode({all: ALL}, "Georgian Lari"), false);
  assertEquals(isKnownCode({all: ALL}, "XYZ"), false);
  assertEquals(isKnownCode({all: ALL}, ""), false);
});

Deno.test("normaliseCode is the one spelling of a code", () => {
  assertEquals(normaliseCode("  gel "), "GEL");
  assertEquals(normaliseCode(""), "");
});
