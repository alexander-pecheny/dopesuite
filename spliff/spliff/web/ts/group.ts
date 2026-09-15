// The Group page. Balances and the Debt graph first, because at the table
// "what do I owe" is the whole question; the feed of Transactions under them is
// the evidence for the answer.

import S from "./i18nstrings.js";
import {
  errorText,
  get,
  request,
  type GroupDTO,
  type HistoryDTO,
  type InviteDTO,
  type TransactionDTO,
} from "./api";
import { amountNode, byId, clear, el, maybe, setText, show, tag } from "./dom";

const groupID = Number(window.location.pathname.split("/")[2] ?? 0);

const title = maybe("groupTitle");
const crumb = maybe("groupCrumb");
const balances = byId("balances");
const transfers = byId("transfers");
const settledNote = byId("settledNote");
const ratesNote = byId("ratesNote");
const feed = byId("feed");
const feedEmpty = byId("feedEmpty");
const membersList = byId("members");
const inviteSection = byId("inviteSection");
const invitesList = byId("invites");
const deletedFeed = byId("deletedFeed");
const groupHistory = byId("groupHistory");
const message = byId("message");

const settingsOverlay = byId("settingsOverlay");
const settingsForm = byId<HTMLFormElement>("settingsForm");
const nameField = byId<HTMLInputElement>("groupNameField");
const currencyField = byId<HTMLSelectElement>("groupCurrencyField");
const ownerControls = byId("ownerControls");
const handOverSelect = byId<HTMLSelectElement>("handOverSelect");
const settingsMessage = byId("settingsMessage");

const inviteOverlay = byId("inviteOverlay");
const inviteForm = byId<HTMLFormElement>("inviteForm");
const inviteLabel = byId<HTMLInputElement>("inviteLabel");
const inviteMaxUses = byId<HTMLInputElement>("inviteMaxUses");
const inviteTTL = byId<HTMLInputElement>("inviteTTL");
const inviteApproval = byId<HTMLInputElement>("inviteApproval");
const inviteMessage = byId("inviteMessage");

let group: GroupDTO | null = null;

byId("addBtn").addEventListener("click", () => {
  window.location.href = `/group/${groupID}/new`;
});

byId("settingsBtn").addEventListener("click", () => {
  if (!group) return;
  setText(settingsMessage, "");
  nameField.value = group.name;
  void fillCurrencies(group.base_currency);
  fillHandOver();
  show(ownerControls, group.is_owner);
  show(settingsOverlay, true);
});

byId("settingsCancel").addEventListener("click", () => show(settingsOverlay, false));

settingsForm.addEventListener("submit", (event) => {
  event.preventDefault();
  void patch({ name: nameField.value, base_currency: currencyField.value });
});

byId("handOverBtn").addEventListener("click", () => {
  const to = Number(handOverSelect.value);
  if (!to) return;
  void act(`POST`, `/api/groups/${groupID}/owner`, { user_id: to }, settingsMessage);
});

byId("deleteGroupBtn").addEventListener("click", () => {
  if (!window.confirm(S.page.group.confirmDelete())) return;
  void leaveFor("DELETE", `/api/groups/${groupID}`);
});

byId("leaveBtn").addEventListener("click", () => {
  if (!window.confirm(S.page.group.confirmLeave())) return;
  void leaveFor("DELETE", `/api/groups/${groupID}/members/me`);
});

byId("newInviteBtn").addEventListener("click", () => {
  setText(inviteMessage, "");
  inviteForm.reset();
  show(inviteOverlay, true);
});

byId("inviteCancel").addEventListener("click", () => show(inviteOverlay, false));

inviteForm.addEventListener("submit", (event) => {
  event.preventDefault();
  void mintInvite();
});

async function patch(body: Record<string, unknown>): Promise<void> {
  setText(settingsMessage, "");
  try {
    await request("PATCH", `/api/groups/${groupID}`, body);
    show(settingsOverlay, false);
    await load();
  } catch (error) {
    setText(settingsMessage, errorText(error));
  }
}

