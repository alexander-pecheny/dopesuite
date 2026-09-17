// The Transaction editor. A bill answers two questions — who handed the money
// over, and who it was for — and the page asks each of them the same way: a
// table of rows, one row per person, with the amount beside them and an × to
// take the row away. What is typed IS what is stored (Payments and Shares, in
// minor units), so there is nothing between the two tables and the ledger.

import S from "./i18nstrings.js";
import {
  errorText,
  get,
  request,
  type EntryDTO,
  type HistoryDTO,
  type MemberDTO,
  type PhotoDTO,
  type TransactionViewDTO,
} from "./api";
import { icon } from "../../../../dopeuikit/assets/ts/icons_gen.js";
import { bindCurrency, bindCurrencyCell } from "./currency-field";
import { byId, clear, el, group as rowGroup, maybe, setText, show, stamp } from "./dom";
import { formatMinor, parseAmount } from "./money";
import {
  buildDraft,
  evenShares,
  paidLeft,
  type DraftError,
  type FormState,
  type Row,
} from "./txform";

const path = window.location.pathname.split("/").filter(Boolean);
// /group/{id}/new when a bill is being entered, /transaction/{id} when one is
// being read or changed.
const creating = path[0] === "group";
// The Group is in the path when a bill is being entered, and comes back with
// the Transaction when one is being read.
let groupID = creating ? Number(path[1]) : 0;
const txID = creating ? 0 : Number(path[1]);

const txCrumb = maybe("txCrumb");
const groupCrumb = maybe<HTMLAnchorElement>("groupCrumb");
const form = byId<HTMLFormElement>("txForm");
const description = byId<HTMLInputElement>("description");
const dayField = byId<HTMLInputElement>("day");
const currencyInput = byId<HTMLInputElement>("currency");
const currencyField = bindCurrency(currencyInput, byId("currencyError"));
const totalField = byId<HTMLInputElement>("total");
const rateLine = byId("rateLine");
const payerBody = byId("payerBody");
const shareBody = byId("shareBody");
const paidLine = byId("paidLine");
const totalsLine = byId("totalsLine");
const message = byId("message");
const deleteBtn = byId<HTMLButtonElement>("deleteBtn");
const restoreBtn = byId<HTMLButtonElement>("restoreBtn");

const photoSection = byId("photoSection");
const photos = byId("photos");
const photosEmpty = byId("photosEmpty");
const photoInput = byId<HTMLInputElement>("photoInput");
const photoMessage = byId("photoMessage");
const historySection = byId("historySection");
const historyList = byId("history");

// The date field is a native picker: the audience enters bills at the table,
// and typing 2026-09-15 on a phone keyboard is not entering a bill.
dayField.type = "date";

let members: MemberDTO[] = [];
let me = 0;
let baseCurrency = "EUR";
let groupName = "";

currencyInput.addEventListener("input", () => {
  const code = currencyField.value();
  if (code) setCurrency(code);
  recompute();
});
totalField.addEventListener("input", recompute);

form.addEventListener("submit", (event) => {
  event.preventDefault();
  void save();
});

byId("cancelBtn").addEventListener("click", () => {
  window.location.href = groupID ? `/group/${groupID}` : "/";
});

deleteBtn.addEventListener("click", () => {
  if (!window.confirm(S.page.transaction.confirmDelete())) return;
  void act("DELETE", `/api/transactions/${txID}`);
});

restoreBtn.addEventListener("click", () => {
  void act("POST", `/api/transactions/${txID}/restore`);
});

byId("uploadBtn").addEventListener("click", () => {
  void upload();
});

byId("addPayer").addEventListener("click", () => {
  // A second payer means the first one's amount is now a real answer to a real
  // question, so the page stops writing the total into it.
  payerFollowsTotal = false;
  addRow(payerRows, payerBody, 0, "").person.focus();
  recompute();
});

byId("addShare").addEventListener("click", () => {
  addRow(shareRows, shareBody, 0, "").person.focus();
  recompute();
});

byId("everyoneBtn").addEventListener("click", () => {
  everyone();
});

