// labelsedit.ts — "Labels": the board's label editor. Every label is editable
// (issue #25) — there is no such thing as a test label whose name comes from
// somewhere else (ADR-0004). Like the session form, the editor has no per-row
// Save: Done commits the lot. sortLabels is the board's one ordering of
// labels, shared with the card's add-label popup.

import { xyApp } from "./app.js";
import { xyCrypto } from "./crypto.js";
import { type ColorField, colorField, LABEL_COLORS } from "./colorpick.js";
import { icon } from "./icons_gen.js";
import { modal } from "./modal.js";
import type { Board, BoardPanel } from "./panels.js";
import type { BoardLabel, CardLabel } from "./unlock.js";

const { el, byId, errMsg } = xyApp;

// labelLastUsage maps label id → the highest card id currently carrying it.
// Card ids grow monotonically, so the max id is a recency proxy for "last used"
// without scanning per-card timelines. Labels absent from the map were never
// used (or imported with no assignments).
function labelLastUsage(cardLabels: ReadonlyArray<CardLabel>): Map<number, number> {
  const usage = new Map<number, number>();
  for (const a of cardLabels) {
    const prev = usage.get(a.labelId);
    if (prev === undefined || a.cardId > prev) usage.set(a.labelId, a.cardId);
  }
  return usage;
}

// sortLabels orders by last usage descending; labels with no usage data fall to
// the bottom, ordered alphabetically descending.
export function sortLabels(labels: ReadonlyArray<BoardLabel>, cardLabels: ReadonlyArray<CardLabel>): BoardLabel[] {
  const usage = labelLastUsage(cardLabels);
  return labels.slice().sort((a, b) => {
    const ua = usage.get(a.id), ub = usage.get(b.id);
    const ha = ua !== undefined, hb = ub !== undefined;
    if (ha && hb) return (ub as number) - (ua as number);
    if (ha !== hb) return ha ? -1 : 1;
    return b.name.localeCompare(a.name, "ru");
  });
}

export interface LabelsEditor {
  createLabel(name: string, color: string): Promise<BoardLabel>;
  panel: BoardPanel;
}

