// The Transaction editor. The modes are the form's — "A paid X for B", split
// even, by percent, by exact amounts, "I paid, claim your part", settlement —
// and they all produce the one model the server stores: Payments that sum to
// the total, and Shares that do not exceed it.

import S from "./i18nstrings.js";
import {
  errorText,
  get,
  request,
  type HistoryDTO,
  type MemberDTO,
  type PhotoDTO,
  type TransactionViewDTO,
} from "./api";
import { byId, clear, el, maybe, setText, show } from "./dom";
import { formatMinor, parseAmount } from "./money";
import { allocateByPercent, allocateEven, buildDraft, payerOrder, type FormState, type Mode } from "./txform";

const path = window.location.pathname.split("/").filter(Boolean);
// /group/{id}/new when a bill is being entered, /transaction/{id} when one is
// being read or changed.
const creating = path[0] === "group";
// The Group is in the path when a bill is being entered, and comes back with
// the Transaction when one is being read.
let groupID = creating ? Number(path[1]) : 0;
const txID = creating ? 0 : Number(path[1]);

const title = maybe("txTitle");
const groupCrumb = maybe<HTMLAnchorElement>("groupCrumb");
const form = byId<HTMLFormElement>("txForm");
const description = byId<HTMLInputElement>("description");
const dayField = byId<HTMLInputElement>("day");
const currencyField = byId<HTMLSelectElement>("currency");
const totalField = byId<HTMLInputElement>("total");
const rateLine = byId("rateLine");
const modeField = byId<HTMLSelectElement>("mode");
const modeHint = byId("modeHint");
const editorBody = byId("editorBody");
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

interface Row {
  member: MemberDTO;
  include: HTMLInputElement;
  paid: HTMLInputElement;
  share: HTMLInputElement;
  percent: HTMLInputElement;
  payee: HTMLInputElement;
  computed: HTMLElement;
}

let rows: Row[] = [];

modeField.addEventListener("change", () => {
  applyMode();
  recompute();
});

currencyField.addEventListener("change", recompute);
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
      }>(`/api/groups/${groupID}`);
      members = group.members;
      me = group.me;
      baseCurrency = group.base_currency;
      groupName = group.name;
      fillCurrencies(baseCurrency);
      dayField.value = new Date().toISOString().slice(0, 10);
      setText(title, S.page.transaction.newTitle());
      show(photoSection, false);
      show(historySection, false);
    } else {
      const view = await get<TransactionViewDTO>(`/api/transactions/${txID}`);
      members = view.members;
      me = view.group.me;
      baseCurrency = view.group.base_currency;
      groupName = view.group.name;
      groupID = view.group.id;
      fillCurrencies(view.transaction.currency);
      fill(view);
    }
    if (groupCrumb) {
      groupCrumb.href = `/group/${groupID}`;
      groupCrumb.textContent = groupName;
    }
    buildRows();
    applyMode();
    recompute();
  } catch (error) {
    setText(message, errorText(error));
  }
}

function fill(view: TransactionViewDTO): void {
  const tx = view.transaction;
  document.title = tx.description;
  setText(title, tx.description);
  description.value = tx.description;
  dayField.value = tx.day;
  totalField.value = tx.total;
  show(deleteBtn, !tx.deleted);
  show(restoreBtn, tx.deleted);
  if (tx.rate_date) {
    setText(rateLine, S.page.transaction.rateAsOf(tx.rate_date));
    show(rateLine, true);
  }
  // A Transaction that exists is read back in the mode that states it exactly:
  // the amounts are what is stored, so "by exact amounts" is the truthful one.
  modeField.value = "exact";
  show(photoSection, true);
  show(historySection, true);
  renderPhotos(tx.photos);
  renderHistory(view.history);
  prefill(tx.payments, tx.shares);
}

let prefilled: { payments: Map<number, number>; shares: Map<number, number> } | null = null;

function prefill(payments: { member_id: number; minor: number }[], shares: { member_id: number; minor: number }[]): void {
  prefilled = {
    payments: new Map(payments.map((e) => [e.member_id, e.minor])),
    shares: new Map(shares.map((e) => [e.member_id, e.minor])),
  };
}

function currency(): string {
  return currencyField.value || baseCurrency;
}

let currencies: string[] | null = null;

function fillCurrencies(selected: string): void {
  void (async () => {
    if (!currencies) {
      try {
        currencies = (await get<string[]>("/api/currencies")).sort();
      } catch {
        currencies = [];
      }
    }
    if (!currencies.includes(selected)) currencies.unshift(selected);
    clear(currencyField);
    for (const code of currencies) {
      const option = el("option", undefined, code);
      option.value = code;
      option.selected = code === selected;
      currencyField.append(option);
    }
    recompute();
  })();
}

function buildRows(): void {
  clear(editorBody);
  rows = [];
  editorBody.append(
    el("span", "split-head", S.page.transaction.colMember()),
    el("span", "split-head", S.page.transaction.colPaid()),
    el("span", "split-head", S.page.transaction.colShare()),
  );
  for (const member of members) {
    const include = el("input");
    include.type = "checkbox";
    include.checked = true;
    include.addEventListener("change", recompute);

    const name = el("label", "split-name");
    name.append(include, document.createTextNode(` ${member.name}`));

    const paid = el("input", "input");
    paid.inputMode = "decimal";
    paid.placeholder = "0";
    paid.addEventListener("input", recompute);

    const share = el("input", "input");
    share.inputMode = "decimal";
    share.placeholder = "0";
    share.addEventListener("input", recompute);

    const percent = el("input", "input");
    percent.inputMode = "decimal";
    percent.placeholder = "%";
    percent.addEventListener("input", recompute);

    const payee = el("input");
    payee.type = "radio";
    payee.name = "payee";
    payee.value = String(member.user_id);
    payee.addEventListener("change", recompute);

    const computed = el("span", "amount");

    const cell = el("span", "u-row u-gap-sm u-align-center");
    cell.append(share, percent, payee, computed);

    editorBody.append(name, paid, cell);
    rows.push({ member, include, paid, share, percent, payee, computed });

    if (prefilled) {
      const paidMinor = prefilled.payments.get(member.user_id);
      if (paidMinor) paid.value = formatMinor(paidMinor, currency());
      const shareMinor = prefilled.shares.get(member.user_id);
      if (shareMinor) share.value = formatMinor(shareMinor, currency());
      include.checked = Boolean(shareMinor);
    }
  }
}