byId("splitBtn").addEventListener("click", () => {
  splitEvenly();
});

async function act(method: string, url: string): Promise<void> {
  setText(message, "");
  try {
    await request(method, url);
    window.location.href = `/group/${groupID}`;
  } catch (error) {
    setText(message, errorText(error));
  }
}

async function load(): Promise<void> {
  try {
    if (creating) {
      const group = await get<{
        id: number;
        name: string;
        base_currency: string;
        me: number;
        members: MemberDTO[];
        live: { currency: string }[];
      }>(`/api/groups/${groupID}`);
      members = group.members;
      me = group.me;
      baseCurrency = group.base_currency;
      groupName = group.name;
      // The picker opens on what this Group deals in: its Base currency, then
      // the currencies its Transactions are already written in.
      fillCurrencies(baseCurrency, [baseCurrency, ...group.live.map((t) => t.currency)]);
      dayField.value = new Date().toISOString().slice(0, 10);
      setText(txCrumb, S.page.transaction.newTitle());
      show(photoSection, false);
      show(historySection, false);
      // A new bill opens on the likeliest shape of one: the person entering it
      // paid, and nobody has been named on the other side yet.
      addRow(payerRows, payerBody, me, "");
      addRow(shareRows, shareBody, 0, "");
    } else {
      const view = await get<TransactionViewDTO>(`/api/transactions/${txID}`);
      members = view.members;
      me = view.group.me;
      baseCurrency = view.group.base_currency;
      groupName = view.group.name;
      groupID = view.group.id;
      // Reading one Transaction does not fetch the Group's others, so the
      // history here is the two currencies this page is sure of.
      fillCurrencies(view.transaction.currency, [view.transaction.currency, baseCurrency]);
      fill(view);
    }
    if (groupCrumb) {
      groupCrumb.href = `/group/${groupID}`;
      groupCrumb.textContent = groupName;
    }
    recompute();
  } catch (error) {
    setText(message, errorText(error));
  }
}

function fill(view: TransactionViewDTO): void {
  const tx = view.transaction;
  document.title = tx.description;
  setText(txCrumb, tx.description);
  description.value = tx.description;
  dayField.value = tx.day;
  totalField.value = tx.total;
  show(deleteBtn, !tx.deleted);
  show(restoreBtn, tx.deleted);
  if (tx.rate_date) {
    setText(rateLine, S.page.transaction.rateAsOf(tx.rate_date));
    show(rateLine, true);
  }
  show(photoSection, true);
  show(historySection, true);
  renderPhotos(tx.photos);
  renderHistory(view.history);
  // A Transaction that exists is read back as the rows it is made of, which is
  // also how it is stored: nothing is inferred and nothing is re-derived.
  payerFollowsTotal = false;
  seed(payerRows, payerBody, tx.payments, tx.currency);
  seed(shareRows, shareBody, tx.shares, tx.currency);
}

function seed(list: Cell[], body: HTMLElement, entries: EntryDTO[], code: string): void {
  for (const entry of entries) addRow(list, body, entry.member_id, formatMinor(entry.minor, code));
  // Never an empty table: the shape of the answer is visible before it is given.
  if (entries.length === 0) addRow(list, body, 0, "");
}

function fillCurrencies(selected: string, recent: string[]): void {
  void currencyField.fill(selected, recent).then(() => {
    setCurrency(currencyField.value() || selected);
    recompute();
  });
}

// ── the two tables ─────────────────────────────────────────────────────────
//
// One row is one person and one amount, on both sides of the bill. The rows
// ARE the state: there is no model behind them to fall out of step with what is
// on the screen, and reading one back (rowsOf) is reading the cells.
//
// The currency cell is in every row because that is where the amount is — but a
// Transaction happens in ONE currency (spliff/docs/adr/0001), so the cells are
// one value: picking a code in any row is picking it for the bill, and every
// other cell and the header follow.

interface Cell {
  node: HTMLElement;
  person: HTMLSelectElement;
  currency: HTMLInputElement;
  amount: HTMLInputElement;
}

