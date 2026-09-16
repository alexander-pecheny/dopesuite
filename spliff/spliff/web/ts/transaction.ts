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
import { amountPlain, byId, clear, el, group as rowGroup, maybe, setText, show, stamp } from "./dom";
import { formatMinor, parseAmount } from "./money";
import {
  allocateByPercent,
  allocateEven,
  buildDraft,
  paidLeft,
  payerOrder,
  singlePayer,
  type FormState,
  type Mode,
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
const currencyField = byId<HTMLSelectElement>("currency");
const totalField = byId<HTMLInputElement>("total");
const rateLine = byId("rateLine");
const modeField = byId<HTMLSelectElement>("mode");
const modeHint = byId("modeHint");
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

// A change of mode changes what each block IS, so both are rebuilt; what was
// typed lives outside them and comes back.
modeField.addEventListener("change", () => {
  setText(modeHint, modeHints()[mode()]);
  renderBody();
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

function modeHints(): Record<Mode, string> {
  return {
    simple: S.page.transaction.hintSimple(),
    even: S.page.transaction.hintEven(),
    percent: S.page.transaction.hintPercent(),
    exact: S.page.transaction.hintExact(),
    claim: S.page.transaction.hintClaim(),
    settlement: S.page.transaction.hintSettlement(),
  };
}

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
      setText(txCrumb, S.page.transaction.newTitle());
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
    setText(modeHint, modeHints()[mode()]);
    renderBody();
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
  // A Transaction that exists is read back in the mode that states it exactly:
  // the amounts are what is stored, so "by exact amounts" is the truthful one.
  modeField.value = "exact";
  show(photoSection, true);
  show(historySection, true);
  renderPhotos(tx.photos);
  renderHistory(view.history);
  prefill(tx.payments, tx.shares);
}

// prefill seeds what was typed from what is stored, in every shape the mode
// select can ask for it back: the amounts as text, who the sole payer and the
// sole beneficiary are when there is one of each, and who already holds a
// Share — so switching to "split evenly" starts from the people who are in it.
function prefill(payments: { member_id: number; minor: number }[], shares: { member_id: number; minor: number }[]): void {
  const code = currency();
  for (const entry of payments) paidText.set(entry.member_id, formatMinor(entry.minor, code));
  for (const entry of shares) {
    shareText.set(entry.member_id, formatMinor(entry.minor, code));
    if (entry.minor > 0) chosen.add(entry.member_id);
  }
  if (payments.length === 1) payer = payments[0].member_id;
  if (shares.length === 1) payee = shares[0].member_id;
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

// ── the two blocks ─────────────────────────────────────────────────────────
//
// A bill answers two questions, and the editor asks them in that order: who
// handed the money over, and who it was for. The mode decides the SHAPE of each
// answer — a picker, a column of amounts, ticks with the amounts derived — and
// the model (txform.ts) decides what that shape means. Nothing below knows how
// a split is computed; it only draws what the model says and hands back what
// was typed.
//
// What was typed is kept HERE, as text, and survives a change of mode: a person
// who fills in three shares and then switches to percentages has not thrown
// away three shares.

const paidText = new Map<number, string>();
const shareText = new Map<number, string>();
const percentText = new Map<number, string>();
const chosen = new Set<number>();
let payer = 0;
let payee = 0;

interface PayerRow {
  member: MemberDTO;
  pick?: HTMLInputElement;
  input?: HTMLInputElement;
  whole?: HTMLElement;
}

interface ShareRow {
  member: MemberDTO;
  tick?: HTMLInputElement;
  pick?: HTMLInputElement;
  percent?: HTMLInputElement;
  input?: HTMLInputElement;
  derived?: HTMLElement;
}

let payerRows: PayerRow[] = [];
let shareRows: ShareRow[] = [];

function mode(): Mode {
  return modeField.value as Mode;
}

function currency(): string {
  return currencyField.value || baseCurrency;
}

// card is the kit's block of content, laid out as a row that wraps: a name, and
// whatever the mode asks about that person. It wraps rather than eliding so a
// long name takes a second line on a phone instead of squeezing the field off.
function card(): HTMLElement {
  return el("div", "card u-row u-wrap u-align-center u-gap-sm");
}

// named is the row's left-hand side: the person the card is about, optionally
// with the tick or the dot that picks them. The control lives inside the label
// so the whole name is the tap target — at the table, on a phone.
function named(member: MemberDTO, control?: HTMLInputElement): HTMLElement {
  const name = el("label", "split-name u-grow");
  if (control) name.append(control, document.createTextNode(" "));
  name.append(document.createTextNode(member.name));
  return name;
}

function amountInput(value: string, placeholder = "0"): HTMLInputElement {
  const input = el("input", "input");
  input.inputMode = "decimal";
  input.placeholder = placeholder;
  input.value = value;
  return input;
}

// captioned is the kit's field: a small caption over the control it names, so a
// column of them needs no header row to fall apart on a phone.
function captioned(caption: string, control: HTMLElement): HTMLElement {
  const field = el("label", "field amount-field");
  field.append(el("span", undefined, caption), control);
  return field;
}

function mark(node: HTMLElement, role: string, member: MemberDTO): void {
  node.dataset.role = role;
  node.dataset.member = String(member.id);
}

function tickbox(kind: "checkbox" | "radio", name: string, on: boolean): HTMLInputElement {
  const input = el("input");
  input.type = kind;
  if (kind === "radio") input.name = name;
  input.checked = on;
  return input;
}

// Who paid. In the two modes that ARE one person handing the whole amount over
// it is a picker and the chosen card says the total; everywhere else it is an
// amount per Member, and the line under the block says what is still not
// accounted for.
function renderPayers(): void {
  clear(payerBody);
  payerRows = [];
  const picker = singlePayer(mode());
  for (const member of members) {
    const row = card();
    const entry: PayerRow = { member };
    if (picker) {
      const pick = tickbox("radio", "payer", payer === member.id);
      pick.addEventListener("change", () => {
        payer = member.id;
        recompute();
      });
      mark(pick, "pick-payer", member);
      entry.pick = pick;
      entry.whole = amountPlain("");
      row.append(named(member, pick), entry.whole);
    } else {
      const input = amountInput(paidText.get(member.id) ?? "");
      input.addEventListener("input", () => {
        paidText.set(member.id, input.value);
        recompute();
      });
      mark(input, "paid", member);
      entry.input = input;
      row.append(named(member), captioned(S.page.transaction.colPaid(), input));
    }
    payerBody.append(row);
    payerRows.push(entry);
  }
}

// For whom. Five shapes, one per mode: pick one, tick several and read the
// split off, tick several and give each a percentage, type every share, or —
// claiming — type your own and leave everybody else's alone.
function renderShares(): void {
  clear(shareBody);
  shareRows = [];
  const m = mode();
  for (const member of members) {
    const row = card();
    const entry: ShareRow = { member };
    const id = member.id;
    switch (m) {
      case "simple":
      case "settlement": {
        const pick = tickbox("radio", "payee", payee === id);
        pick.addEventListener("change", () => {
          payee = id;
          recompute();
        });
        mark(pick, "pick-payee", member);
        entry.pick = pick;
        entry.derived = amountPlain("");
        row.append(named(member, pick), entry.derived);
        break;
      }
      case "even":
      case "percent": {
        const tick = tickbox("checkbox", "", chosen.has(id));
        tick.addEventListener("change", () => {
          if (tick.checked) chosen.add(id);
          else chosen.delete(id);
          recompute();
        });
        mark(tick, "tick", member);
        entry.tick = tick;
        row.append(named(member, tick));
        if (m === "percent") {
          const percent = amountInput(percentText.get(id) ?? "", "%");
          percent.addEventListener("input", () => {
            percentText.set(id, percent.value);
            recompute();
          });
          mark(percent, "percent", member);
          entry.percent = percent;
          row.append(captioned(S.page.transaction.colPercent(), percent));
        }
        // The split is DERIVED, so it is read back rather than typed: what a
        // person sees before saving is what the ledger will read afterwards.
        entry.derived = amountPlain("");
        row.append(captioned(S.page.transaction.colShare(), entry.derived));
        break;
      }
      case "exact": {
        const input = amountInput(shareText.get(id) ?? "");
        input.addEventListener("input", () => {
          shareText.set(id, input.value);
          recompute();
        });
        mark(input, "share", member);
        entry.input = input;
        row.append(named(member), captioned(S.page.transaction.colShare(), input));
        break;
      }
      case "claim": {
        // Only your own share is yours to set. Everybody else's stands as it
        // is — that is what makes a claim a claim and not a rewrite — and what
        // nobody has claimed stays Unclaimed.
        if (id === me) {
          const input = amountInput(shareText.get(id) ?? "");
          input.addEventListener("input", () => {
            shareText.set(id, input.value);
            recompute();
          });
          mark(input, "share", member);
          entry.input = input;
          row.append(named(member), captioned(S.page.transaction.colShare(), input));
        } else {
          entry.derived = amountPlain("");
          row.append(named(member), captioned(S.page.transaction.colShare(), entry.derived));
        }
        break;
      }
    }
    shareBody.append(row);
    shareRows.push(entry);
  }
}

function renderBody(): void {
  renderPayers();
  renderShares();
  recompute();
}

function state(): FormState {
  const code = currency();
  const total = parseAmount(totalField.value, code) ?? 0;
  const paid = new Map<number, number>();
  const exact = new Map<number, number>();
  for (const member of members) {
    const id = member.id;
    const value = parseAmount(paidText.get(id) ?? "", code);
    if (value && value > 0) paid.set(id, value);
    const shareValue = parseAmount(shareText.get(id) ?? "", code);
    if (shareValue !== null) exact.set(id, shareValue);
  }
  return {
    mode: mode(),
    totalMinor: total,
    members: members.map((m) => m.id),
    paid,
    chosen: members.map((m) => m.id).filter((id) => chosen.has(id)),
    percents: new Map(members.map((m) => [m.id, percentText.get(m.id) ?? ""])),
    exact,
    me,
    payee,
    payer,
  };
}

// recompute is what makes the form honest: every derived number is computed
// here, on every keystroke, by exactly the rule the ledger reads them with.
function recompute(): void {
  const st = state();
  const code = currency();
  const priority = payerOrder(st);

  for (const row of payerRows) {
    if (!row.whole) continue;
    row.whole.textContent = payer === row.member.id ? formatMinor(st.totalMinor, code) : "";
  }

  const derived = new Map<number, number>();
  if (st.mode === "even" || st.mode === "percent") {
    const amounts = st.mode === "even"
      ? allocateEven(st.totalMinor, st.chosen, priority)
      : allocateByPercent(st.totalMinor, st.chosen, st.percents, priority);
    st.chosen.forEach((id, i) => derived.set(id, amounts ? amounts[i] : 0));
  }
  for (const row of shareRows) {
    if (!row.derived) continue;
    const id = row.member.id;
    if (st.mode === "simple" || st.mode === "settlement") {
      row.derived.textContent = payee === id ? formatMinor(st.totalMinor, code) : "";
      continue;
    }
    if (st.mode === "claim") {
      row.derived.textContent = formatMinor(st.exact.get(id) ?? 0, code);
      continue;
    }
    row.derived.textContent = formatMinor(derived.get(id) ?? 0, code);
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
