// appealspanel.ts — an appeals List's entries in its ⋯ menu (ADR-0021): its
// settings (where its appeals come from, the tournament, the packet and the
// board's rating token), a refresh from the rating site, the export of
// verdicts back to it, and the same verdicts as an .xlsx for a jury whose
// token cannot write. A manual List has none of the site's entries and adds
// its appeals by hand instead. The rating site is reached through the
// server's relay.

import S from "./i18nstrings.js";
import { xyApp } from "./app.js";
import { xyCrypto } from "./crypto.js";
import { xySync } from "./sync.js";
import { byRank } from "./dragrank.js";
import { iconed } from "./icons_gen.js";
import { KIND } from "./cardkind.js";
import { createList } from "./addlist.js";
import { xlsxSheet } from "./xlsx.js";
import { appealFields, appealHeading, statusText } from "./appealcard.js";
import {
  addManual, composeAppeal, composeMeta, parseAppeal, parseMeta, planExport, planRefresh, verdictRows, NO_META,
  type Appeal, type AppealsMeta, type Held, type Outgoing, type SiteAppeal, type SiteControversial,
} from "./appeals.js";
import type { Board, ListPanel, PanelShell } from "./panels.js";
import type { BoardList } from "./unlock.js";

const { el, jpost, errMsg, downloadBlob } = xyApp;

const XLSX_TYPE = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet";

export interface AppealsPanels {
  settings: ListPanel;
  refresh: ListPanel;
  export: ListPanel;
  xlsx: ListPanel;
  add: ListPanel;
  // Opens the settings for a List that does not exist yet: it is created when
  // they are saved, so an appeals List never exists without its packet.
  create(title: string, rank: string): void;
}

interface SiteAnswer {
  appeals: SiteAppeal[];
  controversials: SiteControversial[];
  controversials_error?: string;
}

export const isAppeals = (list: BoardList): boolean => list.type === "appeals";
export const isManual = (list: BoardList): boolean => isAppeals(list) && parseMeta(list.meta).manual;
const isFromSite = (list: BoardList): boolean => isAppeals(list) && !parseMeta(list.meta).manual;

// A password manager takes a text field followed by a password field for a
// login form and fills both in. These attributes ask every common one not to.
const NOT_A_LOGIN = { autocomplete: "off", "data-1p-ignore": "true", "data-lpignore": "true", "data-bwignore": "true", "data-form-type": "other" };

// tournamentId reads a number typed by hand or pasted as a link to the
// tournament's page on the site.
export function tournamentId(text: string): number | null {
  const m = /(\d+)\D*$/.exec(text.trim());
  return m ? Number(m[1]) : null;
}

