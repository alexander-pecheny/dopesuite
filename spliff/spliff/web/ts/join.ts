// The Invite Link landing. An anonymous visitor sees the Group's name and a
// log-in button that brings them back here; somebody logged in sees what the
// link does for them and, when it still works, one button that joins.

import S from "./i18nstrings.js";
import { errorText, get, request, type InvitePeekDTO } from "./api";
import { byId, clear, el, setText, show } from "./dom";

const code = window.location.pathname.split("/")[2] ?? "";
const body = byId("joinBody");
const message = byId("joinMessage");
const joinBtn = byId<HTMLButtonElement>("joinBtn");
const loginBtn = byId<HTMLButtonElement>("loginBtn");
const openBtn = byId<HTMLButtonElement>("openBtn");

let peek: InvitePeekDTO | null = null;
// Which seat the joiner says is theirs: 0 is "I am myself", anything else is
// the member row of a Phantom whose Payments, Shares and History become theirs.
let claim = 0;

loginBtn.addEventListener("click", () => {
  window.location.href = `/login?next=${encodeURIComponent(`/join/${code}`)}`;
});

openBtn.addEventListener("click", () => {
  if (peek) window.location.href = `/group/${peek.group_id}`;
});

joinBtn.addEventListener("click", () => {
  void join();
});

async function join(): Promise<void> {
  setText(message, "");
  try {
    const result = await request<{ group_id: number; state: string }>(
      "POST", `/api/invites/code/${code}/join`, { claim });
    if (result.state === "member") {
      window.location.href = `/group/${result.group_id}`;
      return;
    }
    show(joinBtn, false);
    setText(message, S.page.join.pending());
  } catch (error) {
    setText(message, errorText(error));
  }
}

async function load(): Promise<void> {
  // Logged in or not decides which peek answers: the public one knows only the
  // Group's name, the other knows what the link does for YOU.
  let loggedIn = true;
  try {
    await get("/api/auth/me");
  } catch {
    loggedIn = false;
  }
  try {
    peek = await get<InvitePeekDTO>(
      loggedIn ? `/api/invites/code/${code}` : `/api/invites/code/${code}/public`);
  } catch (error) {
    setText(message, errorText(error));
    return;
  }
  render(peek, loggedIn);
}

// whoAmI is the picker the Phantoms make possible: join as yourself, or say
// you are one of the names already at this table and take everything it holds.
function whoAmI(link: InvitePeekDTO): void {
  const phantoms = link.phantoms ?? [];
  if (phantoms.length === 0) return;
  claim = 0;
  body.append(el("p", "hint", S.page.join.pickWho()));
  const options: Array<{ id: number; label: string }> = [
    { id: 0, label: S.page.join.asYourself() },
    ...phantoms.map((p) => ({ id: p.id, label: S.page.join.iAm(p.name) })),
  ];
  for (const option of options) {
    const card = el("div", "card u-row u-wrap u-align-center u-gap-sm");
    const pick = el("input");
    pick.type = "radio";
    pick.name = "claim";
    pick.value = String(option.id);
    pick.checked = option.id === claim;
    pick.dataset.claim = String(option.id);
    pick.addEventListener("change", () => {
      claim = option.id;
    });
    const label = el("label", "split-name u-grow");
    label.append(pick, document.createTextNode(` ${option.label}`));
    card.append(label);
    body.append(card);
  }
}

function render(link: InvitePeekDTO, loggedIn: boolean): void {
  clear(body);
  body.append(el("h2", "subhead", link.group_name || S.page.join.unnamed()));
  if (!loggedIn) {
    body.append(el("p", "hint", S.page.join.anonymous()));
    show(loginBtn, true);
    return;
  }
  switch (link.state) {
    case "active":
      body.append(el("p", "hint",
        link.requires_approval ? S.page.join.needsApproval() : S.page.join.invited()));
      // A link that waits for the owner's approval cannot hand over a Phantom:
      // the decision comes later, and there is nowhere to keep what was claimed.
      if (!link.requires_approval) whoAmI(link);
      show(joinBtn, true);
      return;
    case "member":
      body.append(el("p", "hint", S.page.join.alreadyMember()));
      show(openBtn, true);
      return;
    case "pending":
      body.append(el("p", "hint", S.page.join.pending()));
      return;
    default:
      body.append(el("p", "hint hint-danger", S.page.join.dead()));
  }
}

void load();
