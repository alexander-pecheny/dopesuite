// The Groups list: every circle of people you share expenses with, and what
// each of them makes of you. The balance is the point of the row — the name
// alone is a list of holidays.

import S from "./i18nstrings.js";
import { errorText, get, request, type GroupSummaryDTO } from "./api";
import { bindCurrency } from "./currency-field";
import { amountNode, badge, byId, clear, el, group as rowGroup, setText, show } from "./dom";

const list = byId("groupList");
const emptyNote = byId("groupsEmpty");
const message = byId("message");
const overlay = byId("createOverlay");
const form = byId<HTMLFormElement>("createForm");
const nameField = byId<HTMLInputElement>("groupName");
const currency = bindCurrency(byId<HTMLInputElement>("groupCurrency"), byId("groupCurrencyError"));
const createMessage = byId("createMessage");

byId("newGroupBtn").addEventListener("click", () => {
  setText(createMessage, "");
  form.reset();
  // A first Group has no history to open the list on, so the shortlist leads.
  void currency.fill("EUR");
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
  // A code nobody quotes is said here, under the field, and not by a 400.
  if (!currency.ok()) return;
  try {
    const created = await request<{ id: number }>("POST", "/api/groups", {
      name: nameField.value,
      base_currency: currency.value(),
    });
    window.location.href = `/group/${created.id}`;
  } catch (error) {
    setText(createMessage, errorText(error));
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
