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
    const showPlaceColumn = options.placeColumn !== false;
    const showRowMarker = Boolean(options.rowMarkerColumn);
    const thead = document.createElement("thead");
    const header = document.createElement("tr");
    if (showRowMarker) {
      header.appendChild(cellFromSpec("th", options.rowMarkerHeader ?? "", {
        className: options.rowMarkerHeaderClassName || "sticky row-marker row-marker-head",
      }));
    }
    header.appendChild(cellFromSpec("th", options.nameHeader, {className: "sticky sticky-name battle"}));
    header.appendChild(cellFromSpec("th", options.totalHeader ?? "Σ", {className: "sticky sticky-total number"}));
    if (showPlaceColumn) {
      header.appendChild(cellFromSpec("th", options.placeHeader ?? "М", {className: "sticky sticky-place number"}));
      header.appendChild(cellFromSpec("th", options.placeGapHeader ?? "", {className: "sticky sticky-place-gap place-gap-head"}));
    }

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
    const leadingColumnCount = (showRowMarker ? 1 : 0) + (showPlaceColumn ? 4 : 2);
    const colSpan = options.gapColSpan || leadingColumnCount +
      themes.reduce((sum, theme) => sum + (theme.questionLabels?.length || 0) + 2, 0);
    rows.forEach((rowSpec, rowIndex) => {
      const row = document.createElement("tr");
      if (rowSpec.rowClassName) row.className = rowSpec.rowClassName;
      if (showRowMarker) {
        row.appendChild(cellFromSpec("td", rowSpec.rowMarkerCell ?? "", {
          className: rowSpec.rowMarkerClassName || options.rowMarkerCellClassName || "sticky row-marker",
        }));
      }
      row.appendChild(cellFromSpec("td", rowSpec.nameCell, {className: "sticky sticky-name team-name"}));
      row.appendChild(cellFromSpec("td", rowSpec.totalCell ?? rowSpec.total, {className: "sticky sticky-total number total-cell"}));
      if (showPlaceColumn) {
        row.appendChild(cellFromSpec("td", rowSpec.placeCell ?? rowSpec.place, {className: "sticky sticky-place number place-cell"}));
        row.appendChild(cellFromSpec("td", rowSpec.placeGapCell ?? "", {className: "sticky sticky-place-gap place-gap"}));
      }

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
    const showPlaceColumn = options.placeColumn !== false;
    const showRowMarker = Boolean(options.rowMarkerColumn);
    const thead = document.createElement("thead");
    const header = document.createElement("tr");
    if (showRowMarker) {
      header.appendChild(cellFromSpec("th", options.rowMarkerHeader ?? "", {
        className: options.rowMarkerHeaderClassName || "sticky row-marker row-marker-head",
      }));
    }
    header.appendChild(cellFromSpec("th", options.nameHeader, {className: "sticky sticky-name battle"}));
    header.appendChild(cellFromSpec("th", options.totalHeader ?? "Σ", {className: "sticky sticky-total number"}));
    if (showPlaceColumn) {
      header.appendChild(cellFromSpec("th", options.placeHeader ?? "М", {className: "sticky sticky-place number"}));
      header.appendChild(cellFromSpec("th", options.placeGapHeader ?? "", {className: "sticky sticky-place-gap place-gap-head"}));
    }

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
    const leadingColumnCount = (showRowMarker ? 1 : 0) + (showPlaceColumn ? 4 : 2);
    const colSpan = options.gapColSpan || leadingColumnCount +
      themes.reduce((sum, theme) => sum + (theme.questionLabels?.length || 0) + 2, 0) +
      afterThemeHeaders.length;
    const rows = options.rows || [];
    rows.forEach((rowSpec, rowIndex) => {
      const topRow = document.createElement("tr");
      const answerRow = document.createElement("tr");
      const rowClassName = rowSpec.rowClassName || "";
      if (rowClassName) topRow.className = rowClassName;
      answerRow.className = [
        rowSpec.answerRowClassName || options.answerRowClassName || "answer-row",
        rowClassName,
      ].filter(Boolean).join(" ");

      if (showRowMarker) {
        topRow.appendChild(cellFromSpec("td", rowSpec.rowMarkerCell ?? "", {
          className: rowSpec.rowMarkerClassName || options.rowMarkerCellClassName || "sticky row-marker",
          attrs: {rowSpan: 2},
        }));
      }
      topRow.appendChild(cellFromSpec("td", rowSpec.nameCell, {className: "sticky sticky-name team-name", attrs: {rowSpan: 2}}));
      topRow.appendChild(cellFromSpec("td", rowSpec.totalCell ?? rowSpec.total, {className: "sticky sticky-total number total-cell", attrs: {rowSpan: 2}}));
      if (showPlaceColumn) {
        topRow.appendChild(cellFromSpec("td", rowSpec.placeCell ?? rowSpec.place, {className: "sticky sticky-place number place-cell", attrs: {rowSpan: 2}}));
        topRow.appendChild(cellFromSpec("td", rowSpec.placeGapCell ?? "", {className: "sticky sticky-place-gap place-gap", attrs: {rowSpan: 2}}));
      }

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
    let patchTimer = null;
    let patchInFlight = false;
    let patchQueue = new Map();
    let inFlightPatchOps = [];

    function save() {
      if (options.readonly) return;
      saveQueued = true;
      setSyncStatus("saving");
      scheduleSave(debounceMs);
    }

    function patch(path, value) {
      if (options.readonly) return;
      let op;
      try {
        op = {op: "set", path: normalizePatchPath(path), value: cloneJSON(value)};
      } catch (error) {
        console.error(error);
        setSyncStatus("error");
        return;
      }
      patchQueue.set(patchKey(op), op);
      setSyncStatus("saving");
      schedulePatch(debounceMs);
    }

    function scheduleSave(delay) {
      window.clearTimeout(saveTimer);
      saveTimer = window.setTimeout(() => {
        saveTimer = null;
        void flushSave();
      }, delay);
    }

    function schedulePatch(delay) {
      window.clearTimeout(patchTimer);
      patchTimer = window.setTimeout(() => {
        patchTimer = null;
        void flushPatch();
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

    async function flushPatch() {
      if (options.readonly || patchInFlight || patchQueue.size === 0) return;
      const ops = Array.from(patchQueue.values());
      patchQueue.clear();
      patchInFlight = true;
      inFlightPatchOps = inFlightPatchOps.concat(ops);
      let saved = false;
      let retry = true;
      try {
        const response = await fetch(options.stateURL, {
          method: "PATCH",
          headers: {"Content-Type": "application/json"},
          body: JSON.stringify({ops}),
        });
        if (!response.ok) {
          retry = response.status >= 500;
          throw new Error(await response.text());
        }
        const updated = await response.json();
        removeInFlightPatchOps(ops);
        rememberLocalEcho(JSON.stringify(updated));
        options.onRemoteState?.(withPendingLocalPatches(updated), {local: true});
        saved = true;
      } catch (error) {
        removeInFlightPatchOps(ops);
        if (retry) requeuePatchOps(ops);
        console.error(error);
        setSyncStatus("error");
      } finally {
        patchInFlight = false;
        if (patchQueue.size > 0) {
          if (!patchTimer) schedulePatch(saved ? 0 : 2000);
        } else if (saved && !hasPendingSave()) {
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
      return saveQueued ||
        saveInFlight ||
        saveTimer !== null ||
        patchQueue.size > 0 ||
        patchInFlight ||
        patchTimer !== null ||
        inFlightPatchOps.length > 0;
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
        options.onRemoteState?.(withPendingLocalPatches(message.data), message);
        if (!hasPendingSave()) setSyncStatus("saved");
      });
      events.onerror = () => setSyncStatus("reconnecting");
      return events;
    }

    function normalizePatchPath(path) {
      if (!Array.isArray(path) || path.length === 0) {
        throw new Error("state patch path must be a non-empty array");
      }
      return path.map((segment) => {
        if (typeof segment === "string" && segment !== "") return segment;
        if (Number.isInteger(segment) && segment >= 0) return segment;
        throw new Error("state patch path segments must be strings or non-negative integers");
      });
    }

    function patchKey(op) {
      return JSON.stringify(op.path);
    }

    function cloneJSON(value) {
      if (value === undefined) return null;
      return JSON.parse(JSON.stringify(value));
    }

    function removeInFlightPatchOps(ops) {
      const sent = new Set(ops);
      inFlightPatchOps = inFlightPatchOps.filter((op) => !sent.has(op));
    }

    function requeuePatchOps(ops) {
      for (const op of ops) {
        const key = patchKey(op);
        if (!patchQueue.has(key)) patchQueue.set(key, op);
      }
    }

    function pendingPatchOps() {
      return inFlightPatchOps.concat(Array.from(patchQueue.values()));
    }

    function withPendingLocalPatches(remoteState) {
      let next = cloneJSON(remoteState);
      for (const op of pendingPatchOps()) {
        next = applySetPatch(next, op.path, op.value);
      }
      return next;
    }

    function applySetPatch(root, path, value) {
      if (path.length === 0) return cloneJSON(value);
      const [segment, ...rest] = path;
      if (typeof segment === "number") {
        const arr = Array.isArray(root) ? root : [];
        while (arr.length <= segment) arr.push(null);
        arr[segment] = applySetPatch(arr[segment], rest, value);
        return arr;
      }
      const obj = root && typeof root === "object" && !Array.isArray(root) ? root : {};
      obj[segment] = applySetPatch(obj[segment], rest, value);
      return obj;
    }

    return {connect, flushSave, flushPatch, hasPendingSave, save, patch};
  }

  function createHostPresence(options) {
    const root = options.root || document.body;
    const postDelayMs = Number.isFinite(options.postDelayMs) ? options.postDelayMs : 80;
    const heartbeatMs = Number.isFinite(options.heartbeatMs) ? options.heartbeatMs : 5000;
    const staleMs = Number.isFinite(options.staleMs) ? options.staleMs : 16000;
    const remotes = new Map();
    let selfUserID = null;
    let source = null;
    let layer = null;
    let publishTimer = null;
    let heartbeatTimer = null;
    let staleTimer = null;
    let lastCursor = null;
    let connected = false;

    function connect() {
      if (connected || !options.eventsURL || !options.presenceURL) return;
      connected = true;
      ensureLayer();
      void loadSelf();
      source = new EventSource(options.eventsURL);
      source.addEventListener("presence", (event) => {
        try {
          applyPresence(JSON.parse(event.data));
        } catch (error) {
          console.error(error);
        }
      });
      root.addEventListener("focusin", handleFocusOrClick, true);
      root.addEventListener("click", handleFocusOrClick, true);
      document.addEventListener("keydown", handleKeydown, true);
      document.addEventListener("scroll", refresh, true);
      window.addEventListener("scroll", refresh, {passive: true});
      window.addEventListener("resize", refresh);
      window.addEventListener("beforeunload", sendInactive);
      heartbeatTimer = window.setInterval(() => {
        if (lastCursor) void postPresence(true, lastCursor);
      }, heartbeatMs);
      staleTimer = window.setInterval(pruneStale, 1000);
      publishCurrentSoon();
    }

    async function loadSelf() {
      try {
        const response = await fetch("/api/auth/me", {headers: {"Accept": "application/json"}});
        if (!response.ok) return;
        const me = await response.json();
        selfUserID = me.user_id || me.userID || null;
        if (selfUserID && remotes.has(selfUserID)) {
          removeRemote(selfUserID);
        }
      } catch (error) {
        console.error(error);
      }
    }

    function handleFocusOrClick(event) {
      publishFromElement(event.target);
    }

    function handleKeydown() {
      window.requestAnimationFrame(publishCurrent);
    }

    function publishFromElement(element) {
      const cursor = options.cursorFromElement?.(element);
      if (cursor) publish(cursor);
    }

    function publishCurrentSoon() {
      window.requestAnimationFrame(publishCurrent);
    }

    function publishCurrent() {
      const cursor = options.getCursor?.() || options.cursorFromElement?.(document.activeElement);
      if (cursor) publish(cursor);
    }

    function publish(cursor) {
      if (!cursor) return;
      lastCursor = cursor;
      window.clearTimeout(publishTimer);
      publishTimer = window.setTimeout(() => {
        publishTimer = null;
        void postPresence(true, cursor);
      }, postDelayMs);
    }

    async function postPresence(active, cursor) {
      const body = active ? {active: true, cursor} : {active: false};
      try {
        await fetch(options.presenceURL, {
          method: "POST",
          headers: {"Content-Type": "application/json"},
          body: JSON.stringify(body),
        });
      } catch (error) {
        console.error(error);
      }
    }

    function sendInactive() {
      if (!options.presenceURL) return;
      const payload = JSON.stringify({active: false});
      if (navigator.sendBeacon) {
        navigator.sendBeacon(options.presenceURL, new Blob([payload], {type: "application/json"}));
        return;
      }
      void fetch(options.presenceURL, {
        method: "POST",
        headers: {"Content-Type": "application/json"},
        body: payload,
        keepalive: true,
      });
    }

    function applyPresence(message) {
      if (!message || !message.userID) return;
      if (selfUserID && message.userID === selfUserID) return;
      if (!message.active || !message.cursor) {
        removeRemote(message.userID);
        return;
      }
      const remote = remotes.get(message.userID) || {};
      remote.userID = message.userID;
      remote.username = message.username || `user-${message.userID}`;
      remote.color = message.color || "#1a73e8";
      remote.cursor = message.cursor;
      remote.seenAt = Date.now();
      remotes.set(message.userID, remote);
      renderRemote(remote);
    }

    function ensureLayer() {
      if (layer) return layer;
      layer = document.createElement("div");
      layer.className = "collab-cursor-layer";
      document.body.appendChild(layer);
      return layer;
    }

    function renderRemote(remote) {
      ensureLayer();
      const target = options.findTarget?.(remote.cursor);
      const node = ensureRemoteNode(remote);
      if (!target || !document.documentElement.contains(target)) {
        node.hidden = true;
        return;
      }
      const rect = target.getBoundingClientRect();
      if (rect.width <= 0 || rect.height <= 0 || rect.bottom < 0 || rect.right < 0 || rect.top > window.innerHeight || rect.left > window.innerWidth) {
        node.hidden = true;
        return;
      }
      if (isHiddenByScrollFrame(target, rect) || isHiddenByStickyLayer(target, rect)) {
        node.hidden = true;
        return;
      }
      node.hidden = false;
      node.style.left = `${Math.round(rect.left)}px`;
      node.style.top = `${Math.round(rect.top)}px`;
      node.style.width = `${Math.round(rect.width)}px`;
      node.style.height = `${Math.round(rect.height)}px`;
      node.style.setProperty("--cursor-color", remote.color);
      const marker = node.querySelector(".collab-cursor-marker");
      const label = node.querySelector(".collab-cursor-label");
      marker.title = remote.username;
      label.textContent = remote.username;
    }

    function ensureRemoteNode(remote) {
      if (remote.node) return remote.node;
      const node = document.createElement("div");
      node.className = "collab-cursor";
      const marker = document.createElement("span");
      marker.className = "collab-cursor-marker";
      const label = document.createElement("span");
      label.className = "collab-cursor-label";
      marker.appendChild(label);
      node.appendChild(marker);
      layer.appendChild(node);
      remote.node = node;
      return node;
    }

    function isHiddenByScrollFrame(target, rect) {
      const frame = target.closest?.(".sheet-frame");
      if (!frame) return false;
      const frameRect = frame.getBoundingClientRect();
      return rect.left < frameRect.left - 1 ||
        rect.right > frameRect.right + 1 ||
        rect.top < frameRect.top - 1 ||
        rect.bottom > frameRect.bottom + 1;
    }

    function isHiddenByStickyLayer(target, rect) {
      const frame = target.closest?.(".sheet-frame");
      if (!frame || target.closest?.(".sticky")) return false;
      const frameRect = frame.getBoundingClientRect();
      let stickyRight = frameRect.left;
      let stickyBottom = frameRect.top;
      for (const sticky of frame.querySelectorAll(".sticky, thead th")) {
        if (sticky === target || sticky.contains(target) || target.contains(sticky)) continue;
        const style = window.getComputedStyle(sticky);
        if (style.position !== "sticky") continue;
        const stickyRect = sticky.getBoundingClientRect();
        if (stickyRect.width <= 0 || stickyRect.height <= 0) continue;
        if (stickyRect.right <= frameRect.left || stickyRect.left >= frameRect.right || stickyRect.bottom <= frameRect.top || stickyRect.top >= frameRect.bottom) continue;

        const overlapsY = stickyRect.top < rect.bottom && stickyRect.bottom > rect.top;
        const isLeftSticky = style.left !== "auto" && stickyRect.left >= frameRect.left - 2 && stickyRect.left < frameRect.right;
        if (overlapsY && isLeftSticky) {
          stickyRight = Math.max(stickyRight, stickyRect.right);
        }

        const overlapsX = stickyRect.left < rect.right && stickyRect.right > rect.left;
        const isTopSticky = style.top !== "auto" && stickyRect.top >= frameRect.top - 2 && stickyRect.top < frameRect.bottom;
        if (overlapsX && isTopSticky) {
          stickyBottom = Math.max(stickyBottom, stickyRect.bottom);
        }
      }
      return rect.left < stickyRight - 1 || rect.top < stickyBottom - 1;
    }

    function refresh() {
      for (const remote of remotes.values()) {
        renderRemote(remote);
      }
    }

    function pruneStale() {
      const cutoff = Date.now() - staleMs;
      for (const [userID, remote] of remotes.entries()) {
        if (remote.seenAt < cutoff) {
          removeRemote(userID);
        }
      }
    }

    function removeRemote(userID) {
      const remote = remotes.get(userID);
      if (remote?.node) remote.node.remove();
      remotes.delete(userID);
    }

    function disconnect() {
      if (!connected) return;
      connected = false;
      window.clearTimeout(publishTimer);
      window.clearInterval(heartbeatTimer);
      window.clearInterval(staleTimer);
      source?.close();
      source = null;
      sendInactive();
      root.removeEventListener("focusin", handleFocusOrClick, true);
      root.removeEventListener("click", handleFocusOrClick, true);
      document.removeEventListener("keydown", handleKeydown, true);
      document.removeEventListener("scroll", refresh, true);
      window.removeEventListener("scroll", refresh);
      window.removeEventListener("resize", refresh);
      for (const userID of Array.from(remotes.keys())) removeRemote(userID);
    }

    return {connect, disconnect, publish, publishCurrent, publishFromElement, refresh};
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
    createHostPresence,
  };
})();
