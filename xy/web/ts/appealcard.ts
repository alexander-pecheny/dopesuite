// appealcard.ts — an appeal Card as the card editor shows it (ADR-0021): the
// packet's question, the appeal, the controversial answers on that question,
// the votes and the verdict. The preview reads it; the fields view edits the verdict, and its
// reader composes the Card's JSON back for the editor's draft and Save. A vote
// is written the moment it is cast, outside the draft, because it is the
// member's own and not the Card's.

import S from "./i18nstrings.js";
import { xyApp } from "./app.js";
import { iconed } from "./icons_gen.js";
import { linkSegments } from "./timeline.js";
import { composeAppeal, tally, verdictStatus, type Appeal, type AppealFields, type Status, type Vote, type VoteValue } from "./appeals.js";

const { el } = xyApp;

export interface AppealCardDeps {
  votes(): Vote[];
  // Who is who, for the names under the votes.
  memberName(userId: number): string;
  myId(): number | null;
  // The packet's question N, rendered, or a line saying why there is none.
  question(cardListId: number, n: number): HTMLElement;
  vote(cardId: number, value: VoteValue | null): Promise<void>;
  // Whether the List is filled by hand: then there is no site to report on,
  // and the appeal itself stays editable.
  manual(listId: number): boolean;
}

export interface AppealCard {
  view(cardId: number, listId: number, a: Appeal): HTMLElement;
  editor(cardId: number, listId: number, a: Appeal, onInput: () => void): { node: HTMLElement; read(): string };
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

// prose is text from the site as written, with its URLs made clickable.
// appealFields is the form for what an appeal says, on a manual List: the
// panel that adds one and the card editor both use it. read is null while the
// question number or the answer to accept is missing, after showing why.
export function appealFields(f: AppealFields, onInput: () => void, fail: (text: string) => void): { nodes: HTMLElement[]; read(): AppealFields | null } {
  const q = el("input", { class: "input", type: "text", inputmode: "numeric", autocomplete: "off", value: f.question ? String(f.question) : "" }) as HTMLInputElement;
  const type = el("select", { class: "input" },
    el("option", { value: "A", text: S.appeals.manual.typeAccept() }),
    el("option", { value: "R", text: S.appeals.manual.typeRemove() }),
    el("option", { value: "N", text: S.appeals.manual.typeHostError() })) as HTMLSelectElement;
  type.value = f.type || "A";
  const answer = el("input", { class: "input", type: "text", autocomplete: "off", value: f.answer }) as HTMLInputElement;
  const text = el("textarea", { class: "input fld-input", rows: "6" }) as HTMLTextAreaElement;
  text.value = f.text;
  const answerBlock = block(S.appeals.manual.answer(), answer);
  const sync = (): void => { answerBlock.hidden = type.value !== "A"; };
  type.addEventListener("change", () => { sync(); onInput(); });
  for (const n of [q, answer, text]) n.addEventListener("input", onInput);
  sync();
  const read = (): AppealFields | null => {
    const question = Number(q.value.trim());
    if (!Number.isInteger(question) || question < 1) { fail(S.appeals.manual.needQuestion()); return null; }
    if (type.value === "A" && !answer.value.trim()) { fail(S.appeals.manual.needAnswer()); return null; }
    return { type: type.value, question, answer: answer.value, text: text.value };
  };
  return {
    nodes: [block(S.appeals.manual.question(), q), block(S.appeals.manual.type(), type), answerBlock, block(S.appeals.card.text(), text)],
    read,
  };
}

const prose = (text: string): HTMLElement => el("div", { class: "pv-field" },
  linkSegments(text).map((s) => (s.href ? el("a", { class: "pv-link", href: s.href, target: "_blank", rel: "noopener noreferrer", text: s.text }) : s.text)));

// foldedBlock is a block cut to its first lines, with a toggle in its head
// that shows only when there is more to see.
function foldedBlock(label: string, ...body: Node[]): HTMLElement {
  const fold = el("div", { class: "appeal-fold is-folded" }, el("div", { class: "u-col u-gap-sm" }, ...body));
  const toggle = el("button", { class: "btn btn-ghost btn-xs", type: "button", hidden: true });
  const paint = (): void => {
    const folded = fold.classList.contains("is-folded");
    toggle.setAttribute("aria-expanded", String(!folded));
    toggle.replaceChildren(...iconed(folded ? "chevron-down" : "chevron-up", folded ? S.appeals.card.unfold() : S.appeals.card.fold()));
  };
  toggle.addEventListener("click", () => { fold.classList.toggle("is-folded"); paint(); });
  const measure = new ResizeObserver(() => {
    if (!fold.classList.contains("is-folded")) return;
    const cut = fold.scrollHeight > fold.clientHeight;
    toggle.hidden = !cut;
    fold.classList.toggle("is-cut", cut);
  });
  measure.observe(fold);
  measure.observe(fold.firstElementChild!);
  paint();
  return el("div", { class: "fld fld-wide" },
    el("div", { class: "fld-head" }, el("span", { class: "fld-label", text: label }), el("span", { class: "u-spacer" }), toggle),
    el("div", { class: "fld-body" }, fold));
}

export function createAppealCard(deps: AppealCardDeps): AppealCard {
  // controversials groups the answers by the game jury's verdict, declined
  // first, with the appealed one leading its group.
  function controversials(a: Appeal): HTMLElement {
    const label = S.appeals.card.controversials(String(a.question));
    if (!a.controversials.length) return block(label, el("p", { class: "hint", text: S.appeals.card.noControversials() }));
    const groups: [string, string, string][] = [
      ["D", "appeal-contro-declined", S.appeals.controversial.declined()],
      ["A", "appeal-contro-accepted", S.appeals.controversial.accepted()],
      ["N", "", S.appeals.controversial.open()],
    ];
    const boxes = groups.map(([st, cls, head]) => {
      const mine = a.controversials.filter((c) => (st === "N" ? c.status !== "A" && c.status !== "D" : c.status === st))
        .sort((x, y) => Number(y.id === a.controversial) - Number(x.id === a.controversial));
      if (!mine.length) return null;
      const rows = mine.map((c) => {
        const row = el("div", { title: c.comment || null, text: c.answer });
        if (c.id === a.controversial) row.append(" ", el("span", { class: "badge badge-emphasis", text: S.appeals.card.appealed() }));
        return row;
      });
      return el("div", { class: `appeal-contro ${cls}` }, el("div", { class: "fld-label", text: `${head} · ${mine.length}` }), ...rows);
    });
    return foldedBlock(label, el("div", { class: "appeal-contro-grid" }, ...boxes));
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
      const manual = deps.manual(listId);
      const verdict: Node[] = [decided(cardId, a), a.verdict.text ? prose(a.verdict.text) : el("p", { class: "hint", text: S.appeals.verdict.noText() })];
      if (!manual) verdict.push(site(a));
      box.replaceChildren(
        foldedBlock(S.appeals.card.question(), deps.question(listId, a.question)),
        foldedBlock(S.appeals.card.text(), ...text));
      if (!manual || a.controversials.length) box.append(controversials(a));
      box.append(votes(cardId, draw), block(S.appeals.verdict.label(), ...verdict));
    };
    draw();
    return box;
  }

