// The fest's teams page (ADR-0024): the team editor, sending each change at
// once, and the roster loaded from an xlsx.

import {openTeamDialog, request, setInstantSearch} from "./team-editor.js";
import type {Answer, TeamDetail} from "./team-editor.js";
import S from "./i18nstrings.js";

// Bind the page: the add button carries the fest's API, each pencil its team.
(() => {
  const addButton = document.querySelector<HTMLElement>("[data-team-add]");
  const api = addButton?.dataset.teamAdd || "";
  if (!addButton || !api) return;
  const pageVenue = Number(addButton.dataset.teamVenue || 0);
  setInstantSearch(addButton.dataset.teamSearch === "instant");
  // The teams page sends each change at once, and reloads once it is kept.
  const reloaded = (answer: Answer): Answer => {
    if (answer.ok) window.location.reload();
    return answer;
  };
  const open = async (id: string) => {
    const answer = await request(`${api}/teams/${id}`, "GET");
    if (!answer.ok) return;
    const team = answer.data as TeamDetail;
    openTeamDialog(api, team, pageVenue, {
      save: (body) => request(team.id > 0 ? `${api}/teams/${team.id}` : `${api}/teams`, team.id > 0 ? "PUT" : "POST", body).then(reloaded),
      remove: () => request(`${api}/teams/${team.id}`, "DELETE").then(reloaded),
    });
  };
  addButton.addEventListener("click", () => void open("new"));
  for (const pencil of document.querySelectorAll<HTMLElement>("[data-team-edit]")) {
    pencil.addEventListener("click", (event) => {
      event.preventDefault();
      void open(pencil.dataset.teamEdit || "");
    });
  }
})();

interface SheetPlan {
  added_teams: string[];
  renamed: Array<{from: string; to: string}>;
  players: Array<{team: string; added: string[]; removed: string[]}>;
}

async function sendSheet(api: string, file: File, preview: boolean): Promise<{ok: boolean; plan: SheetPlan | null; message: string}> {
  const body = new FormData();
  body.append("file", file);
  try {
    const response = await fetch(`${api}/teams/xlsx${preview ? "?preview=1" : ""}`, {method: "POST", body});
    const text = (await response.text()).trim();
    if (!response.ok) return {ok: false, plan: null, message: text || S.fest.rosterSheet.failed()};
    return {ok: true, plan: (JSON.parse(text) as {plan: SheetPlan}).plan, message: ""};
  } catch {
    return {ok: false, plan: null, message: S.fest.rosterSheet.failed()};
  }
}

// openSheetDialog shows what a roster sheet changes, and loads it on confirm.
function openSheetDialog(api: string, file: File, plan: SheetPlan): void {
  const dialog = document.createElement("dialog");
  dialog.className = "modal-dialog roster-dialog";
  const form = document.createElement("form");
  form.className = "u-col u-gap-md";
  const title = document.createElement("h2");
  title.textContent = S.fest.rosterSheet.title();
  const lines = document.createElement("div");
  lines.className = "u-col u-gap-sm";
  const muted = (text: string) => {
    const line = document.createElement("p");
    line.className = "hint";
    line.textContent = text;
    return line;
  };
  const empty = !plan.added_teams.length && !plan.renamed.length && !plan.players.length;
  if (empty) lines.appendChild(muted(S.fest.rosterSheet.nothing()));
  if (plan.added_teams.length) lines.appendChild(muted(S.fest.rosterSheet.addedTeams(plan.added_teams.join(", "))));
  for (const r of plan.renamed) lines.appendChild(muted(S.fest.rosterSheet.renamed(r.from, r.to)));
  for (const team of plan.players) {
    const block = document.createElement("div");
    const name = document.createElement("strong");
    name.textContent = team.team;
    block.appendChild(name);
    if (team.added.length) block.appendChild(muted(S.fest.rosterSheet.playersAdded(team.added.join(", "))));
    if (team.removed.length) block.appendChild(muted(S.fest.rosterSheet.playersRemoved(team.removed.join(", "))));
    lines.appendChild(block);
  }
  const error = document.createElement("p");
  error.className = "hint hint-danger";
  error.hidden = true;
  const actions = document.createElement("div");
  actions.className = "modal-actions";
  const cancel = document.createElement("button");
  cancel.type = "button";
  cancel.className = "btn";
  cancel.textContent = S.fest.rosterSheet.cancel();
  cancel.addEventListener("click", () => dialog.close());
  actions.appendChild(cancel);
  if (!empty) {
    const confirm = document.createElement("button");
    confirm.type = "submit";
    confirm.className = "btn";
    confirm.textContent = S.fest.rosterSheet.confirm();
    actions.appendChild(confirm);
  }
  form.addEventListener("submit", (event) => {
    event.preventDefault();
    for (const button of form.querySelectorAll<HTMLButtonElement>("button")) button.disabled = true;
    void sendSheet(api, file, false).then((answer) => {
      if (answer.ok) {
        dialog.close();
        window.location.reload();
        return;
      }
      for (const button of form.querySelectorAll<HTMLButtonElement>("button")) button.disabled = false;
      error.textContent = answer.message;
      error.hidden = false;
    });
  });
  form.append(title, muted(S.fest.rosterSheet.hint()), lines, error, actions);
  dialog.appendChild(form);
  dialog.addEventListener("close", () => dialog.remove());
  document.body.appendChild(dialog);
  dialog.showModal();
}

// The load-from-xlsx button: pick a file, see what it changes, confirm.
(() => {
  const button = document.querySelector<HTMLElement>("[data-team-xlsx]");
  const api = document.querySelector<HTMLElement>("[data-team-add]")?.dataset.teamAdd || "";
  if (!button || !api) return;
  const picker = document.createElement("input");
  picker.type = "file";
  picker.accept = ".xlsx,application/vnd.openxmlformats-officedocument.spreadsheetml.sheet";
  picker.hidden = true;
  document.body.appendChild(picker);
  button.addEventListener("click", () => picker.click());
  picker.addEventListener("change", () => {
    const file = picker.files?.[0];
    picker.value = "";
    if (!file) return;
    void sendSheet(api, file, true).then((answer) => {
      if (answer.ok && answer.plan) openSheetDialog(api, file, answer.plan);
      else window.alert(answer.message);
    });
  });
})();