let payerRows: Cell[] = [];
let shareRows: Cell[] = [];

// Most bills have one payer, and typing the same number twice is not entering a
// bill: the single payer's row follows the Total until somebody says otherwise.
let payerFollowsTotal = true;

function currency(): string {
  return currencyField.value() || baseCurrency;
}

function setCurrency(code: string): void {
  if (currencyInput.value !== code) currencyInput.value = code;
  for (const row of [...payerRows, ...shareRows]) {
    if (row.currency !== document.activeElement) row.currency.value = code;
  }
}

function personSelect(selected: number): HTMLSelectElement {
  // The one cell that gives way: a name can be read at half its width and an
  // amount cannot, so the select takes what the two numbers leave.
  const select = el("select", "input u-grow");
  const none = el("option", undefined, S.page.transaction.pickPerson());
  none.value = "0";
  select.append(none);
  for (const member of members) {
    const option = el("option", undefined, member.name);
    option.value = String(member.id);
    select.append(option);
  }
  select.value = String(selected);
  select.setAttribute("aria-label", S.page.transaction.rowPerson());
  return select;
}

function addRow(list: Cell[], body: HTMLElement, member: number, amount: string): Cell {
  const node = el("div", "card split-row u-row u-wrap u-align-center u-gap-sm");
  const person = personSelect(member);

  // The suggest popover is appended to the input's own parent, so the cell is
  // the anchor it needs and the list lands on the field rather than beside it.
  const cell = el("span", "suggest-anchor u-col");
  const code = el("input", "input input-narrow");
  code.type = "text";
  code.autocomplete = "off";
  code.spellcheck = false;
  code.setAttribute("autocapitalize", "characters");
  code.setAttribute("aria-label", S.page.transaction.rowCurrency());
  code.value = currency();
  cell.append(code);
  bindCurrencyCell(code, (picked) => {
    setCurrency(picked);
    recompute();
  });

  const value = el("input", "input amount-field");
  value.inputMode = "decimal";
  value.placeholder = "0.00";
  value.value = amount;
  value.setAttribute("aria-label", S.page.transaction.rowAmount());

  const remove = el("button", "action-icon");
  remove.type = "button";
  remove.setAttribute("aria-label", S.page.transaction.removeRow());
  remove.title = S.page.transaction.removeRow();
  remove.append(icon("x"));

  const entry: Cell = { node, person, currency: code, amount: value };
  person.addEventListener("change", recompute);
  value.addEventListener("input", () => {
    if (list === payerRows) payerFollowsTotal = false;
    recompute();
  });
  remove.addEventListener("click", () => {
    const at = list.indexOf(entry);
    if (at >= 0) list.splice(at, 1);
    node.remove();
    // A table with nothing in it cannot be added to by example, so the row a
    // person deleted last comes back empty rather than leaving a hole.
    if (list.length === 0) addRow(list, body, 0, "");
    recompute();
  });

  node.append(person, cell, value, remove);
  body.append(node);
  list.push(entry);
  return entry;
}

function rowsOf(list: Cell[]): Row[] {
  const code = currency();
  return list.map((row) => ({
    member: Number(row.person.value),
    minor: parseAmount(row.amount.value, code),
  }));
}

function state(): FormState {
  return {
    totalMinor: parseAmount(totalField.value, currency()) ?? 0,
    members: members.map((m) => m.id),
    payments: rowsOf(payerRows),
    shares: rowsOf(shareRows),
  };
}

// Everybody in the Group, on the "For whom" side: the empty rows are used up
// first, and the people already named are left where they are.
function everyone(): void {
  const named = new Set(rowsOf(shareRows).map((row) => row.member));
  const blanks = shareRows.filter((row) => Number(row.person.value) === 0);
  for (const member of members) {
    if (named.has(member.id)) continue;
    const row = blanks.shift() ?? addRow(shareRows, shareBody, 0, "");
    row.person.value = String(member.id);
  }
  recompute();
}