async function act(method: string, url: string, body: unknown, into: HTMLElement): Promise<void> {
  setText(into, "");
  try {
    await request(method, url, body);
    show(settingsOverlay, false);
    show(inviteOverlay, false);
    await load();
  } catch (error) {
    setText(into, errorText(error));
  }
}

async function leaveFor(method: string, url: string): Promise<void> {
  setText(settingsMessage, "");
  try {
    await request(method, url);
    window.location.href = "/";
  } catch (error) {
    setText(settingsMessage, errorText(error));
  }
}

async function mintInvite(): Promise<void> {
  await act("POST", `/api/groups/${groupID}/invites`, {
    label: inviteLabel.value,
    max_uses: Number(inviteMaxUses.value || 0),
    ttl_hours: Number(inviteTTL.value || 0),
    requires_approval: inviteApproval.checked,
  }, inviteMessage);
}

let currencies: string[] | null = null;

async function fillCurrencies(selected: string): Promise<void> {
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
    option.selected = code === selected;
    currencyField.append(option);
  }
}

function fillHandOver(): void {
  clear(handOverSelect);
  if (!group) return;
  for (const member of group.members) {
    if (member.user_id === group.me) continue;
    const option = el("option", undefined, member.name);
    option.value = String(member.user_id);
    handOverSelect.append(option);
  }
}

async function load(): Promise<void> {
  try {
    group = await get<GroupDTO>(`/api/groups/${groupID}`);
    render(group);
    if (group.is_owner) await loadInvites();
  } catch (error) {
    setText(message, errorText(error));
  }
}

function render(g: GroupDTO): void {
  document.title = g.name;
  setText(title, g.name);
  setText(crumb, g.name);
  show(ratesNote, g.no_rates);

  clear(balances);
  for (const member of g.members) {
    const row = el("li", "list-row");
    const name = el("span", "list-row-title split-name", member.name);
    row.append(name);
    if (member.is_owner) row.append(tag(S.page.group.ownerTag()));
    if (member.user_id === g.me) row.append(tag(S.page.group.you()));
    row.append(el("span", "u-spacer"));
    row.append(amountNode(`${member.balance} ${g.base_currency}`, member.balance_minor));
    balances.append(row);
  }

  clear(transfers);
  for (const transfer of g.transfers) {
    const row = el("li", "list-row");
    row.append(el("span", "split-name", S.page.group.transfer(transfer.from_name, transfer.to_name)));
    row.append(el("span", "u-spacer"));
    row.append(el("span", "amount", `${transfer.amount} ${g.base_currency}`));
    transfers.append(row);
  }
  show(settledNote, g.transfers.length === 0 && !g.no_rates);

  renderFeed(feed, g.live, g);
  show(feedEmpty, g.live.length === 0);
  renderFeed(deletedFeed, g.deleted, g);
  show(byId("deletedBlock"), g.deleted.length > 0);

  renderMembers(g);
  renderHistory(groupHistory, g.history);
  show(inviteSection, g.is_owner);
}

function renderFeed(into: HTMLElement, list: TransactionDTO[], g: GroupDTO): void {
  clear(into);
  for (const tx of list) {
    const row = el("li", "list-row");
    const link = el("a", "list-row-title split-name", tx.description);
    link.href = `/transaction/${tx.id}`;
    row.append(link);
    row.append(el("span", "muted", tx.day));
    if (tx.unclaimed_minor > 0) {
      row.append(tag(S.page.group.unclaimed(`${tx.unclaimed} ${tx.currency}`)));
    }
    row.append(el("span", "u-spacer"));
    const amount = el("span", "amount", `${tx.total} ${tx.currency}`);
    row.append(amount);
    if (tx.in_base && tx.currency !== g.base_currency) {
      row.append(el("span", "muted", `= ${tx.in_base} ${g.base_currency}`));
    }
    into.append(row);
  }
}

