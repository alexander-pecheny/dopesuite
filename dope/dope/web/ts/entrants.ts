// The entrants tab of a buzzer Game (CONTEXT.md, Entrant list): who the Game
// seats, in seed order, where that list comes from, and the host's hand edits.
// EK, ES, individual SI, Brain, Troika and Hamsa mount this one module; each page
// asks for the element when the tab is open and calls refresh() when a fest
// event says the list may have changed. The server does every rule — who may
// be removed, which seats move — and answers the tab afresh after each write.

import {td, th} from "./cells.js";
import {resultsPins, resultsTeamCell} from "./standings.js";
import {icon, iconed} from "./icons_gen.js";
import type {IconName} from "./icons_gen.js";
import S from "./i18nstrings.js";

// The list pins the seed number and the name.
const ENTRANT_PINS = resultsPins();

export interface EntrantSource {
  kind: string;
  game?: string;
  division?: string;
  // fresh takes the source's list alone, without the host's hand edits.
  fresh?: boolean;
}

interface SourceOption extends EntrantSource {
  label: string;
  divided?: boolean;
}

export interface EntrantRow {
  teamID: number;
  name?: string;
  city?: string;
  seedNumber?: number;
  declined?: boolean;
  waitlist?: boolean;
  played?: boolean;
  oneOff?: boolean;
}

export interface EntrantsView {
  kind?: "team" | "troika" | "player";
  rows?: EntrantRow[];
  drawSize?: number;
  activeCount?: number;
  edited?: boolean;
  // edits counts the hand edits a re-import applies again (ADR-0025).
  edits?: number;
  sources?: SourceOption[];
  preselect?: EntrantSource;
  divisions?: string[];
  candidates?: Array<{key: string; label: string}>;
  oneOffs?: boolean;
  entered?: boolean;
  resizes?: boolean;
  rebuilt?: boolean;
  kept?: string;
  // movesDropped: the hand moves an import from another source left behind.
  movesDropped?: number;
  // unranked: whom the last import seeded last, having nothing to rank them by.
  unranked?: string[];
}

export interface EntrantsTabOptions {
  // The Game's API base (/api/fest/{fest}/games/{game}). The EK page moves
  // between Games without a reload, so it passes a function.
  apiBase: string | (() => string);
  // The tab drew something new: the page may want to re-measure name fades.
  onRender?: () => void;
  // A write moved entrants between seats: the page's bouts may show others now.
  onChanged?: () => void;
  // The Game's Structure was rebuilt for the list: the page's bouts are stale.
  onRebuilt?: () => void;
}

export interface EntrantsTab {
  // The tab's panel, fetched on first use (and again for another Game).
  element(): HTMLElement;
  // Fetch the list again, if the tab has been drawn.
  refresh(): void;
  // Take a view the page already has, as the init payload.
  adopt(next: EntrantsView): void;
}

// sourceKey is a source as the picker's <option> value.
function sourceKey(source: EntrantSource | undefined): string {
  return source ? `${source.kind}:${source.game || ""}` : "";
}

