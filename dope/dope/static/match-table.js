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

  function createNodeIndex(root, specs) {
    const maps = new Map();
    for (const spec of specs || []) {
      const map = new Map();
      root.querySelectorAll(spec.selector).forEach((node) => {
        map.set(indexKeyFromDataset(node.dataset, spec.keys), node);
      });
      maps.set(spec.name, {keys: spec.keys, map});
    }
    return {
      get(name, values = {}) {
        const entry = maps.get(name);
        if (!entry) return null;
        return entry.map.get(indexKeyFromValues(values, entry.keys)) || null;
      },
    };
  }

  function createScoreTableIndex(root, options = {}) {
    const entity = options.entity || "team";
    const prefix = options.matchScoped ? ["matchCode"] : [];
    const themeKeys = prefix.concat([entity], options.shootout ? ["shootout"] : [], ["theme"]);
    const specs = [
      {name: "answer", selector: ".answer-cell", keys: themeKeys.concat(["answer"])},
      {name: "themeScore", selector: ".theme-score", keys: themeKeys},
      {name: "total", selector: ".total-cell", keys: prefix.concat([entity])},
      {name: "place", selector: ".place-cell", keys: prefix.concat([entity])},
      {name: "input", selector: ".venue-input", keys: prefix.concat([entity])},
      {name: "placeInput", selector: ".place-input", keys: prefix.concat([entity])},
      {name: "plus", selector: ".plus-cell", keys: prefix.concat([entity])},
      {name: "tiebreak", selector: ".tiebreak-cell", keys: prefix.concat([entity])},
      {name: "correctCount", selector: ".correct-count-cell", keys: prefix.concat([entity], ["valueIndex"])},
      {name: "playerSelect", selector: ".player-select", keys: themeKeys},
    ];
    return createNodeIndex(root, specs.concat(options.extraSpecs || []));
  }

  function indexKeyFromDataset(dataset, keys) {
    const values = {};
    for (const key of keys) values[key] = dataset[key];
    return indexKeyFromValues(values, keys);
  }

  function indexKeyFromValues(values, keys) {
    return keys.map((key) => String(values[key] ?? "")).join("\u001f");
  }

  function setNodeText(node, value, formatter = String) {
    if (!node) return;
    const text = formatter(value);
    if (node.textContent !== text) node.textContent = text;
  }

  function setMarkClass(node, mark) {
    if (!node) return;
    node.classList.remove("right", "wrong");
    if (mark) node.classList.add(mark);
  }

  function parseScopedEvent(raw) {
    const parsed = JSON.parse(raw);
    if (parsed && typeof parsed.scope === "string" && Object.prototype.hasOwnProperty.call(parsed, "data")) {
      return parsed;
    }
    return {scope: "unknown", revision: 0, data: parsed};
  }

  function createStateSync(options) {
    const debounceMs = Number.isFinite(options.debounceMs) ? options.debounceMs : 250;
    const maxEchoes = Number.isFinite(options.maxEchoes) ? options.maxEchoes : 12;
    const setSyncStatus = options.setStatus || (() => {});
    const echoSet = new Set();
    const echoOrder = [];
    let saveTimer = null;
    let saveQueued = false;
    let saveInFlight = false;

    function save() {
      if (options.readonly) return;
      saveQueued = true;
      setSyncStatus("saving");
      scheduleSave(debounceMs);
    }

    function scheduleSave(delay) {
      window.clearTimeout(saveTimer);
      saveTimer = window.setTimeout(() => {
        saveTimer = null;
        void flushSave();
      }, delay);
    }

    async function flushSave() {
      if (options.readonly || saveInFlight || !saveQueued) return;
      saveQueued = false;
      saveInFlight = true;
      let saved = false;
      try {
        const raw = JSON.stringify(options.getState());
        rememberLocalEcho(raw);
        const response = await fetch(options.stateURL, {
          method: "PUT",
          headers: {"Content-Type": "application/json"},
          body: raw,
        });
        if (!response.ok) throw new Error(await response.text());
        saved = true;
      } catch (error) {
        console.error(error);
        setSyncStatus("error");
      } finally {
        saveInFlight = false;
        if (saveQueued) {
          if (!saveTimer) scheduleSave(0);
        } else if (saved) {
          setSyncStatus("saved");
        }
      }
    }

    function rememberLocalEcho(raw) {
      echoSet.add(raw);
      echoOrder.push(raw);
      while (echoOrder.length > maxEchoes) {
        echoSet.delete(echoOrder.shift());
      }
    }

    function consumeLocalEcho(raw) {
      if (!echoSet.has(raw)) return false;
      echoSet.delete(raw);
      const index = echoOrder.indexOf(raw);
      if (index >= 0) echoOrder.splice(index, 1);
      return true;
    }

    function hasPendingSave() {
      return saveQueued || saveInFlight || saveTimer !== null;
    }

    function connect() {
      const events = new EventSource(options.eventsURL);
      events.addEventListener("state", (event) => {
        let message;
        try {
          message = parseScopedEvent(event.data);
        } catch (_error) {
          return;
        }
        if (message.scope !== options.scope) return;
        const raw = JSON.stringify(message.data);
        if (consumeLocalEcho(raw)) {
          if (!hasPendingSave()) setSyncStatus("saved");
          return;
        }
        options.onRemoteState?.(message.data, message);
        if (!hasPendingSave()) setSyncStatus("saved");
      });
      events.onerror = () => setSyncStatus("reconnecting");
      return events;
    }

    return {connect, flushSave, hasPendingSave, save};
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
    createNodeIndex,
    createScoreTableIndex,
    setNodeText,
    setMarkClass,
    parseScopedEvent,
    createStateSync,
  };
})();
