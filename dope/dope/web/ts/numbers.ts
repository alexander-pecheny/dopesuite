// Fest team-numbers page: edit-in-place toggle (Замена номера) and the two mass
// number-import <dialog> modals (paste → confirm → apply). Extracted verbatim
// from the page's former inline <script>; keyed on the #numbers-* ids and
// #numbers-form's data-has-numbers.

// Wire types of pages/numbers_import.go (importMatchResponse / importApplyResponse).
interface ImportTeamOption {
  id: number;
  label: string;
}

interface ImportMatch {
  line: number;
  number: number;
  raw: string;
  teamId: number;
  distance: number;
  exact: boolean;
}

interface ImportMatchResponse {
  teams: ImportTeamOption[] | null;
  matches: ImportMatch[] | null;
  errors: string[] | null;
}

interface ImportApplyResponse {
  ok: boolean;
  error?: string;
}

(() => {
  const form = document.getElementById("numbers-form");
  if (!form) return;
  const byId = <T extends HTMLElement>(id: string): T => {
    const node = document.getElementById(id);
    if (!node) throw new Error(`numbers page is missing #${id}`);
    return node as T;
  };
  const hasNumbers = form.dataset.hasNumbers === "1";
  const editBtn = document.getElementById("numbers-edit-btn");
  const autoBtn = document.getElementById("numbers-auto-btn");
  const clearBtn = document.getElementById("numbers-clear-btn");
  const cancelBtn = document.getElementById("numbers-cancel-btn");
  const help = byId("numbers-help");
  const save = byId("numbers-save");
  const numInputs = form.querySelectorAll<HTMLInputElement>(".number-row-num");

  const enterEdit = () => {
    form.classList.add("editing");
    numInputs.forEach((input) => input.removeAttribute("readonly"));
    help.removeAttribute("hidden");
    save.removeAttribute("hidden");
    if (editBtn) editBtn.setAttribute("hidden", "");
    if (autoBtn) autoBtn.setAttribute("hidden", "");
    if (clearBtn) clearBtn.setAttribute("hidden", "");
  };
  const exitEdit = () => {
    location.reload();
  };

  if (editBtn) editBtn.addEventListener("click", enterEdit);
  if (cancelBtn) cancelBtn.addEventListener("click", exitEdit);
  if (autoBtn && hasNumbers) {
    autoBtn.addEventListener("click", (event) => {
      const ok = window.confirm("Команды будут перенумерованы 1..N по алфавиту. Если бланки ответов уже напечатаны со старыми номерами — они станут невалидными. Продолжить?");
      if (!ok) event.preventDefault();
    });
  }
  if (clearBtn) {
    clearBtn.addEventListener("click", (event) => {
      const ok = window.confirm("Очистить все номера команд?");
      if (!ok) event.preventDefault();
    });
  }

  const importBtn = document.getElementById("numbers-import-btn");
  const baseAction = form.getAttribute("action");

  const closeDialog = (dialog: HTMLDialogElement) => {
    if (dialog && dialog.parentNode) dialog.close();
  };

  const openImportDialog = () => {
    const dialog = document.createElement("dialog");
    dialog.className = "modal-dialog numbers-import-dialog";
    const dform = document.createElement("form");
    dform.className = "numbers-import-form";
    const title = document.createElement("h2");
    title.textContent = "Импорт номеров";
    const hint = document.createElement("p");
    hint.className = "muted";
    hint.textContent = "Вставьте строки в формате «номер⇥команда» (через табуляцию), по одной на строку. Имена сопоставятся с командами феста; неточные совпадения можно будет поправить.";
    const textarea = document.createElement("textarea");
    textarea.className = "numbers-import-textarea";
    textarea.rows = 12;
    textarea.placeholder = "1\tНазвание команды\n2\tДругая команда";
    const err = document.createElement("p");
    err.className = "empty";
    err.hidden = true;
    const actions = document.createElement("div");
    actions.className = "modal-actions";
    const cancel = document.createElement("button");
    cancel.type = "button";
    cancel.className = "btn btn-secondary";
    cancel.textContent = "Отмена";
    cancel.addEventListener("click", () => closeDialog(dialog));
    const submit = document.createElement("button");
    submit.type = "submit";
    submit.className = "btn";
    submit.textContent = "Сопоставить";
    actions.append(cancel, submit);
    dform.append(title, hint, textarea, err, actions);
    dform.addEventListener("submit", (event) => {
      event.preventDefault();
      const text = textarea.value;
      if (!text.trim()) {
        err.textContent = "Вставьте данные для импорта.";
        err.hidden = false;
        return;
      }
      submit.disabled = true;
      err.hidden = true;
      fetch(`${baseAction}/import/match`, {
        method: "POST",
        headers: { "Content-Type": "application/x-www-form-urlencoded" },
        body: new URLSearchParams({ text }),
      })
        .then((resp) => {
          if (!resp.ok) throw new Error("Ошибка сервера (" + resp.status + ").");
          return resp.json() as Promise<ImportMatchResponse>;
        })
        .then((data) => {
          closeDialog(dialog);
          openConfirmDialog(data);
        })
        .catch((e: unknown) => {
          submit.disabled = false;
          err.textContent = (e instanceof Error && e.message) || "Не удалось сопоставить.";
          err.hidden = false;
        });
    });
    dialog.appendChild(dform);
    dialog.addEventListener("close", () => dialog.remove());
    document.body.appendChild(dialog);
    dialog.showModal();
    textarea.focus();
  };

  const openConfirmDialog = (data: ImportMatchResponse) => {
    const teams = (data && data.teams) || [];
    const matches = (data && data.matches) || [];
    const errors = (data && data.errors) || [];

    const dialog = document.createElement("dialog");
    dialog.className = "modal-dialog numbers-import-dialog";
    const dform = document.createElement("form");
    dform.className = "numbers-import-form";
    const title = document.createElement("h2");
    title.textContent = "Подтвердите сопоставление";

    dform.append(title);

    if (errors.length) {
      const errBox = document.createElement("ul");
      errBox.className = "numbers-import-errors";
      errors.forEach((line) => {
        const li = document.createElement("li");
        li.textContent = line;
        errBox.appendChild(li);
      });
      dform.appendChild(errBox);
    }

    if (!matches.length) {
      const empty = document.createElement("p");
      empty.className = "empty";
      empty.textContent = "Не удалось разобрать ни одной строки.";
      dform.appendChild(empty);
    }

    const buildSelect = (selectedId: number) => {
      const select = document.createElement("select");
      select.className = "numbers-import-select";
      const skip = document.createElement("option");
      skip.value = "";
      skip.textContent = "— пропустить —";
      select.appendChild(skip);
      teams.forEach((team) => {
        const opt = document.createElement("option");
        opt.value = String(team.id);
        opt.textContent = team.label;
        if (team.id === selectedId) opt.selected = true;
        select.appendChild(opt);
      });
      return select;
    };

    const rowEls: Array<{ number: number; select: HTMLSelectElement }> = [];
    if (matches.length) {
      const list = document.createElement("ol");
      list.className = "numbers-import-list";
      matches.forEach((m) => {
        const li = document.createElement("li");
        li.className = "numbers-import-row";
        if (!m.teamId) li.classList.add("is-unmatched");
        else if (!m.exact) li.classList.add("is-fuzzy");

        const num = document.createElement("span");
        num.className = "numbers-import-num";
        num.textContent = String(m.number);

        const raw = document.createElement("span");
        raw.className = "numbers-import-raw";
        raw.textContent = m.raw;

        const arrow = document.createElement("span");
        arrow.className = "numbers-import-arrow";
        arrow.textContent = "→";

        const select = buildSelect(m.teamId);

        const badge = document.createElement("span");
        badge.className = "numbers-import-badge";
        if (!m.teamId) badge.textContent = "нет совпадения";
        else if (m.exact) badge.textContent = "точно";
        else badge.textContent = "≈ (" + m.distance + ")";

        li.append(num, raw, arrow, select, badge);
        list.appendChild(li);
        rowEls.push({ number: m.number, select });
      });
      dform.appendChild(list);
    }

    const err = document.createElement("p");
    err.className = "empty";
    err.hidden = true;
    dform.appendChild(err);

    const actions = document.createElement("div");
    actions.className = "modal-actions";
    const back = document.createElement("button");
    back.type = "button";
    back.className = "btn btn-secondary";
    back.textContent = "Назад";
    back.addEventListener("click", () => {
      closeDialog(dialog);
      openImportDialog();
    });
    const apply = document.createElement("button");
    apply.type = "submit";
    apply.className = "btn";
    apply.textContent = "Применить";
    if (!matches.length) apply.disabled = true;
    actions.append(back, apply);
    dform.appendChild(actions);

    dform.addEventListener("submit", (event) => {
      event.preventDefault();
      const assignments: Array<{ teamId: number; number: number }> = [];
      const usedTeams = new Set<number>();
      for (const row of rowEls) {
        const val = row.select.value;
        if (!val) continue;
        const teamId = Number(val);
        if (usedTeams.has(teamId)) {
          err.textContent = "Одна команда выбрана несколько раз — поправьте сопоставление.";
          err.hidden = false;
          return;
        }
        usedTeams.add(teamId);
        assignments.push({ teamId, number: row.number });
      }
      if (!assignments.length) {
        err.textContent = "Нет ни одного подтверждённого сопоставления.";
        err.hidden = false;
        return;
      }
      apply.disabled = true;
      err.hidden = true;
      fetch(`${baseAction}/import/apply`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ assignments }),
      })
        .then((resp) => resp.json() as Promise<ImportApplyResponse>)
        .then((res) => {
          if (!res.ok) throw new Error(res.error || "Не удалось сохранить.");
          location.reload();
        })
        .catch((e: unknown) => {
          apply.disabled = false;
          err.textContent = (e instanceof Error && e.message) || "Не удалось сохранить.";
          err.hidden = false;
        });
    });

    dialog.appendChild(dform);
    dialog.addEventListener("close", () => dialog.remove());
    document.body.appendChild(dialog);
    dialog.showModal();
  };

  if (importBtn) importBtn.addEventListener("click", openImportDialog);
})();

export {};
