// appealcard.ts — an appeal Card as the card editor shows it (ADR-0021): the
// packet's question, the appeal, the controversial answers on that question,
// the votes and the verdict. The preview reads it; the fields view edits the verdict, and its
// reader composes the Card's JSON back for the editor's draft and Save. A vote
// is written the moment it is cast, outside the draft, because it is the
// member's own and not the Card's.

import S from "./i18nstrings.js";
import { xyApp } from "./app.js";
import { iconed } from "./icons_gen.js";
import { composeAppeal, tally, verdictStatus, type Appeal, type Status, type Vote, type VoteValue } from "./appeals.js";

const { el } = xyApp;

export interface AppealCardDeps {
  votes(): Vote[];
  // Who is who, for the names under the votes.
  memberName(userId: number): string;
  myId(): number | null;
  // The packet's question N, rendered, or a line saying why there is none.
  question(cardListId: number, n: number): HTMLElement;
  vote(cardId: number, value: VoteValue | null): Promise<void>;
}

export interface AppealCard {
  view(cardId: number, listId: number, a: Appeal): HTMLElement;
  editor(cardId: number, a: Appeal, onInput: () => void): { node: HTMLElement; read(): string };
}

// heading is the appeal's one-line name: the board card and the editor's
// title both use it.
export function appealHeading(a: Appeal): string {
  const what = a.type === "A" ? S.appeals.kind.accept(a.answer)
    : a.type === "R" ? S.appeals.kind.remove()
      : a.type === "N" ? S.appeals.kind.hostError()
        : a.type;
  return S.appeals.card.heading(String(a.question), what);
}

export function statusText(st: string): string {
  if (st === "A") return S.appeals.status.accepted();
  if (st === "D") return S.appeals.status.declined();
  return S.appeals.status.open();
}

function controversialText(st: string): string {
  if (st === "A") return S.appeals.controversial.accepted();
  if (st === "D") return S.appeals.controversial.declined();
  return S.appeals.controversial.open();
}

// verdictLines is the verdict as text, for the timeline's edit diff.
export function verdictLines(a: Appeal): string {
  return `${a.verdict.status ? statusText(a.verdict.status) : S.appeals.verdict.followVotes()}\n\n${a.verdict.text}`;
}

const tone = (st: string): string => (st === "A" ? " badge-positive" : st === "D" ? " badge-negative" : "");

function block(label: string, ...body: Node[]): HTMLElement {
  return el("div", { class: "fld fld-wide" },
    el("div", { class: "fld-head" }, el("span", { class: "fld-label", text: label })),
    el("div", { class: "fld-body u-col u-gap-sm" }, ...body));
}

const prose = (text: string): HTMLElement => el("div", { class: "pv-field", text });