function mode(): Mode {
  return modeField.value as Mode;
}

// applyMode shows only the controls the chosen mode uses. Every mode writes the
// same model; what differs is which of the three columns a person touches.
function applyMode(): void {
  const m = mode();
  const hints: Record<Mode, string> = {
    simple: S.page.transaction.hintSimple(),
    even: S.page.transaction.hintEven(),
    percent: S.page.transaction.hintPercent(),
    exact: S.page.transaction.hintExact(),
    claim: S.page.transaction.hintClaim(),
    settlement: S.page.transaction.hintSettlement(),
  };
  setText(modeHint, hints[m]);
  for (const row of rows) {
    show(row.include, m === "even" || m === "percent");
    show(row.share, m === "exact" || m === "claim");
    show(row.percent, m === "percent");
    show(row.payee, m === "simple" || m === "settlement");
    show(row.computed, m === "even" || m === "percent");
    row.share.disabled = m === "claim" && row.member.user_id !== me;
  }
}

function state(): FormState {
  const total = parseAmount(totalField.value, currency()) ?? 0;
  const paid = new Map<number, number>();
  const percents = new Map<number, string>();
  const exact = new Map<number, number>();
  const chosen: number[] = [];
  let payee = 0;
  for (const row of rows) {
    const value = parseAmount(row.paid.value, currency());
    if (value && value > 0) paid.set(row.member.user_id, value);
    if (row.include.checked) chosen.push(row.member.user_id);
    percents.set(row.member.user_id, row.percent.value);
    const shareValue = parseAmount(row.share.value, currency());
    if (shareValue !== null) exact.set(row.member.user_id, shareValue);
    if (row.payee.checked) payee = row.member.user_id;
  }
  return {
    mode: mode(), totalMinor: total, members: members.map((m) => m.user_id),
    paid, chosen, percents, exact, me, payee,
  };
}

// recompute is what makes the form honest: the Shares are derived here, on
// every keystroke, by exactly the rule the ledger reads them with — so what a
// person sees before saving is what everybody sees afterwards.
function recompute(): void {
  const st = state();
  const code = currency();
  const priority = payerOrder(st);
  if (st.mode === "even" || st.mode === "percent") {
    const amounts = st.mode === "even"
      ? allocateEven(st.totalMinor, st.chosen, priority)
      : allocateByPercent(st.totalMinor, st.chosen, st.percents, priority);
    const byMember = new Map<number, number>();
    st.chosen.forEach((id, i) => byMember.set(id, amounts ? amounts[i] : 0));
    for (const row of rows) {
      row.computed.textContent = formatMinor(byMember.get(row.member.user_id) ?? 0, code);
    }
  }
  const result = buildDraft(st);
  if (result.error) {
    setText(totalsLine, draftError(result.error));
    return;
  }
  const shares = result.draft?.shares ?? [];
  const claimed = shares.reduce((sum, e) => sum + e.minor, 0);
  const left = st.totalMinor - claimed;
  setText(totalsLine, left > 0
    ? S.page.transaction.unclaimedNote(`${formatMinor(left, code)} ${code}`)
    : S.page.transaction.fullyClaimed());
}

function draftError(error: string): string {
  switch (error) {
    case "no_payer":
      return S.page.transaction.needPayer();
    case "payments_mismatch":
      return S.page.transaction.needPaymentsMatch();
    case "shares_overdraw":
      return S.page.transaction.needSharesFit();
    case "bad_percent":
      return S.page.transaction.needPercents();
    case "no_members":
      return S.page.transaction.needMembers();
    default:
      return S.page.transaction.needPayee();
  }
}

async function save(): Promise<void> {
  setText(message, "");
  const st = state();
  const result = buildDraft(st);
  if (!result.draft) {
    setText(message, draftError(result.error ?? ""));
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
    const image = el("img", "photo-thumb");
    image.src = photo.url;
    image.alt = S.page.transaction.photoAlt();
    image.width = photo.width;
    image.height = photo.height;
    const link = el("a");
    link.href = photo.url;
    link.target = "_blank";
    link.rel = "noopener";
    link.append(image);
    strip.append(link);
  }
  photos.append(strip);
  show(photosEmpty, list.length === 0);
}

function renderHistory(entries: HistoryDTO[]): void {
  clear(historyList);
  for (const entry of entries) {
    const row = el("li", "list-row");
    row.append(el("span", "list-row-title split-name", entry.actor || S.page.history.somebody()));
    row.append(el("span", "muted", historyVerb(entry.kind)));
    row.append(el("span", "u-spacer"));
    row.append(el("span", "muted", entry.at));
    historyList.append(row);
    const diff = describe(entry);
    if (diff) {
      const detail = el("li", "list-row");
      detail.append(el("span", "muted", diff));
      historyList.append(detail);
    }
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
  return parts.join("; ");
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