// The one split the editor computes for you, by the same rule the ledger reads
// it back with: equal parts, the odd minor units to whoever paid most.
function splitEvenly(): void {
  const code = currency();
  const amounts = evenShares(state());
  shareRows.forEach((row, i) => {
    if (Number(row.person.value) === 0) return;
    row.amount.value = formatMinor(amounts[i], code);
  });
  recompute();
}

// recompute is what makes the form honest: every line under the tables is
// computed here, on every keystroke, by exactly the rule the ledger reads the
// bill with.
function recompute(): void {
  const code = currency();
  if (payerFollowsTotal && payerRows.length === 1) {
    payerRows[0].amount.value = totalField.value.trim();
  }
  const st = state();

  // A form nobody has typed into yet says nothing: "the payments add up to the
  // total" is true of nothing and nothing, and it is not what somebody opening
  // a blank bill needs to read.
  const started = st.totalMinor !== 0 ||
    [...st.payments, ...st.shares].some((row) => (row.minor ?? 0) !== 0);
  if (!started) {
    setText(paidLine, "");
    setText(totalsLine, "");
    return;
  }

  const left = paidLeft(st);
  setText(paidLine, left === 0
    ? S.page.transaction.paidDone()
    : left > 0
      ? S.page.transaction.paidLeft(`${formatMinor(left, code)} ${code}`)
      : S.page.transaction.paidOver(`${formatMinor(-left, code)} ${code}`));

  const result = buildDraft(st);
  if (result.error) {
    setText(totalsLine, draftError(result.error));
    return;
  }
  const shares = result.draft?.shares ?? [];
  const claimed = shares.reduce((sum, e) => sum + e.minor, 0);
  const unclaimedLeft = st.totalMinor - claimed;
  setText(totalsLine, unclaimedLeft > 0
    ? S.page.transaction.unclaimedNote(`${formatMinor(unclaimedLeft, code)} ${code}`)
    : S.page.transaction.fullyClaimed());
}

function draftError(error: DraftError): string {
  switch (error) {
    case "no_payer":
      return S.page.transaction.needPayer();
    case "payments_mismatch":
      return S.page.transaction.needPaymentsMatch();
    case "shares_overdraw":
      return S.page.transaction.needSharesFit();
    case "negative_amount":
      return S.page.transaction.needPositive();
    case "duplicate_member":
      return S.page.transaction.needOnce();
    default:
      return S.page.transaction.needPerson();
  }
}

async function save(): Promise<void> {
  setText(message, "");
  // A code nobody quotes is said under the field, before the bill is sent.
  if (!currencyField.ok()) return;
  const st = state();
  const result = buildDraft(st);
  if (!result.draft) {
    setText(message, draftError(result.error ?? "no_person"));
    return;
  }
  const body = {
    description: description.value,
    day: dayField.value,
    currency: currency(),
    total_minor: st.totalMinor,
    payments: result.draft.payments,
    shares: result.draft.shares,
  };
  try {
    if (creating) {
      const created = await request<{ id: number }>("POST", `/api/groups/${groupID}/transactions`, body);
      window.location.href = `/transaction/${created.id}`;
      return;
    }
    await request("PUT", `/api/transactions/${txID}`, body);
    window.location.href = `/group/${groupID}`;
  } catch (error) {
    setText(message, errorText(error));
  }
}

async function upload(): Promise<void> {
  setText(photoMessage, "");
  const file = photoInput.files?.[0];
  if (!file) {
    setText(photoMessage, S.page.transaction.pickPhoto());
    return;
  }
  const data = new FormData();
  data.append("photo", file);
  try {
    const response = await fetch(`/api/transactions/${txID}/photos`, { method: "POST", body: data });
    if (!response.ok) throw new Error((await response.text()).trim() || `HTTP ${response.status}`);
    window.location.reload();
  } catch (error) {
    setText(photoMessage, errorText(error));
  }
}