export function createAppealCard(deps: AppealCardDeps): AppealCard {
  function controversials(a: Appeal): HTMLElement {
    if (!a.controversials.length) return block(S.appeals.card.controversials(String(a.question)), el("p", { class: "hint", text: S.appeals.card.noControversials() }));
    const rows = a.controversials.map((c) => {
      const row = el("div", { class: "u-row u-gap-sm u-align-center" },
        el("span", { class: "badge" + tone(c.status), text: controversialText(c.status) }),
        el("span", { class: "u-grow", text: c.answer }));
      if (c.id === a.controversial) row.append(el("span", { class: "badge badge-emphasis", text: S.appeals.card.appealed() }));
      return row;
    });
    return block(S.appeals.card.controversials(String(a.question)), ...rows);
  }

  // votes is the voting row: the two buttons, the count, and who voted how.
  function votes(cardId: number, redraw: () => void): HTMLElement {
    const all = deps.votes().filter((v) => v.cardId === cardId);
    const me = deps.myId();
    const mine = all.find((v) => v.userId === me)?.value || "";
    const t = tally(all, cardId);
    const button = (value: VoteValue, glyph: "thumbs-up" | "thumbs-down", text: string, n: number): HTMLElement => {
      const b = el("button", { class: "seg-btn" + (mine === value ? " active" : ""), type: "button", "aria-pressed": String(mine === value) },
        ...iconed(glyph, `${text} · ${n}`));
      b.addEventListener("click", () => { void deps.vote(cardId, mine === value ? null : value).then(redraw); });
      return b;
    };
    const names = (value: string): string => all.filter((v) => v.value === value).map((v) => deps.memberName(v.userId)).join(", ");
    const who: HTMLElement[] = [];
    if (t.for) who.push(el("p", { class: "hint", text: S.appeals.vote.forWho(names("for")) }));
    if (t.against) who.push(el("p", { class: "hint", text: S.appeals.vote.againstWho(names("against")) }));
    return block(S.appeals.vote.label(),
      el("div", { class: "u-row" }, el("div", { class: "seg" }, button("for", "thumbs-up", S.appeals.vote.for(), t.for), button("against", "thumbs-down", S.appeals.vote.against(), t.against))),
      ...who);
  }

  // decided is the status line: what the jury decided and how.
  function decided(cardId: number, a: Appeal): HTMLElement {
    const t = tally(deps.votes(), cardId);
    const v = verdictStatus(a, t);
    const how = v.byHand ? S.appeals.verdict.byHand() : v.status ? S.appeals.verdict.byVotes(String(t.for), String(t.against)) : "";
    const row = el("div", { class: "u-row u-gap-sm u-align-center u-wrap" },
      el("span", { class: "badge" + tone(v.status), text: v.status ? statusText(v.status) : S.appeals.verdict.undecided() }));
    if (how) row.append(el("span", { class: "hint", text: how }));
    return row;
  }

  function site(a: Appeal): HTMLElement {
    const line = a.site.overriddenBy
      ? S.appeals.site.overridden(String(a.site.overriddenBy))
      : S.appeals.site.status(statusText(a.site.status));
    return el("p", { class: "hint", text: line });
  }

  function view(cardId: number, listId: number, a: Appeal): HTMLElement {
    const box = el("div", { class: "card-fields" });
    const draw = (): void => {
      const text: Node[] = [prose(a.text)];
      if (a.textChangedAt) text.unshift(el("p", { class: "hint hint-danger", text: S.appeals.card.textChanged(new Date(a.textChangedAt).toLocaleString("ru")) }));
      const verdict: Node[] = [decided(cardId, a), a.verdict.text ? prose(a.verdict.text) : el("p", { class: "hint", text: S.appeals.verdict.noText() }), site(a)];
      box.replaceChildren(
        block(S.appeals.card.question(), deps.question(listId, a.question)),
        block(S.appeals.card.text(), ...text),
        controversials(a),
        votes(cardId, draw),
        block(S.appeals.verdict.label(), ...verdict));
    };
    draw();
    return box;
  }

  function editor(cardId: number, a: Appeal, onInput: () => void): { node: HTMLElement; read(): string } {
    const sel = el("select", { class: "input" },
      el("option", { value: "", text: S.appeals.verdict.followVotes() }),
      el("option", { value: "A", text: S.appeals.status.accepted() }),
      el("option", { value: "D", text: S.appeals.status.declined() })) as HTMLSelectElement;
    sel.value = a.verdict.status;
    const text = el("textarea", { class: "input fld-input", rows: "6", placeholder: S.appeals.verdict.placeholder() }) as HTMLTextAreaElement;
    text.value = a.verdict.text;
    const read = (): Appeal => ({ ...a, verdict: { status: sel.value as Status, text: text.value } });
    const status = el("div");
    const drawStatus = (): void => { status.replaceChildren(decided(cardId, read())); };
    sel.addEventListener("change", () => { drawStatus(); onInput(); });
    text.addEventListener("input", onInput);
    const votesBox = el("div", { class: "fld-wide" });
    const drawVotes = (): void => { votesBox.replaceChildren(votes(cardId, () => { drawVotes(); drawStatus(); })); };
    drawVotes();
    drawStatus();
    const node = el("div", { class: "card-fields" },
      votesBox,
      block(S.appeals.verdict.statusLabel(), sel, status),
      block(S.appeals.verdict.label(), text, site(a)));
    return { node, read: () => composeAppeal(read()) };
  }

  return { view, editor };
}