  function editor(cardId: number, listId: number, a: Appeal, onInput: () => void): { node: HTMLElement; read(): string } {
    const sel = el("select", { class: "input" },
      el("option", { value: "", text: S.appeals.verdict.followVotes() }),
      el("option", { value: "A", text: S.appeals.status.accepted() }),
      el("option", { value: "D", text: S.appeals.status.declined() })) as HTMLSelectElement;
    sel.value = a.verdict.status;
    const text = el("textarea", { class: "input fld-input", rows: "10", placeholder: S.appeals.verdict.placeholder() }) as HTMLTextAreaElement;
    text.value = a.verdict.text;
    const manual = deps.manual(listId);
    const msg = el("p", { class: "hint hint-danger", hidden: true });
    const own = manual ? appealFields(a, onInput, (t) => { msg.textContent = t; msg.hidden = false; }) : null;
    // A half-typed question number keeps what the card had, so the draft stays
    // valid JSON while the jury types.
    const said = (): Partial<Appeal> => {
      msg.hidden = true;
      const f = own?.read();
      return f ? { ...f, answer: f.type === "A" ? f.answer.trim() : "", text: f.text.trim() } : {};
    };
    const read = (): Appeal => ({ ...a, ...said(), verdict: { status: sel.value as Status, text: text.value } });
    const status = el("div");
    const drawStatus = (): void => { status.replaceChildren(decided(cardId, read())); };
    sel.addEventListener("change", () => { drawStatus(); onInput(); });
    text.addEventListener("input", onInput);
    const votesBox = el("div", { class: "fld-wide" });
    const drawVotes = (): void => { votesBox.replaceChildren(votes(cardId, () => { drawVotes(); drawStatus(); })); };
    drawVotes();
    drawStatus();
    const node = el("div", { class: "card-fields" },
      ...(own ? [...own.nodes, msg] : []),
      votesBox,
      block(S.appeals.verdict.statusLabel(), sel, status),
      block(S.appeals.verdict.label(), text, site(a)));
    return { node, read: () => composeAppeal(read()) };
  }

  return { view, editor };
}