export function createLabelsEditor(board: Board): LabelsEditor {
  // Every label is editable (issue #25) — there is no such thing as a test label
  // whose name comes from somewhere else (ADR-0004). Like the session form, the
  // editor has no per-row Save: Done commits the lot.
  interface LabelRow { lbl: BoardLabel; name: HTMLInputElement; color: ColorField }
  let labelRows: LabelRow[] = [];
  let labelDraft: { name: HTMLInputElement; color: ColorField } | null = null;

  // flushLabelsEditor writes whatever the editor is holding — renamed or
  // recoloured rows first, then a name left in the create row. It throws, so the
  // leave gate can keep the modal open on a failure instead of eating the edit.
  async function flushLabelsEditor(): Promise<void> {
    for (const row of labelRows) {
      const name = row.name.value.trim();
      const color = row.color.value();
      // A blanked name is a slip, not a rename: a nameless label is unusable.
      if (!name || (name === row.lbl.name && color === row.lbl.color)) continue;
      await board.verbs.patch("patchLabel", `/api/labels/${row.lbl.id}`, {
        name_enc: await xyCrypto.encField(board.dk(), name),
        color_enc: await xyCrypto.encField(board.dk(), color),
      });
      row.lbl.name = name;
      row.lbl.color = color;
    }
    if (labelDraft && labelDraft.name.value.trim()) {
      await createLabel(labelDraft.name.value.trim(), labelDraft.color.value());
      labelDraft.name.value = "";
    }
  }

  function renderLabelsEditor(focusNew = false): void {
    const box = byId("labelsEditor");
    const usage = labelUsageCounts();
    box.replaceChildren();
    labelRows = [];

    // The card's add-label popup was the only way to make one, so you had to open
    // a card first — and managing labels is what this modal is for.
    const newName = el("input", { class: "input", type: "text", placeholder: "Новая метка" }) as HTMLInputElement;
    const newColor = colorField(el("div"), LABEL_COLORS[0]);
    labelDraft = { name: newName, color: newColor };
    const add = el("button", { class: "input", type: "button", text: "Добавить" });
    // Add is the create affordance, not a save — it commits now so you can
    // type the next one. Leaving with a name still in the box creates it too.
    const submit = async (): Promise<void> => {
      if (!newName.value.trim()) return;
      try {
        await flushLabelsEditor();
        board.render();
        renderLabelsEditor(true);
      } catch (err) { labelsEditModal.message(errMsg(err)); }
    };
    add.addEventListener("click", () => { void submit(); });
    newName.addEventListener("keydown", (e) => {
      if (e.key !== "Enter") return;
      e.preventDefault();
      void submit();
    });
    box.append(el("div", { class: "sess-row" },
      el("div", { class: "sess-head" }, newName),
      el("div", { class: "sess-actions" }, newColor.node, add)));

    if (!board.state.labels.length) box.append(el("p", { class: "label-empty", text: "Меток нет." }));
    for (const lbl of sortLabels(board.state.labels, board.state.cardLabels)) {
      const name = el("input", { class: "input", type: "text", value: lbl.name }) as HTMLInputElement;
      const color = colorField(el("div"), lbl.color);
      const count = el("span", { class: "sess-meta", text: `${usage.get(lbl.id) || 0} карт.` });
      labelRows.push({ lbl, name, color });
      const drop = el("button", { class: "btn btn-danger", type: "button" }, icon("trash-2"));
      drop.addEventListener("click", async () => {
        if (!confirm(`Удалить метку «${lbl.name}»? Она исчезнет со всех карточек.`)) return;
        try {
          // Commit the other rows first — this re-renders, and their edits would
          // go with the old DOM.
          await flushLabelsEditor();
          await board.verbs.del("deleteLabel", `/api/labels/${lbl.id}`);
          board.state.labels = board.state.labels.filter((l) => l.id !== lbl.id);
          board.state.cardLabels = board.state.cardLabels.filter((a) => a.labelId !== lbl.id);
          board.render();
          renderLabelsEditor();
        } catch (err) { labelsEditModal.message(errMsg(err)); }
      });
      box.append(el("div", { class: "sess-row" },
        el("div", { class: "sess-head" }, name, count),
        el("div", { class: "sess-actions" }, color.node, drop)));
    }
    if (focusNew) newName.focus();
  }

  async function leaveLabelsEditor(): Promise<boolean> {
    try {
      await flushLabelsEditor();
      board.render();
      return true;
    } catch (err) {
      labelsEditModal.message(errMsg(err));
      return false;
    }
  }

  const labelsEditModal = modal("labelsEdit");

  function openLabelsEditor(): void {
    renderLabelsEditor();
    labelsEditModal.open({
      onClose: () => { labelRows = []; labelDraft = null; },
      confirm: leaveLabelsEditor,
    });
  }

  // labelUsageCounts: label id → how many live cards carry it, either way.
  function labelUsageCounts(): Map<number, number> {
    const counts = new Map<number, number>();
    const live = new Set(board.state.cards.map((c) => c.id));
    for (const a of board.state.cardLabels) {
      if (!live.has(a.cardId)) continue;
      counts.set(a.labelId, (counts.get(a.labelId) || 0) + 1);
    }
    return counts;
  }

  async function createLabel(name: string, color: string): Promise<BoardLabel> {
    const res = await board.verbs.create("createLabel", `/api/boards/${board.id}/labels`, {
      name_enc: await xyCrypto.encField(board.dk(), name),
      color_enc: await xyCrypto.encField(board.dk(), color),
    });
    const lbl: BoardLabel = { id: res.id as number, name, color };
    board.state.labels.push(lbl);
    return lbl;
  }

  return {
    createLabel,
    panel: {
      id: "labels", menu: "board", icon: "tags",
      label: "Метки",
      title: "Переименовать, перекрасить или удалить метки доски",
      open: openLabelsEditor,
    },
  };
}
