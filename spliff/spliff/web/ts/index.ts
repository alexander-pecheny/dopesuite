// The Groups list: every circle of people you share expenses with, and what
// each of them makes of you. The balance is the point of the row — the name
// alone is a list of holidays.

import S from "./i18nstrings.js";
import { errorText, get, request, type CurrencyDTO, type GroupSummaryDTO } from "./api";
import { amountNode, badge, byId, clear, el, group as rowGroup, setText, show } from "./dom";

const list = byId("groupList");
const emptyNote = byId("groupsEmpty");
const message = byId("message");
const overlay = byId("createOverlay");
const form = byId<HTMLFormElement>("createForm");
const nameField = byId<HTMLInputElement>("groupName");
const currencyField = byId<HTMLSelectElement>("groupCurrency");
const createMessage = byId("createMessage");

byId("newGroupBtn").addEventListener("click", () => {
  setText(createMessage, "");
  form.reset();
  void fillCurrencies();
  show(overlay, true);
  nameField.focus();
});

byId("createCancel").addEventListener("click", () => show(overlay, false));

form.addEventListener("submit", (event) => {
  event.preventDefault();
  void create();
});

async function create(): Promise<void> {
  setText(createMessage, "");
  try {
    const created = await request<{ id: number }>("POST", "/api/groups", {
      name: nameField.value,
      base_currency: currencyField.value,
    });
    window.location.href = `/group/${created.id}`;
  } catch (error) {
    setText(createMessage, errorText(error));
  }
}

// The currencies are the ones the newest Rate table actually carries, asked of
// the server rather than listed here: a list of our own would go stale the day
// the source adds one.
let currencies: CurrencyDTO[] | null = null;

async function fillCurrencies(): Promise<void> {
  if (!currencies) {
    try {
      currencies = await get<CurrencyDTO[]>("/api/currencies");
    } catch {
      currencies = [];
    }
  }
  clear(currencyField);
  for (const currency of currencies) {
    const option = el("option", undefined, currency.code);
    option.value = currency.code;
    if (currency.code === "EUR") option.selected = true;
    currencyField.append(option);
  }
}

async function load(): Promise<void> {
  try {
    const groups = await get<GroupSummaryDTO[]>("/api/groups");
    render(groups);
  } catch (error) {
    setText(message, errorText(error));
  }
}

function render(groups: GroupSummaryDTO[]): void {
  clear(list);
  show(emptyNote, groups.length === 0);
  // The whole card is the link, not a word inside it: on a phone that is the
  // difference between opening a Group and missing it.
  for (const group of groups) {
    const item = el("li");
    const row = el("a", "list-row");
    row.href = `/group/${group.id}`;
    const left = rowGroup(true);
    left.append(el("span", "list-row-title split-name", group.name));
    if (group.is_owner) left.append(badge(S.page.group.ownerTag()));
    row.append(left);
    if (group.balance_minor === 0) {
      row.append(el("span", "muted", S.page.index.settled()));
    } else {
      row.append(amountNode(`${group.balance} ${group.base_currency}`, group.balance_minor));
    }
    item.append(row);
    list.append(item);
  }
}

void load();