function renderPhotos(list: PhotoDTO[]): void {
  clear(photos);
  const strip = el("span", "u-row u-gap-sm");
  for (const photo of list) {
    const image = el("img", "thumb");
    image.src = photo.url;
    image.alt = S.page.transaction.photoAlt();
    image.width = photo.width;
    image.height = photo.height;
    const link = el("a");
    link.href = photo.url;
    link.target = "_blank";
    link.rel = "noopener";
    link.append(image);

    // Any Member adds a Photo and any Member removes one: trust is social, and
    // History records who did which.
    const remove = el("button", "btn btn-ghost btn-xs", S.page.transaction.removePhoto());
    remove.type = "button";
    remove.addEventListener("click", () => {
      if (!window.confirm(S.page.transaction.confirmRemovePhoto())) return;
      void removePhoto(photo.id);
    });

    const cell = el("span", "u-col u-gap-xs u-align-center");
    cell.append(link, remove);
    strip.append(cell);
  }
  photos.append(strip);
  show(photosEmpty, list.length === 0);
}

async function removePhoto(id: number): Promise<void> {
  setText(photoMessage, "");
  try {
    await request("DELETE", `/api/photos/${id}`);
    window.location.reload();
  } catch (error) {
    setText(photoMessage, errorText(error));
  }
}

function renderHistory(entries: HistoryDTO[]): void {
  clear(historyList);
  for (const entry of entries) {
    const row = el("li", "list-row");
    const left = rowGroup(true);
    left.append(el("span", "list-row-title split-name", entry.actor || S.page.history.somebody()));
    left.append(el("span", "muted", historyVerb(entry.kind)));
    // What changed, when there is something to say: the two JSON snapshots
    // History keeps, read as the fields that differ.
    const diff = describe(entry);
    if (diff) left.append(el("span", "muted", diff));
    row.append(left, el("span", "muted", stamp(entry.at)));
    historyList.append(row);
  }
}

function historyVerb(kind: string): string {
  switch (kind) {
    case "created":
      return S.page.history.created();
    case "edited":
      return S.page.history.edited();
    case "deleted":
      return S.page.history.deleted();
    case "restored":
      return S.page.history.restored();
    case "photo_added":
      return S.page.history.photoAdded();
    case "photo_removed":
      return S.page.history.photoRemoved();
    default:
      return kind;
  }
}

interface Snapshot {
  description: string;
  day: string;
  currency: string;
  total_minor: number;
  shares: Array<{ member_id: number; minor: number }>;
}

// describe says what changed, in the words of the fields that changed. It reads
// the two JSON snapshots History keeps rather than a schema of its own, so a
// field added to a Transaction shows up here the moment it is stored.
function describe(entry: HistoryDTO): string {
  const before = parse(entry.before);
  const after = parse(entry.after);
  if (!before || !after) return "";
  const parts: string[] = [];
  if (before.description !== after.description) {
    parts.push(S.page.history.fieldDescription(before.description, after.description));
  }
  if (before.day !== after.day) parts.push(S.page.history.fieldDay(before.day, after.day));
  if (before.total_minor !== after.total_minor || before.currency !== after.currency) {
    parts.push(S.page.history.fieldTotal(
      `${formatMinor(before.total_minor, before.currency)} ${before.currency}`,
      `${formatMinor(after.total_minor, after.currency)} ${after.currency}`,
    ));
  }
  // A Claim changes no field a person typed into the header — it changes what
  // is left for the others, which is the number they came to see.
  const was = left(before);
  const now = left(after);
  if (was !== now) {
    parts.push(S.page.history.fieldUnclaimed(
      `${formatMinor(was, before.currency)} ${before.currency}`,
      `${formatMinor(now, after.currency)} ${after.currency}`,
    ));
  }
  return parts.join("; ");
}

function left(snapshot: Snapshot): number {
  const claimed = (snapshot.shares ?? []).reduce(
    (sum, e) => sum + (Number.isFinite(e?.minor) ? e.minor : 0), 0);
  return Math.max(0, snapshot.total_minor - claimed);
}

function parse(raw: string): Snapshot | null {
  if (!raw) return null;
  try {
    return JSON.parse(raw) as Snapshot;
  } catch {
    return null;
  }
}

void load();