export function createEntrantsTab(options: EntrantsTabOptions): EntrantsTab {
  const root = document.createElement("section");
  root.className = "results-wrapper seed-import-panel u-col u-gap-md";
  let view: EntrantsView | null = null;
  let loaded = false;
  let busy = false;
  let notice = "";
  let noticeError = false;
  let renaming = 0;
  // The entrant whose row asks who plays instead.
  let replacing = 0;
  // What the host picked, until an import settles it.
  let picked: EntrantSource | null = null;
  // The Game the view was read for.
  let loadedBase = "";

  function base(): string {
    return typeof options.apiBase === "function" ? options.apiBase() : options.apiBase;
  }

  async function load(): Promise<void> {
    loaded = true;
    loadedBase = base();
    try {
      const response = await fetch(`${base()}/entrants`);
      if (!response.ok) throw new Error((await response.text()).trim());
      view = await response.json() as EntrantsView;
    } catch (error) {
      setNotice(errorText(error), true);
    }
    render();
  }

  function setNotice(text: string, error = false): void {
    notice = text;
    noticeError = error;
  }

  // write sends one change and adopts the tab the server answers with.
  async function write(url: string, init: RequestInit): Promise<boolean> {
    if (busy) return false;
    busy = true;
    setNotice("");
    render();
    let ok = false;
    try {
      const response = await fetch(url, init);
      if (!response.ok) throw new Error((await response.text()).trim());
      view = await response.json() as EntrantsView;
      ok = true;
      if (view.rebuilt) options.onRebuilt?.();
      else options.onChanged?.();
    } catch (error) {
      setNotice(S.entrants.state.failed(errorText(error)), true);
    }
    busy = false;
    render();
    return ok;
  }

  function send(url: string, method: string, body?: unknown): Promise<boolean> {
    return write(url, {
      method,
      headers: {"Content-Type": "application/json"},
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  }

  function entrantURL(id: number): string {
    return `${base()}/entrants/${id}`;
  }

  function render(): void {
    const nodes: HTMLElement[] = [];
    if (view) {
      nodes.push(sourceBar(view));
      if (notice) nodes.push(hint(notice, noticeError));
      if (view.kept) nodes.push(hint(S.entrants.state.kept(view.kept)));
      if (view.movesDropped) nodes.push(hint(S.entrants.state.movesDropped(String(view.movesDropped))));
      if (view.unranked?.length) nodes.push(hint(S.entrants.state.unranked(view.unranked.join(", "))));
      const rows = view.rows || [];
      if (view.resizes) nodes.push(hint(S.entrants.state.resizes()));
      else if (view.entered) nodes.push(hint(S.entrants.state.fixed()));
      if (rows.length) {
        const seated = rows.filter((row) => row.seedNumber && !row.waitlist).length;
        nodes.push(hint(S.entrants.state.summary(String(view.drawSize || seated), String(seated), String(view.activeCount || 0))));
        nodes.push(table(rows));
      } else {
        nodes.push(emptyNote(S.entrants.state.empty()));
      }
      nodes.push(addForm(view));
    } else if (notice) {
      nodes.push(hint(notice, noticeError));
    }
    root.replaceChildren(...nodes);
    root.querySelector<HTMLInputElement>("[data-entrant-rename], [data-entrant-replace]")?.focus();
    options.onRender?.();
  }

  // ---- where the list comes from ----

  function sourceBar(current: EntrantsView): HTMLElement {
    const sources = current.sources || [];
    const choice = picked || current.preselect || sources[0];
    const option = sources.find((source) => sourceKey(source) === sourceKey(choice)) || sources[0];
    const bar = document.createElement("div");
    bar.className = "u-row u-wrap u-gap-sm u-align-center";

    const label = document.createElement("span");
    label.className = "hint";
    label.textContent = S.entrants.import.label();
    bar.appendChild(label);

    const select = document.createElement("select");
    select.className = "input";
    select.setAttribute("aria-label", S.entrants.import.label());
    for (const source of sources) {
      const node = document.createElement("option");
      node.value = sourceKey(source);
      node.textContent = source.label;
      node.selected = source === option;
      select.appendChild(node);
    }
    select.addEventListener("change", () => {
      const next = sources.find((source) => sourceKey(source) === select.value);
      picked = next ? {kind: next.kind, game: next.game, division: picked?.division ?? choice?.division} : null;
      render();
    });
    bar.appendChild(select);

    const divisions = current.divisions || [];
    let division: HTMLSelectElement | null = null;
    if (option?.divided && divisions.length) {
      division = document.createElement("select");
      division.className = "input";
      division.setAttribute("aria-label", S.entrants.filter.all());
      const values: Array<[string, string]> = [["", S.entrants.filter.all()]];
      for (const flag of divisions) {
        values.push([flag, S.entrants.filter.carrying(flag)], [`-${flag}`, S.entrants.filter.notCarrying(flag)]);
      }
      for (const [value, text] of values) {
        const node = document.createElement("option");
        node.value = value;
        node.textContent = text;
        node.selected = value === (choice?.division || "");
        division.appendChild(node);
      }
      division.addEventListener("change", () => {
        picked = {kind: option.kind, game: option.game, division: division!.value};
      });
      bar.appendChild(division);
    }

    let file: HTMLInputElement | null = null;
    if (option?.kind === "xlsx") {
      file = document.createElement("input");
      file.type = "file";
      file.accept = ".xlsx";
      file.className = "input";
      file.setAttribute("aria-label", S.entrants.import.file());
      bar.appendChild(file);
    }

    // A re-import applies the host's hand edits again (ADR-0025); the box
    // lets the host take the source's list alone instead.
    let keep: HTMLInputElement | null = null;
    const edits = current.edits || 0;
    if (edits > 0 && (current.rows || []).length) {
      const label = document.createElement("label");
      label.className = "u-row u-gap-xs u-align-center";
      keep = document.createElement("input");
      keep.type = "checkbox";
      keep.checked = true;
      label.append(keep, S.entrants.import.keepEdits(String(edits)));
      bar.appendChild(label);
    }

    const run = document.createElement("button");
    run.type = "button";
    run.className = "btn";
    run.textContent = S.entrants.import.run();
    run.disabled = busy || !option;
    run.addEventListener("click", () => {
      if (!option) return;
      const fresh = keep ? !keep.checked : false;
      if (fresh && !window.confirm(S.entrants.import.confirmFresh())) return;
      const source: EntrantSource = {kind: option.kind, game: option.game, division: option.divided ? division?.value || "" : "", fresh};
      const done = () => {
        picked = null;
        if (view) setNotice(S.entrants.import.done(view.rows?.length || 0));
        render();
      };
      if (file) {
        const chosen = file.files?.[0];
        if (!chosen) {
          setNotice(S.entrants.error.sourceMissing(), true);
          render();
          return;
        }
        const body = new FormData();
        body.append("file", chosen);
        if (fresh) body.append("fresh", "1");
        void write(`${base()}/entrants/import`, {method: "POST", body}).then((ok) => ok && done());
        return;
      }
      void send(`${base()}/entrants/import`, "POST", source).then((ok) => ok && done());
    });
    bar.appendChild(run);
    return bar;
  }

  // ---- the list ----

  function table(rows: EntrantRow[]): HTMLElement {
    const wrap = document.createElement("div");
    wrap.className = "table-scroll";
    const node = document.createElement("table");
    node.className = "results-table seed-import-table";
    const head = document.createElement("thead");
    const headRow = document.createElement("tr");
    headRow.append(
      ENTRANT_PINS.mark(th(S.entrants.head.seed(), "results-place-head seed-number-head"), "place"),
      ENTRANT_PINS.mark(th(S.entrants.head.name(), "results-team-head seed-team-head"), "name"),
      th(S.entrants.head.declined(), "seed-declined-head"),
      th("", "entrant-actions-head"),
    );
    head.appendChild(headRow);
    node.appendChild(head);
    const body = document.createElement("tbody");
    let waitlistShown = false;
    rows.forEach((row, index) => {
      if (row.waitlist && !waitlistShown) {
        waitlistShown = true;
        const divider = document.createElement("tr");
        divider.appendChild(td(S.entrants.row.waitlist(), "seed-waitlist-cell", {colSpan: 4}));
        body.appendChild(divider);
      }
      body.appendChild(entrantRow(rows, row, index));
    });
    node.appendChild(body);
    wrap.appendChild(node);
    return wrap;
  }

  function entrantRow(rows: EntrantRow[], row: EntrantRow, index: number): HTMLTableRowElement {
    const tr = document.createElement("tr");
    const classes = ["results-row"];
    const before = rows[index - 1];
    const after = rows[index + 1];
    if (!before || Boolean(before.waitlist) !== Boolean(row.waitlist)) classes.push("results-group-first");
    if (!after || Boolean(after.waitlist) !== Boolean(row.waitlist)) classes.push("results-group-last");
    if (row.declined) classes.push("seed-declined-row");
    tr.className = classes.join(" ");
    const name = row.name || "";
    if (row.played) tr.title = S.entrants.row.played();

    const seedCell = ENTRANT_PINS.mark(td("", "results-place seed-number-cell"), "place");
    const seed = document.createElement("input");
    seed.type = "text";
    seed.inputMode = "numeric";
    seed.className = "seed-number-input";
    seed.value = row.seedNumber ? String(row.seedNumber) : "";
    seed.disabled = busy || Boolean(row.played);
    seed.setAttribute("aria-label", S.entrants.row.seedLabel(name));
    seed.addEventListener("change", () => {
      const wanted = Number.parseInt(seed.value, 10);
      if (!Number.isFinite(wanted) || wanted <= 0 || wanted === row.seedNumber) {
        seed.value = row.seedNumber ? String(row.seedNumber) : "";
        return;
      }
      // The entrant takes the place of whoever holds that seed now; past the
      // last seed it goes to the end.
      const at = rows.findIndex((other) => (other.seedNumber || 0) >= wanted && !other.declined);
      const position = at < 0 ? rows.length : at + 1;
      void send(entrantURL(row.teamID), "PATCH", {position});
    });
    seedCell.appendChild(seed);
    tr.appendChild(seedCell);

    if (renaming === row.teamID) {
      tr.appendChild(td(renameField(row)));
    } else if (replacing === row.teamID) {
      tr.appendChild(td(replaceField(rows, row)));
    } else {
      tr.appendChild(ENTRANT_PINS.mark(resultsTeamCell(name, {city: row.city, badges: row.oneOff ? [S.entrants.row.oneOff()] : undefined}), "name"));
    }

    const declinedCell = td("", "results-num seed-declined-cell");
    const box = document.createElement("input");
    box.type = "checkbox";
    box.checked = Boolean(row.declined);
    box.disabled = busy;
    box.setAttribute("aria-label", S.entrants.row.declinedLabel(name));
    box.addEventListener("change", () => void send(entrantURL(row.teamID), "PATCH", {declined: box.checked}));
    declinedCell.appendChild(box);
    tr.appendChild(declinedCell);

    const actions = document.createElement("span");
    actions.className = "entrant-actions u-row u-gap-xs";
    const fixed = busy || Boolean(row.played);
    actions.appendChild(actionButton("arrow-up", S.entrants.row.up(), fixed || index === 0,
      () => void send(entrantURL(row.teamID), "PATCH", {position: index})));
    actions.appendChild(actionButton("arrow-down", S.entrants.row.down(), fixed || index === rows.length - 1,
      () => void send(entrantURL(row.teamID), "PATCH", {position: index + 2})));
    if (row.oneOff) {
      actions.appendChild(actionButton("pencil", S.entrants.row.rename(), fixed, () => {
        renaming = row.teamID;
        render();
      }));
    }
    actions.appendChild(actionButton("replace", S.entrants.row.replace(), fixed, () => {
      replacing = row.teamID;
      renaming = 0;
      render();
    }));
    actions.appendChild(actionButton("trash-2", S.entrants.row.remove(), fixed, () => {
      if (!window.confirm(S.entrants.row.removeConfirm(name))) return;
      void send(entrantURL(row.teamID), "DELETE");
    }));
    tr.appendChild(td(actions, "entrant-actions-cell"));
    return tr;
  }

  // renameField is a one-off's name as an input: Enter or leaving the field
  // saves it, Escape keeps the old one.
  function renameField(row: EntrantRow): HTMLElement {
    const input = document.createElement("input");
    input.type = "text";
    input.className = "input";
    input.value = row.name || "";
    input.dataset.entrantRename = "";
    input.setAttribute("aria-label", S.entrants.row.rename());
    let done = false;
    const finish = (save: boolean) => {
      if (done) return;
      done = true;
      renaming = 0;
      const next = input.value.trim();
      if (save && next && next !== row.name) void send(entrantURL(row.teamID), "PATCH", {name: next});
      else render();
    };
    input.addEventListener("keydown", (event) => {
      if (event.key === "Enter") finish(true);
      else if (event.key === "Escape") finish(false);
    });
    input.addEventListener("blur", () => finish(true));
    return input;
  }

  // replaceField asks who plays instead of an entrant: a fest team (troika,
  // player) from outside the list, an entrant of the list, who swaps places
  // with it, or, where the format allows, a one-off name. Enter sends it,
  // Escape or leaving the field empty keeps the row as it was.
  function replaceField(rows: EntrantRow[], row: EntrantRow): HTMLElement {
    const choices = new Map<string, string>();
    for (const candidate of view?.candidates || []) choices.set(candidate.label, candidate.key);
    rows.forEach((other, index) => {
      if (other.teamID === row.teamID || other.played) return;
      choices.set(S.entrants.row.replaceListed(other.name || "", String(index + 1)), `entrant:${other.teamID}`);
    });
    const listID = "entrant-replace-choices";
    const list = document.createElement("datalist");
    list.id = listID;
    for (const label of choices.keys()) {
      const option = document.createElement("option");
      option.value = label;
      list.appendChild(option);
    }
    const input = document.createElement("input");
    input.type = "text";
    input.className = "input";
    input.setAttribute("list", listID);
    input.placeholder = S.entrants.row.replacePlaceholder();
    input.setAttribute("aria-label", S.entrants.row.replacePick(row.name || ""));
    input.dataset.entrantReplace = "";
    let done = false;
    const finish = (save: boolean) => {
      if (done) return;
      const typed = input.value.trim();
      if (save && typed) {
        const key = choices.get(typed);
        if (!key && !view?.oneOffs) {
          setNotice(S.entrants.error.pickSomebody(), true);
          done = true;
          replacing = 0;
          render();
          return;
        }
        done = true;
        replacing = 0;
        void send(entrantURL(row.teamID), "PATCH", {replaceWith: key ? {key} : {name: typed}});
        return;
      }
      done = true;
      replacing = 0;
      render();
    };
    input.addEventListener("keydown", (event) => {
      if (event.key === "Enter") finish(true);
      else if (event.key === "Escape") finish(false);
    });
    // Picking from the list is the choice itself: no Enter needed.
    input.addEventListener("change", () => {
      if (choices.has(input.value.trim())) finish(true);
    });
    input.addEventListener("blur", () => finish(false));
    const wrap = document.createElement("span");
    wrap.className = "u-row u-gap-xs u-align-center";
    wrap.append(list, input);
    return wrap;
  }

  function actionButton(name: IconName, label: string, disabled: boolean, onClick: () => void): HTMLButtonElement {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "action-icon";
    button.title = label;
    button.setAttribute("aria-label", label);
    button.disabled = disabled;
    button.appendChild(icon(name));
    button.addEventListener("click", onClick);
    return button;
  }

  // ---- adding one ----

  function addForm(current: EntrantsView): HTMLElement {
    const wrap = document.createElement("div");
    wrap.className = "u-col u-gap-xs";
    const troika = current.kind === "troika";
    const candidates = current.candidates || [];
    const form = document.createElement("form");
    form.className = "u-row u-wrap u-gap-sm u-align-center";
    const listID = "entrant-candidates";
    const list = document.createElement("datalist");
    list.id = listID;
    for (const candidate of candidates) {
      const option = document.createElement("option");
      option.value = candidate.label;
      list.appendChild(option);
    }
    const input = document.createElement("input");
    input.type = "text";
    input.className = "input";
    input.size = 32;
    input.setAttribute("list", listID);
    input.placeholder = troika ? S.entrants.add.pickTroika() : S.entrants.add.pick();
    input.setAttribute("aria-label", input.placeholder);
    input.dataset.entrantAdd = "";
    const submit = document.createElement("button");
    submit.type = "submit";
    submit.className = "btn";
    submit.disabled = busy;
    submit.append(...iconed("plus", S.entrants.add.submit()));
    form.append(list, input, submit);
    form.addEventListener("submit", (event) => {
      event.preventDefault();
      const typed = input.value.trim();
      if (!typed) {
        input.focus();
        return;
      }
      const match = candidates.find((candidate) => candidate.label === typed);
      if (!match && !current.oneOffs) {
        setNotice(S.entrants.error.pickSomebody(), true);
        render();
        return;
      }
      void send(`${base()}/entrants`, "POST", match ? {key: match.key} : {name: typed}).then((ok) => {
        if (ok) root.querySelector<HTMLInputElement>("[data-entrant-add]")?.focus();
      });
    });
    wrap.appendChild(form);
    wrap.appendChild(hint(troika ? S.entrants.add.hintTroika() : S.entrants.add.hint()));
    if ((current.rows || []).length) wrap.appendChild(hint(S.entrants.add.replaceHint()));
    return wrap;
  }

  return {
    element(): HTMLElement {
      if (!loaded || loadedBase !== base()) {
        if (loadedBase !== base()) {
          view = null;
          picked = null;
          renaming = 0;
          replacing = 0;
          setNotice("");
          root.replaceChildren();
        }
        void load();
      }
      return root;
    },
    refresh(): void {
      if (!loaded || busy) return;
      void load();
    },
    adopt(next: EntrantsView): void {
      view = next;
      loaded = true;
      loadedBase = base();
      render();
    },
  };
}

function hint(text: string, danger = false): HTMLElement {
  const node = document.createElement("p");
  node.className = danger ? "hint hint-danger" : "hint";
  node.textContent = text;
  return node;
}

function emptyNote(text: string): HTMLElement {
  const node = document.createElement("p");
  node.className = "empty";
  node.textContent = text;
  return node;
}

function errorText(error: unknown): string {
  return error instanceof Error ? error.message.trim() : String(error);
}