function renderMembers(g: GroupDTO): void {
  clear(membersList);
  for (const member of g.members) {
    const row = el("li", "list-row");
    row.append(el("span", "list-row-title split-name", member.name));
    if (member.is_owner) row.append(tag(S.page.group.ownerTag()));
    row.append(el("span", "u-spacer"));
    if (g.is_owner && !member.is_owner) {
      const kick = el("button", "btn btn-ghost", S.page.group.kick());
      kick.type = "button";
      kick.addEventListener("click", () => {
        if (!window.confirm(S.page.group.confirmKick(member.name))) return;
        void act("DELETE", `/api/groups/${groupID}/members/${member.user_id}`, undefined, message);
      });
      row.append(kick);
    }
    membersList.append(row);
  }
}

function renderHistory(into: HTMLElement, entries: HistoryDTO[]): void {
  clear(into);
  for (const entry of entries) {
    const row = el("li", "list-row");
    row.append(el("span", "split-name", `${entry.actor} — ${historyVerb(entry.kind)}`));
    if (entry.description) {
      const link = el("a", "list-row-title split-name", entry.description);
      link.href = `/transaction/${entry.transaction_id}`;
      row.append(link);
    }
    row.append(el("span", "u-spacer"));
    row.append(el("span", "muted", entry.at));
    into.append(row);
  }
}

export function historyVerb(kind: string): string {
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

async function loadInvites(): Promise<void> {
  try {
    const invites = await get<InviteDTO[]>(`/api/groups/${groupID}/invites`);
    renderInvites(invites);
  } catch (error) {
    setText(message, errorText(error));
  }
}

function renderInvites(invites: InviteDTO[]): void {
  clear(invitesList);
  for (const invite of invites) {
    const row = el("li", "list-row");
    const head = el("span", "list-row-title split-name", invite.label || invite.code);
    row.append(head);
    row.append(tag(inviteState(invite.state), invite.state !== "active"));
    row.append(el("span", "invite-code", invite.url));
    row.append(el("span", "u-spacer"));

    const copy = el("button", "btn btn-ghost", S.page.invite.copy());
    copy.type = "button";
    copy.addEventListener("click", () => {
      void navigator.clipboard.writeText(invite.url).then(() => {
        copy.textContent = S.page.invite.copied();
      }).catch(() => undefined);
    });
    row.append(copy);

    if (invite.state === "active") {
      const revoke = el("button", "btn btn-ghost", S.page.invite.revoke());
      revoke.type = "button";
      revoke.addEventListener("click", () => {
        void act("POST", `/api/invites/${invite.id}/revoke`, undefined, message);
      });
      row.append(revoke);
    }
    const drop = el("button", "btn btn-danger", S.page.invite.remove());
    drop.type = "button";
    drop.addEventListener("click", () => {
      void act("DELETE", `/api/invites/${invite.id}`, undefined, message);
    });
    row.append(drop);
    invitesList.append(row);

    for (const person of invite.pending) {
      const waiting = el("li", "list-row");
      waiting.append(el("span", "split-name", person.name));
      waiting.append(tag(S.page.invite.waiting()));
      waiting.append(el("span", "u-spacer"));
      for (const decision of ["approve", "decline"] as const) {
        const button = el("button", decision === "approve" ? "btn" : "btn btn-ghost",
          decision === "approve" ? S.page.invite.approve() : S.page.invite.decline());
        button.type = "button";
        button.addEventListener("click", () => {
          void act("POST", `/api/groups/${groupID}/join-requests/${person.user_id}`, { decision }, message);
        });
        waiting.append(button);
      }
      invitesList.append(waiting);
    }
  }
}

export function inviteState(state: string): string {
  switch (state) {
    case "active":
      return S.page.invite.stateActive();
    case "revoked":
      return S.page.invite.stateRevoked();
    case "expired":
      return S.page.invite.stateExpired();
    case "exhausted":
      return S.page.invite.stateExhausted();
    default:
      return state;
  }
}

void load();