export function createAppealsPanels(board: Board, shell: PanelShell): AppealsPanels {
  const held = (list: BoardList): Held[] => board.cardsOf(list.id)
    .filter((c) => c.kind === KIND.appeal)
    .map((c) => ({ cardId: c.id, rank: c.rank, appeal: parseAppeal(c.desc) }))
    .filter((h): h is Held => h.appeal != null);

  // The packets a List may be linked to: every List that is not an appeals
  // List, a List Group offered once under its name.
  function packetChoices(): Array<{ id: number; label: string }> {
    const seen = new Set<number>();
    const out: Array<{ id: number; label: string }> = [];
    for (const l of [...board.state.lists].sort(byRank)) {
      if (isAppeals(l)) continue;
      if (l.groupId == null) { out.push({ id: l.id, label: l.title || S.board.list.untitled() }); continue; }
      if (seen.has(l.groupId)) continue;
      seen.add(l.groupId);
      out.push({ id: l.id, label: S.appeals.settings.groupOption(board.groupById(l.groupId)?.name || S.board.list.groupFallback()) });
    }
    return out;
  }

  function field(label: string, control: HTMLElement, hint?: string): HTMLElement {
    const node = el("div", { class: "fld fld-wide" },
      el("div", { class: "fld-head" }, el("span", { class: "fld-label", text: label })),
      el("div", { class: "fld-body u-col u-gap-xs" }, control));
    if (hint) node.querySelector(".fld-body")?.append(el("p", { class: "hint", text: hint }));
    return node;
  }

  async function saveToken(token: string): Promise<void> {
    if (token === board.state.ratingToken) return;
    const sealed = token ? await xyCrypto.encField(board.dk(), token) : "";
    await board.verbs.patch("patchBoard", `/api/boards/${board.id}`, { rating_token: sealed });
    board.state.ratingToken = token;
  }

  // openSettings draws the form for an existing List, or for one about to be
  // made when `create` is given.
  function openSettings(list: BoardList | null, create?: { title: string; rank: string }): void {
    const meta = list ? parseMeta(list.meta) : NO_META;
    const nodes: HTMLElement[] = [];
    let titleInp: HTMLInputElement | null = null;
    if (create) {
      titleInp = el("input", { class: "input", type: "text", value: create.title, ...NOT_A_LOGIN }) as HTMLInputElement;
      nodes.push(field(S.appeals.settings.title(), titleInp));
    }
    const modeSel = el("select", { class: "input" },
      el("option", { value: "site", text: S.appeals.settings.modeSite() }),
      el("option", { value: "manual", text: S.appeals.settings.modeManual() })) as HTMLSelectElement;
    modeSel.value = meta.manual ? "manual" : "site";
    // A hand-made appeal's id means nothing to the site, so a List that holds
    // appeals keeps the way it was filled.
    const filled = list != null && held(list).length > 0;
    modeSel.disabled = filled;
    nodes.push(field(S.appeals.settings.mode(), modeSel, filled ? S.appeals.settings.modeLocked() : S.appeals.settings.modeHint()));
    const tInp = el("input", { class: "input", type: "text", inputmode: "numeric", value: meta.tournamentId ? String(meta.tournamentId) : "", ...NOT_A_LOGIN }) as HTMLInputElement;
    const tField = field(S.appeals.settings.tournament(), tInp, S.appeals.settings.tournamentHint());
    nodes.push(tField);
    const choices = packetChoices();
    const pSel = el("select", { class: "input" },
      el("option", { value: "", text: S.appeals.settings.packetNone() }),
      ...choices.map((c) => el("option", { value: String(c.id), text: c.label }))) as HTMLSelectElement;
    const groupOf = (id: number): number | null => board.state.lists.find((l) => l.id === id)?.groupId ?? null;
    const linkedGroup = meta.packetListId != null ? groupOf(meta.packetListId) : null;
    const pick = choices.find((c) => c.id === meta.packetListId || (linkedGroup != null && groupOf(c.id) === linkedGroup));
    pSel.value = pick ? String(pick.id) : "";
    nodes.push(field(S.appeals.settings.packet(), pSel, S.appeals.settings.packetHint()));
    // The token is a text field drawn as dots: a password field is what makes
    // a password manager offer the user's own login here.
    const tokInp = el("input", { class: "input input-secret", type: "text", spellcheck: "false", value: board.state.ratingToken, placeholder: "rt_…", ...NOT_A_LOGIN }) as HTMLInputElement;
    const tokField = field(S.appeals.settings.token(), tokInp, S.appeals.settings.tokenHint());
    nodes.push(tokField);
    const msg = el("p", { class: "hint hint-danger", hidden: true });
    const save = el("button", { class: "btn btn-primary", type: "submit" }) as HTMLButtonElement;
    const manual = (): boolean => modeSel.value === "manual";
    const sync = (): void => {
      tField.hidden = manual();
      tokField.hidden = manual();
      save.textContent = !create ? S.appeals.settings.save() : manual() ? S.appeals.settings.createManual() : S.appeals.settings.create();
    };
    modeSel.addEventListener("change", sync);
    sync();
    const form = el("form", { class: "u-col u-gap-md", ...NOT_A_LOGIN }, ...nodes, msg, el("div", { class: "u-row u-gap-sm" }, save));
    form.addEventListener("submit", (e) => {
      e.preventDefault();
      const next: AppealsMeta = { tournamentId: tournamentId(tInp.value), packetListId: pSel.value ? Number(pSel.value) : null, manual: manual() };
      const fail = (text: string): void => { msg.textContent = text; msg.hidden = false; };
      if (!next.manual && !next.tournamentId) { fail(S.appeals.settings.needTournament()); return; }
      if (!next.manual && !next.packetListId) { fail(S.appeals.settings.needPacket()); return; }
      if (create && !(titleInp?.value.trim())) { fail(S.appeals.settings.needTitle()); return; }
      save.disabled = true;
      void (async () => {
        board.setStatus("saving");
        try {
          if (!next.manual) await saveToken(tokInp.value.trim());
          let target = list;
          if (!target && create) {
            target = await createList(board, (titleInp as HTMLInputElement).value.trim(), "appeals", create.rank, composeMeta(next));
          } else if (target) {
            const sealed = await xyCrypto.encField(board.dk(), composeMeta(next));
            await board.verbs.patch("patchList", `/api/lists/${target.id}`, { meta: sealed });
            target.meta = composeMeta(next);
          }
          board.setStatus("saved");
          board.render();
          if (!next.manual && target && (create || !held(target).length)) await runRefresh(target);
          else if (next.manual && target && create) openAdd(target);
          else shell.message("");
        } catch (err) {
          board.setStatus("error");
          fail(errMsg(err));
        } finally { save.disabled = false; }
      })();
    });
    shell.open({ icon: "gavel", title: create ? S.appeals.settings.createTitle() : S.appeals.settings.titleFor(list?.title || ""), body: form });
    (titleInp || modeSel).focus();
  }

  // openAdd is the form for one appeal typed in by hand. Saving it keeps the
  // form open and empty, for the next one.
  function openAdd(list: BoardList, lastType = "A"): void {
    const msg = el("p", { class: "hint", hidden: true });
    const fail = (text: string): void => { msg.className = "hint hint-danger"; msg.textContent = text; msg.hidden = false; };
    const f = appealFields({ type: lastType, question: 0, answer: "", text: "" }, () => { msg.hidden = true; }, fail);
    const save = el("button", { class: "btn btn-primary", type: "submit", text: S.appeals.manual.save() }) as HTMLButtonElement;
    const form = el("form", { class: "u-col u-gap-md", ...NOT_A_LOGIN }, ...f.nodes, msg, el("div", { class: "u-row u-gap-sm" }, save));
    form.addEventListener("submit", (e) => {
      e.preventDefault();
      const fields = f.read();
      if (!fields) return;
      save.disabled = true;
      void (async () => {
        board.setStatus("saving");
        try {
          const { appeal, rank } = addManual(held(list), fields, new Date().toISOString());
          const desc = composeAppeal(appeal);
          const res = await board.verbs.create("createCard", `/api/lists/${list.id}/cards`, { description: await xyCrypto.encField(board.dk(), desc), rank, kind: KIND.appeal });
          board.state.cards.push({ id: res.id as number, listId: list.id, kind: KIND.appeal, rank, desc, handoutMeta: null, alias: null, createdAt: appeal.issuedAt });
          board.setStatus("saved");
          board.render();
          openAdd(list, fields.type);
          shell.message(S.appeals.manual.added(appealHeading(appeal)));
        } catch (err) {
          board.setStatus("error");
          fail(errMsg(err));
          save.disabled = false;
        }
      })();
    });
    shell.open({ icon: "plus", title: S.appeals.manual.title(list.title), body: form });
    form.querySelector("input")?.focus();
  }

  async function fetchSite(meta: AppealsMeta): Promise<SiteAnswer> {
    return (await jpost("/api/rating/appeals", { tournament_id: meta.tournamentId, token: board.state.ratingToken })) as SiteAnswer;
  }

  // report replaces the shell's body with one finished outcome.
  function report(icon: "refresh-cw" | "cloud-upload", title: string, lines: string[], danger: string[] = []): void {
    shell.open({
      icon, title,
      body: el("div", { class: "u-col u-gap-sm" },
        ...lines.map((t) => el("p", { text: t })),
        ...danger.map((t) => el("p", { class: "hint hint-danger", text: t }))),
    });
  }

  async function runRefresh(list: BoardList): Promise<void> {
    const title = S.appeals.refresh.title();
    const meta = parseMeta(list.meta);
    if (!meta.tournamentId) { openSettings(list); return; }
    if (!xySync.requireOnline(S.appeals.offline())) return;
    report("refresh-cw", title, [S.appeals.refresh.loading(String(meta.tournamentId))]);
    try {
      const site = await fetchSite(meta);
      const plan = planRefresh(held(list), site.appeals || [], site.controversials || [], new Date().toISOString());
      board.setStatus("saving");
      const dk = board.dk();
      for (const c of plan.create) {
        const desc = composeAppeal(c.appeal);
        const res = await board.verbs.create("createCard", `/api/lists/${list.id}/cards`, { description: await xyCrypto.encField(dk, desc), rank: c.rank, kind: KIND.appeal });
        board.state.cards.push({ id: res.id as number, listId: list.id, kind: KIND.appeal, rank: c.rank, desc, handoutMeta: null, alias: null, createdAt: new Date().toISOString() });
      }
      for (const u of plan.update) await writeAppeal(u.cardId, u.appeal);
      board.setStatus("saved");
      board.render();
      const lines = [S.appeals.refresh.done(String(plan.create.length), String(plan.update.length), String((site.appeals || []).length))];
      const changed = plan.update.filter((u) => u.appeal.textChangedAt).length;
      const danger = site.controversials_error ? [S.appeals.refresh.noControversials(site.controversials_error)] : [];
      if (changed) danger.push(S.appeals.refresh.textChanged(changed));
      report("refresh-cw", title, lines, danger);
    } catch (err) {
      board.setStatus("error");
      report("refresh-cw", title, [], [errMsg(err)]);
    }
  }

  async function writeAppeal(cardId: number, a: Appeal): Promise<void> {
    const desc = composeAppeal(a);
    await board.verbs.patch("patchCard", `/api/cards/${cardId}`, { description: await xyCrypto.encField(board.dk(), desc) });
    const card = board.state.cards.find((c) => c.id === cardId);
    if (card) card.desc = desc;
  }

  async function openExport(list: BoardList): Promise<void> {
    const title = S.appeals.export.title();
    const meta = parseMeta(list.meta);
    if (!meta.tournamentId) { openSettings(list); return; }
    if (!board.state.ratingToken) { report("cloud-upload", title, [], [S.appeals.export.needToken()]); return; }
    if (!xySync.requireOnline(S.appeals.offline())) return;
    report("cloud-upload", title, [S.appeals.export.loading()]);
    let plan: Outgoing[];
    const mine = held(list);
    try {
      plan = planExport(mine, board.state.votes, (await fetchSite(meta)).appeals || []);
    } catch (err) { report("cloud-upload", title, [], [errMsg(err)]); return; }
    if (!plan.length) { report("cloud-upload", title, [S.appeals.export.nothing()]); return; }
    const byCard = new Map(mine.map((h) => [h.cardId, h.appeal]));
    const ticks = plan.map((o) => {
      const box = el("input", { type: "checkbox", checked: true }) as HTMLInputElement;
      const a = byCard.get(o.cardId);
      const row = el("label", { class: "u-row u-gap-sm u-align-start" }, box,
        el("span", { class: "u-col u-gap-xs u-grow" },
          el("span", { text: `${a ? appealHeading(a) : o.appealId} — ${statusText(o.status)}` }),
          el("span", { class: "hint", text: S.appeals.export.siteNow(statusText(o.siteStatus)) })));
      return { o, box, row };
    });
    const send = el("button", { class: "btn btn-primary", type: "button" }, ...iconed("cloud-upload", S.appeals.export.send(String(plan.length))));
    const recount = (): void => { send.lastChild!.textContent = S.appeals.export.send(String(ticks.filter((t) => t.box.checked).length)); };
    for (const t of ticks) t.box.addEventListener("change", recount);
    send.addEventListener("click", () => { void sendVerdicts(list, ticks.filter((t) => t.box.checked).map((t) => t.o)); });
    shell.open({ icon: "cloud-upload", title, body: el("div", { class: "u-col u-gap-md" },
      el("p", { class: "hint", text: S.appeals.export.hint() }),
      el("div", { class: "u-col u-gap-sm" }, ...ticks.map((t) => t.row)),
      el("div", { class: "u-row u-gap-sm" }, send)) });
  }

  async function sendVerdicts(list: BoardList, outgoing: Outgoing[]): Promise<void> {
    const title = S.appeals.export.title();
    report("cloud-upload", title, [S.appeals.export.sending(String(outgoing.length))]);
    const failed: string[] = [];
    let sent = 0;
    for (const o of outgoing) {
      try {
        const answer = (await jpost("/api/rating/verdict", { appeal_id: o.appealId, status: o.status, comment: o.comment, token: board.state.ratingToken })) as SiteAppeal;
        sent++;
        const h = held(list).find((x) => x.cardId === o.cardId);
        if (h) await writeAppeal(o.cardId, { ...h.appeal, site: { status: answer.status || o.status, comment: (answer.comment ?? o.comment) || "", overriddenBy: answer.overriddenById ?? null } });
      } catch (err) { failed.push(S.appeals.export.failedOne(String(o.appealId), errMsg(err))); }
    }
    board.render();
    report("cloud-upload", title, [S.appeals.export.done(String(sent))], failed);
  }

  async function downloadXlsx(list: BoardList): Promise<void> {
    const bytes = await xlsxSheet(verdictRows(held(list), board.state.votes));
    const id = parseMeta(list.meta).tournamentId;
    downloadBlob(new Blob([bytes], { type: XLSX_TYPE }), `tournament-appeals-${id ?? list.id}.xlsx`);
  }

  return {
    settings: { id: "appeals-settings", menu: "list", icon: "gavel", label: S.appeals.settings.label(), offered: (s) => isAppeals(s.list), open: (s) => openSettings(s.list) },
    add: { id: "appeals-add", menu: "list", icon: "plus", label: S.appeals.manual.label(), offered: (s) => isManual(s.list), open: (s) => openAdd(s.list) },
    refresh: { id: "appeals-refresh", menu: "list", icon: "refresh-cw", label: S.appeals.refresh.label(), offered: (s) => isFromSite(s.list), open: (s) => { void runRefresh(s.list); } },
    export: { id: "appeals-export", menu: "list", icon: "cloud-upload", label: S.appeals.export.label(), offered: (s) => isFromSite(s.list), open: (s) => { void openExport(s.list); } },
    xlsx: { id: "appeals-xlsx", menu: "list", icon: "file-down", label: S.appeals.xlsx.label(), offered: (s) => isFromSite(s.list), open: (s) => { void downloadXlsx(s.list).catch((err) => alert(errMsg(err))); } },
    create: (title, rank) => openSettings(null, { title, rank }),
  };
}
