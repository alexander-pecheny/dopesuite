(function () {
  function th(content, className = "", attrs = {}) {
    return cell("th", content, className, attrs);
  }

  function td(content, className = "", attrs = {}) {
    return cell("td", content, className, attrs);
  }

  function cell(tagName, content, className = "", attrs = {}) {
    const node = document.createElement(tagName);
    if (className) node.className = className;
    setContent(node, content);
    applyAttrs(node, attrs);
    return node;
  }

  function cellFromSpec(tagName, spec, defaults = {}) {
    if (spec instanceof Node) return spec;
    if (spec?.node instanceof Node) return spec.node;
    if (spec === undefined || spec === null || typeof spec !== "object" || Array.isArray(spec)) {
      return cell(tagName, spec ?? "", defaults.className || "", defaults.attrs || {});
    }
    const node = cell(
      tagName,
      Object.prototype.hasOwnProperty.call(spec, "content") ? spec.content : "",
      spec.className ?? defaults.className ?? "",
      spec.attrs || defaults.attrs || {},
    );
    if (spec.dataset) applyDataset(node, spec.dataset);
    return node;
  }

  function setContent(node, content) {
    if (content instanceof Node) {
      node.appendChild(content);
      return;
    }
    if (Array.isArray(content)) {
      for (const item of content) {
        if (item instanceof Node) node.appendChild(item);
        else node.appendChild(document.createTextNode(item == null ? "" : String(item)));
      }
      return;
    }
    node.textContent = content == null ? "" : String(content);
  }

  function applyAttrs(node, attrs = {}) {
    if (!attrs) return;
    const {dataset, ...rest} = attrs;
    Object.assign(node, rest);
    if (dataset) applyDataset(node, dataset);
  }

  function applyDataset(node, dataset = {}) {
    for (const [key, value] of Object.entries(dataset)) {
      node.dataset[key] = String(value);
    }
  }

  function option(value, label) {
    const node = document.createElement("option");
    node.value = value;
    node.textContent = label;
    return node;
  }

  function buildFlatScoreTable(options) {
    const table = document.createElement("table");
    table.className = options.className || "match-table compact-score-table";
    applyAttrs(table, options.attrs);
    for (const [eventName, handler] of Object.entries(options.events || {})) {
      table.addEventListener(eventName, handler);
    }

    const themes = options.themes || [];
    const thead = document.createElement("thead");
    const header = document.createElement("tr");
    header.appendChild(cellFromSpec("th", options.nameHeader, {className: "sticky sticky-name battle"}));
    header.appendChild(cellFromSpec("th", options.totalHeader ?? "Σ", {className: "sticky sticky-total number"}));
    header.appendChild(cellFromSpec("th", options.placeHeader ?? "М", {className: "sticky sticky-place number"}));
    header.appendChild(cellFromSpec("th", options.placeGapHeader ?? "", {className: "sticky sticky-place-gap place-gap-head"}));

    for (const theme of themes) {
      const questionClass = theme.questionClassName || options.questionClassName || "question-head";
      for (const label of theme.questionLabels || []) {
        header.appendChild(th(label, questionClass));
      }
      header.appendChild(th(theme.label ?? "", theme.labelClassName || options.themeHeaderClassName || "theme-head"));
      header.appendChild(th("", theme.gapHeaderClassName || options.gapHeaderClassName || "gap-head"));
    }
    thead.appendChild(header);
    table.appendChild(thead);

    const tbody = document.createElement("tbody");
    const rows = options.rows || [];
    const colSpan = options.gapColSpan || 4 + themes.reduce((sum, theme) => sum + (theme.questionLabels?.length || 0) + 2, 0);
    rows.forEach((rowSpec, rowIndex) => {
      const row = document.createElement("tr");
      row.appendChild(cellFromSpec("td", rowSpec.nameCell, {className: "sticky sticky-name team-name"}));
      row.appendChild(cellFromSpec("td", rowSpec.totalCell ?? rowSpec.total, {className: "sticky sticky-total number total-cell"}));
      row.appendChild(cellFromSpec("td", rowSpec.placeCell ?? rowSpec.place, {className: "sticky sticky-place number place-cell"}));
      row.appendChild(cellFromSpec("td", rowSpec.placeGapCell ?? "", {className: "sticky sticky-place-gap place-gap"}));

      (rowSpec.themes || []).forEach((themeSpec, themeIndex) => {
        for (const answerCell of themeSpec.answers || []) {
          row.appendChild(cellFromSpec("td", answerCell, {className: "answer-cell theme-block"}));
        }
        row.appendChild(cellFromSpec("td", themeSpec.scoreCell ?? themeSpec.score, {
          className: "number theme-score theme-block theme-block-score",
        }));
        const theme = themes[themeIndex] || {};
        row.appendChild(cellFromSpec("td", themeSpec.gapCell ?? "", {
          className: themeSpec.gapClassName || theme.gapClassName || options.gapClassName || "gap",
        }));
      });
      tbody.appendChild(row);

      if (options.gapRows !== false && rowIndex < rows.length - 1) {
        const gapRow = document.createElement("tr");
        if (options.gapRowClassName) gapRow.className = options.gapRowClassName;
        gapRow.appendChild(td("", options.gapCellClassName || "team-gap", {colSpan}));
        tbody.appendChild(gapRow);
      }
    });
    table.appendChild(tbody);
    return table;
  }

  function buildTwoRowScoreTable(options) {
    const table = document.createElement("table");
    table.className = options.className || "match-table";
    applyAttrs(table, options.attrs);
    for (const [eventName, handler] of Object.entries(options.events || {})) {
      table.addEventListener(eventName, handler);
    }

    const themes = options.themes || [];
    const afterThemeHeaders = options.afterThemeHeaders || [];
    const thead = document.createElement("thead");
    const header = document.createElement("tr");
    header.appendChild(cellFromSpec("th", options.nameHeader, {className: "sticky sticky-name battle"}));
    header.appendChild(cellFromSpec("th", options.totalHeader ?? "Σ", {className: "sticky sticky-total number"}));
    header.appendChild(cellFromSpec("th", options.placeHeader ?? "М", {className: "sticky sticky-place number"}));
    header.appendChild(cellFromSpec("th", options.placeGapHeader ?? "", {className: "sticky sticky-place-gap place-gap-head"}));

    for (const theme of themes) {
      const questionClass = theme.questionClassName || options.questionClassName || "question-head";
      for (const label of theme.questionLabels || []) {
        header.appendChild(th(label, questionClass));
      }
      header.appendChild(th(theme.label ?? "", theme.labelClassName || options.themeHeaderClassName || "theme-head"));
      header.appendChild(th("", theme.gapHeaderClassName || options.gapHeaderClassName || "gap-head"));
    }
    for (const headerCell of afterThemeHeaders) {
      header.appendChild(cellFromSpec("th", headerCell));
    }
    thead.appendChild(header);
    table.appendChild(thead);

    const tbody = document.createElement("tbody");
    const colSpan = options.gapColSpan || 4 +
      themes.reduce((sum, theme) => sum + (theme.questionLabels?.length || 0) + 2, 0) +
      afterThemeHeaders.length;
    const rows = options.rows || [];
    rows.forEach((rowSpec, rowIndex) => {
      const topRow = document.createElement("tr");
      const answerRow = document.createElement("tr");
      answerRow.className = rowSpec.answerRowClassName || options.answerRowClassName || "answer-row";

      topRow.appendChild(cellFromSpec("td", rowSpec.nameCell, {className: "sticky sticky-name team-name", attrs: {rowSpan: 2}}));
      topRow.appendChild(cellFromSpec("td", rowSpec.totalCell ?? rowSpec.total, {className: "sticky sticky-total number total-cell", attrs: {rowSpan: 2}}));
      topRow.appendChild(cellFromSpec("td", rowSpec.placeCell ?? rowSpec.place, {className: "sticky sticky-place number place-cell", attrs: {rowSpan: 2}}));
      topRow.appendChild(cellFromSpec("td", rowSpec.placeGapCell ?? "", {className: "sticky sticky-place-gap place-gap", attrs: {rowSpan: 2}}));

      (rowSpec.themes || []).forEach((themeSpec, themeIndex) => {
        const theme = themes[themeIndex] || {};
        const questionCount = theme.questionLabels?.length || 0;
        topRow.appendChild(cellFromSpec("td", themeSpec.playerCell ?? "", {
          className: "player-cell theme-block theme-block-top-left",
          attrs: {colSpan: questionCount},
        }));
        topRow.appendChild(cellFromSpec("td", themeSpec.scoreCell ?? themeSpec.score, {
          className: "number theme-score theme-block theme-block-score",
          attrs: {rowSpan: 2},
        }));
        topRow.appendChild(cellFromSpec("td", themeSpec.gapCell ?? "", {
          className: themeSpec.gapClassName || theme.gapClassName || options.gapClassName || "gap",
        }));

        for (const answerCell of themeSpec.answers || []) {
          answerRow.appendChild(cellFromSpec("td", answerCell, {className: "answer-cell theme-block"}));
        }
        answerRow.appendChild(cellFromSpec("td", themeSpec.answerGapCell ?? "", {
          className: themeSpec.gapClassName || theme.gapClassName || options.gapClassName || "gap",
        }));
      });

      for (const extraCell of rowSpec.afterThemeCells || []) {
        topRow.appendChild(cellFromSpec("td", extraCell));
      }

      tbody.appendChild(topRow);
      tbody.appendChild(answerRow);
      if (options.gapRows !== false && rowIndex < rows.length - 1) {
        const gapRow = document.createElement("tr");
        if (options.gapRowClassName) gapRow.className = options.gapRowClassName;
        gapRow.appendChild(td("", options.gapCellClassName || "team-gap", {colSpan}));
        tbody.appendChild(gapRow);
      }
    });
    table.appendChild(tbody);
    return table;
  }

  function computePlaces(totals) {
    const sorted = totals.map((total, index) => ({total, index})).sort((a, b) => b.total - a.total);
    const places = new Array(totals.length).fill("");
    let i = 0;
    while (i < sorted.length) {
      let j = i;
      while (j + 1 < sorted.length && sorted[j + 1].total === sorted[i].total) j++;
      const label = i === j ? String(i + 1) : `${i + 1}–${j + 1}`;
      for (let k = i; k <= j; k++) places[sorted[k].index] = label;
      i = j + 1;
    }
    return places;
  }

  function setText(root, selector, value, formatter = String) {
    const node = root.querySelector(selector);
    if (node) node.textContent = formatter(value);
  }

  function isFormControl(target) {
    return target instanceof HTMLInputElement ||
      target instanceof HTMLSelectElement ||
      target instanceof HTMLTextAreaElement ||
      target instanceof HTMLButtonElement;
  }

  function clamp(value, min, max) {
    return Math.max(min, Math.min(max, value));
  }

  function sameArray(a, b) {
    if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) return false;
    for (let i = 0; i < a.length; i++) {
      if (a[i] !== b[i]) return false;
    }
    return true;
  }

  function cssEscape(value) {
    return window.CSS?.escape ? CSS.escape(value) : String(value).replace(/["\\]/g, "\\$&");
  }

  window.DopeTable = {
    th,
    td,
    option,
    buildFlatScoreTable,
    buildTwoRowScoreTable,
    computePlaces,
    setText,
    isFormControl,
    clamp,
    sameArray,
    cssEscape,
  };
})();
