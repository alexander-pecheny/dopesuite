// The Groups list: every circle of people you share expenses with, and what
// each of them makes of you. The balance is the point of the row — the name
// alone is a list of holidays.

import S from "./i18nstrings.js";
import { errorText, get, request, type GroupSummaryDTO } from "./api";
import { amountNode, byId, clear, el, setText, show, tag } from "./dom";

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
let currencies: string[] | null = null;

async function fillCurrencies(): Promise<void> {
  if (!currencies) {
    try {
      currencies = (await get<string[]>("/api/currencies")).sort();
    } catch {
      currencies = [];
    }
  }
  clear(currencyField);
  for (const code of currencies) {
    const option = el("option", undefined, code);
    option.value = code;
    if (code === "EUR") option.selected = true;
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
  for (const group of groups) {
    const row = el("li", "list-row");
    const link = el("a", "list-row-title", group.name);
    link.href = `/group/${group.id}`;
    row.append(link);
    if (group.is_owner) row.append(tag(S.page.group.ownerTag()));
    row.append(el("span", "u-spacer"));
    if (group.balance_minor === 0) {
      row.append(el("span", "muted", S.page.index.settled()));
    } else {
      row.append(amountNode(`${group.balance} ${group.base_currency}`, group.balance_minor));
    }
    list.append(row);
  }
}

void load();
