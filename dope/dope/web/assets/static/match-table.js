(function () {
  const MINUS_SIGN = "\u2212";

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
        else node.appendChild(document.createTextNode(formatDisplayText(item)));
      }
      return;
    }
    node.textContent = formatDisplayText(content);
  }

  function formatDisplayText(value) {
    return value == null ? "" : String(value).replace(/^-/, MINUS_SIGN);
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
    node.textContent = formatDisplayText(label);
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
    const rows = options.rows || [];
    const leadingColumnCount = (showRowMarker ? 1 : 0) + (showPlaceColumn ? 4 : 2);
    const colSpan = options.gapColSpan || leadingColumnCount +
      themes.reduce((sum, theme) => sum + (theme.questionLabels?.length || 0) + 2, 0) +
      afterThemeHeaders.length;
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
      for (const extraCell of rowSpec.afterThemeCells || []) {
        row.appendChild(cellFromSpec("td", extraCell));
      }
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

  // computePlaces ranks teams by descending total, labeling ties with a "lo–hi"
  // range (e.g. two teams sharing 2nd both read "2–3"). Pass opts.tiebreaks (a
  // parallel array) plus opts.compareTiebreak(a, b) — returning >0 when `a` ranks
  // below `b` — to split equal totals, as OD does with its shootout result: two
  // teams stay tied only when both total AND tiebreak match. With no comparator
  // it degrades to a pure total-based ranking (EK/KSI).
  function computePlaces(totals, opts = {}) {
    const {tiebreaks = null, compareTiebreak = null} = opts;
    const tiebreakOf = (index) => (tiebreaks ? tiebreaks[index] : null);
    const tied = (a, b) => !compareTiebreak || compareTiebreak(tiebreakOf(a), tiebreakOf(b)) === 0;
    const sorted = totals
      .map((total, index) => ({total, index}))
      .sort((a, b) => {
        if (b.total !== a.total) return b.total - a.total;
        return compareTiebreak ? compareTiebreak(tiebreakOf(a.index), tiebreakOf(b.index)) : 0;
      });
    const places = new Array(totals.length).fill("");
    let i = 0;
    while (i < sorted.length) {
      let j = i;
      while (j + 1 < sorted.length && sorted[j + 1].total === sorted[i].total && tied(sorted[j + 1].index, sorted[i].index)) j++;
      const label = i === j ? String(i + 1) : `${i + 1}–${j + 1}`;
      for (let k = i; k <= j; k++) places[sorted[k].index] = label;
      i = j + 1;
    }
    return places;
  }

  function setText(root, selector, value, formatter = formatDisplayText) {
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
    const list = specs || [];
    const maps = new Map();
    for (const spec of list) {
      const map = new Map();
      root.querySelectorAll(spec.selector).forEach((node) => {
        map.set(indexKeyFromDataset(node.dataset, spec.keys), node);
      });
      maps.set(spec.name, {keys: spec.keys, map});
    }
    return {
      // specs is retained so patchScoreTable can drive the sync from the same
      // single source of truth used to build the index.
      specs: list,
      get(name, values = {}) {
        const entry = maps.get(name);
        if (!entry) return null;
        return entry.map.get(indexKeyFromValues(values, entry.keys)) || null;
      },
      eachNode(name, cb) {
        const entry = maps.get(name);
        if (!entry) return;
        entry.map.forEach((node) => cb(node));
      },
    };
  }

  function createScoreTableIndex(root, options = {}) {
    return createNodeIndex(root, scoreCellSpecs(options).concat(options.extraSpecs || []));
  }

  // scoreTeamOf / scoreThemeOf resolve the MatchView team / theme a built cell
  // refers to, straight from the cell's own data-* coordinates — so a sync needs
  // nothing but the node and the new state.
  function scoreTeamOf(node, matchState) {
    return (matchState.teams || [])[Number(node.dataset.team)] || null;
  }

  function scoreThemeOf(node, matchState) {
    const team = scoreTeamOf(node, matchState);
    if (!team) return null;
    const themes = node.dataset.shootout === "1" ? team.shootoutThemes : team.themes;
    return (themes || [])[Number(node.dataset.theme)] || null;
  }

  // scoreCellSpecs is the SINGLE source of truth for the score table's live
  // cells. Each entry says how to find the cell (selector + dataset keys, used to
  // build the index) AND how to keep it in step with a MatchView (sync, used by
  // patchScoreTable). Adding a new live cell means adding one entry here —
  // indexing and the in-place patch both pick it up, so no cell can be rendered
  // but silently left un-synced (the bug this replaced). A spec without a sync is
  // index-only: its value change is handled by a full rebuild (place medals) or
  // it is host-managed out of band (venue input).
  function scoreCellSpecs(options = {}) {
    const entity = options.entity || "team";
    const prefix = options.matchScoped ? ["matchCode"] : [];
    const teamKeys = prefix.concat([entity]);
    const themeKeys = teamKeys.concat(options.shootout ? ["shootout"] : [], ["theme"]);
    return [
      {name: "answer", selector: ".answer-cell", keys: themeKeys.concat(["answer"]),
        sync: (node, ms) => {
          const theme = scoreThemeOf(node, ms);
          if (theme) setMarkClass(node, (theme.answers || [])[Number(node.dataset.answer)]);
        }},
      {name: "themeScore", selector: ".theme-score", keys: themeKeys,
        sync: (node, ms, o) => {
          const theme = scoreThemeOf(node, ms);
          if (theme) setNodeText(node, theme.score, o.formatNumber);
        }},
      // The per-round player shows as read-only text on the viewer and as an
      // editable <select> on the host; each surface has its own spec so both stay
      // live. (Before, only the host's select was patched — the viewer's text was
      // forgotten, so player changes never reached spectators.)
      {name: "playerText", selector: ".readonly-player-text", keys: themeKeys,
        sync: (node, ms) => {
          const theme = scoreThemeOf(node, ms);
          if (!theme) return;
          setNodeText(node, theme.player);
          const popover = node.closest(".readonly-player")?.querySelector(".readonly-player-popover");
          if (popover) setNodeText(popover, theme.player);
        }},
      {name: "playerSelect", selector: ".player-select", keys: themeKeys,
        sync: (node, ms, o) => {
          const theme = scoreThemeOf(node, ms);
          if (!theme || document.activeElement === node) return; // don't clobber an open select
          const value = theme.player || "";
          if (value && !Array.from(node.options).some((opt) => opt.value === value)) {
            node.appendChild(new Option(value, value));
          }
          if (node.value !== value) node.value = value;
          o.onPlayerSelectSynced?.(node);
        }},
      {name: "total", selector: ".total-cell", keys: teamKeys,
        sync: (node, ms, o) => { const t = scoreTeamOf(node, ms); if (t) setNodeText(node, t.total, o.formatNumber); }},
      {name: "plus", selector: ".plus-cell", keys: teamKeys,
        sync: (node, ms, o) => { const t = scoreTeamOf(node, ms); if (t) setNodeText(node, t.plus, o.formatNumber); }},
      {name: "tiebreak", selector: ".tiebreak-cell", keys: teamKeys,
        sync: (node, ms, o) => { const t = scoreTeamOf(node, ms); if (t) setNodeText(node, t.shootoutTotal ?? t.tiebreak, o.formatNumber); }},
      {name: "correctCount", selector: ".correct-count-cell", keys: teamKeys.concat(["valueIndex"]),
        sync: (node, ms, o) => {
          const t = scoreTeamOf(node, ms);
          // Columns render reversed: cell valueIndex i shows correctCounts[4 - i].
          if (t) setNodeText(node, (t.correctCounts || [])[4 - Number(node.dataset.valueIndex)], o.formatNumber);
        }},
      {name: "placeInput", selector: ".place-input", keys: teamKeys,
        sync: (node, ms) => {
          const t = scoreTeamOf(node, ms);
          if (!t) return;
          if (document.activeElement !== node) node.value = formatPlace(t.place);
          node.dataset.committedPlace = String(t.place || 0);
        }},
      // Index-only (no sync): place restyles medal classes and the viewer renders
      // it as text, so a place change forces a rebuild; venue input is host-managed.
      {name: "place", selector: ".place-cell", keys: teamKeys},
      {name: "input", selector: ".venue-input", keys: teamKeys},
    ];
  }

  function indexKeyFromDataset(dataset, keys) {
    const values = {};
    for (const key of keys) values[key] = dataset[key];
    return indexKeyFromValues(values, keys);
  }

  function indexKeyFromValues(values, keys) {
    return keys.map((key) => String(values[key] ?? "")).join("\u001f");
  }

  function setNodeText(node, value, formatter = formatDisplayText) {
    if (!node) return;
    const text = formatter(value);
    if (node.textContent !== text) node.textContent = text;
  }

  function setMarkClass(node, mark) {
    if (!node) return;
    node.classList.remove("right", "wrong");
    if (mark) node.classList.add(mark);
  }

  // canPatchScoreShape reports whether `next` can be patched into a table built
  // for `previous` without a rebuild — i.e. the table SHAPE (team/theme counts,
  // team names, finished flag, question values) is unchanged and only cell
  // VALUES (scores, marks, players, places) differ. Callers add their own extra
  // gates (title, venue, place) for fields their table renders structurally.
  // Shared by the host (editable) and viewer (read-only) so a live edit patches
  // in place instead of tearing down and rebuilding the whole battle.
  function canPatchScoreShape(previous, next) {
    if (!previous || !next) return false;
    if (previous.code !== next.code || previous.finished !== next.finished) return false;
    if (!sameArray(previous.questionValues, next.questionValues)) return false;
    const prevTeams = previous.teams || [];
    const nextTeams = next.teams || [];
    if (prevTeams.length !== nextTeams.length) return false;
    for (let i = 0; i < nextTeams.length; i++) {
      if (prevTeams[i].name !== nextTeams[i].name) return false;
      if ((prevTeams[i].themes || []).length !== (nextTeams[i].themes || []).length) return false;
      if ((prevTeams[i].shootoutThemes || []).length !== (nextTeams[i].shootoutThemes || []).length) return false;
    }
    return true;
  }

  // patchScoreTable updates a built score table in place from a MatchView. It is
  // data-driven: for every spec that declares a `sync` (see scoreCellSpecs), it
  // runs that sync over each indexed cell of that type, each cell reading its own
  // data-* coordinates. Shared verbatim by the host and viewer — whatever cells
  // their tables contain get patched. opts.formatNumber formats numeric text;
  // opts.onPlayerSelectSynced lets the host refresh its select's overflow chrome.
  function patchScoreTable(index, matchState, opts = {}) {
    if (!index || !matchState) return;
    for (const spec of index.specs || []) {
      if (!spec.sync) continue;
      index.eachNode(spec.name, (node) => spec.sync(node, matchState, opts));
    }
  }

  function renderGameBreadcrumbs(root, options = {}) {
    if (!root) return;
    const festTitle = String(options.festTitle || "Фест").trim() || "Фест";
    const gameTitle = String(options.gameTitle || "Игра").trim() || "Игра";
    const currentTitle = String(options.currentTitle || "").trim();
    root.replaceChildren();

    const festLink = document.createElement("a");
    festLink.className = "game-breadcrumbs-fest";
    festLink.href = options.festHref || "/";
    festLink.textContent = festTitle;
    root.appendChild(festLink);

    root.appendChild(breadcrumbSeparator());
    if (options.gameHref && currentTitle && currentTitle !== gameTitle) {
      const gameLink = document.createElement("a");
      gameLink.className = "game-breadcrumbs-game";
      gameLink.href = options.gameHref;
      gameLink.textContent = gameTitle;
      root.appendChild(gameLink);
      root.appendChild(breadcrumbSeparator());
      const current = document.createElement("span");
      current.className = "game-breadcrumbs-current";
      current.textContent = currentTitle;
      root.appendChild(current);
    } else {
      const game = document.createElement("span");
      game.className = "game-breadcrumbs-game";
      game.textContent = currentTitle || gameTitle;
      root.appendChild(game);
    }
  }

  function breadcrumbSeparator() {
    const sep = document.createElement("span");
    sep.className = "game-breadcrumbs-sep";
    sep.textContent = "/";
    sep.setAttribute("aria-hidden", "true");
    return sep;
  }

  function parseScopedEvent(raw) {
    const parsed = JSON.parse(raw);
    if (parsed && typeof parsed.scope === "string" &&
        (Object.prototype.hasOwnProperty.call(parsed, "data") ||
         Object.prototype.hasOwnProperty.call(parsed, "ops"))) {
      return parsed;
    }
    return {scope: "unknown", revision: 0, data: parsed};
  }

  function cloneJSON(value) {
    if (value === undefined) return null;
    return JSON.parse(JSON.stringify(value));
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

  // isPathPrefix reports whether `prefix` is an ancestor-or-equal of `full`
  // (both already normalized, so segments compare strictly). Used so a coarse
  // op marks every cell under the subtree it rewrote.
  function isPathPrefix(prefix, full) {
    if (prefix.length > full.length) return false;
    for (let i = 0; i < prefix.length; i++) {
      if (prefix[i] !== full[i]) return false;
    }
    return true;
  }

  function patchKey(op) {
    return JSON.stringify(op.path);
  }

  // createPendingOps tracks un-acked local edits as scoped set-ops so they can be
  // (a) batched into one request and (b) re-overlaid on top of any server state
  // we render before the edit is confirmed — so an optimistically-applied cell
  // never regresses while its write is in flight, even across a full resync /
  // refetch. Shared by createStateSync (OD/KSI whole-game state) and host.js (EK
  // per-match edits) so all three editors get identical durability.
  //
  // Ops to the same path coalesce, last-write-wins. take() moves the queued batch
  // to "in flight"; ack() drops them once the server confirms; requeue() returns
  // them for retry (without clobbering a newer queued op for the same path);
  // overlay() applies (in-flight then queued) onto a clone of the given state.
  // createPendingOps tracks un-acked edits. With opts.storageKey set (and
  // localStorage available) the un-acked set is also mirrored to localStorage and
  // rehydrated on the next page load, so a refresh/crash mid-sync — exactly when
  // edits "don't apply" and the operator reloads — doesn't silently drop edits
  // the server never confirmed: they reappear (overlaid + spinner) and re-send.
  // Persistence is opt-in (host.js EK pending passes no key) and TTL-bounded so a
  // long-abandoned session can't resurrect ancient edits.
  function createPendingOps(opts) {
    opts = opts || {};
    const ttlMs = Number.isFinite(opts.ttlMs) ? opts.ttlMs : 15 * 60 * 1000;
    let store = null;
    if (opts.storageKey) {
      try {
        store = window.localStorage;
      } catch (_e) {
        store = null;
      }
    }
    const storageKey = store ? opts.storageKey : null;

    let queue = new Map();
    let inFlight = [];

    // persist mirrors the current un-acked set (in-flight + queued) to storage.
    // take() is intentionally not persisted: it only moves ops queued->in-flight,
    // so all() — and thus what we'd write — is unchanged. Best-effort.
    function persist() {
      if (!storageKey) return;
      try {
        const ops = all();
        if (ops.length === 0) store.removeItem(storageKey);
        else store.setItem(storageKey, JSON.stringify(ops));
      } catch (_e) {
        /* quota / serialization — recovery is best-effort, never break editing */
      }
    }

    function add(path, value) {
      const op = {op: "set", path: normalizePatchPath(path), value: cloneJSON(value), ts: pendingTimestamp()};
      queue.set(patchKey(op), op);
      persist();
      return op;
    }
    function take() {
      const ops = Array.from(queue.values());
      queue.clear();
      inFlight = inFlight.concat(ops);
      return ops;
    }
    function ack(ops) {
      const sent = new Set(ops);
      inFlight = inFlight.filter((op) => !sent.has(op));
      persist();
    }
    function requeue(ops) {
      for (const op of ops) {
        const key = patchKey(op);
        if (!queue.has(key)) queue.set(key, op);
      }
      persist();
    }
    function all() {
      return inFlight.concat(Array.from(queue.values()));
    }
    function overlay(state) {
      let next = cloneJSON(state);
      for (const op of all()) next = setAtDeltaPath(next, op.path, op.value);
      return next;
    }
    // has reports whether `path` is covered by an un-acked edit, so the UI can
    // mark that cell pending until the server confirms it. True when a queued/
    // in-flight op targets `path` exactly OR an ANCESTOR of it — so a coarse
    // whole-array patch (e.g. OD's ["entries"]) marks every cell beneath it,
    // while exact-path editors (KSI/EK) behave as a plain equality check.
    function has(path) {
      const norm = normalizePatchPath(path);
      return all().some((op) => isPathPrefix(op.path, norm));
    }

    // Rehydrate un-acked ops persisted by a previous load. Nothing is truly in
    // flight after a reload, so everything re-queues (to be overlaid + re-sent).
    if (storageKey) {
      try {
        const saved = JSON.parse(store.getItem(storageKey) || "[]");
        const now = pendingTimestamp();
        let kept = 0;
        for (const op of Array.isArray(saved) ? saved : []) {
          if (!op || !Array.isArray(op.path)) continue;
          if (op.ts && now - op.ts > ttlMs) continue; // stale — don't resurrect
          queue.set(patchKey(op), {op: "set", path: op.path, value: op.value, ts: op.ts || now});
          kept++;
        }
        persist(); // rewrite without the stale entries we filtered out
        if (kept === 0) {
          try {
            store.removeItem(storageKey);
          } catch (_e) {
            /* ignore */
          }
        }
      } catch (_e) {
        /* corrupt payload — ignore, start clean */
      }
    }

    return {
      add, take, ack, requeue, all, overlay, has,
      queued: () => queue.size,
      inFlightCount: () => inFlight.length,
      size: () => queue.size + inFlight.length,
    };
  }

  function pendingTimestamp() {
    try {
      return Date.now();
    } catch (_e) {
      return 0;
    }
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
    // Editors persist un-acked edits per scope so a mid-sync refresh recovers
    // them; viewers never edit, so they don't (and can't resurrect stray ops).
    const pending = createPendingOps({
      storageKey: !options.readonly && options.scope ? `dope.pending:${options.scope}` : null,
    });
    // Unified SSE protocol: lastSeq is the per-scope position we have applied.
    // A delta applies only if its prevSeq === lastSeq; otherwise a drop / late
    // join / restart left a gap and we resync the full state. Seeded once from
    // the server-rendered initial seq so the first remote edit chains cleanly.
    let lastSeq = 0;
    let lastSeqSeeded = false;
    let resyncing = false;
    // Active SSE stream, plus guards so connect()'s lifecycle listeners bind
    // once and so recovery never re-opens a stream the server locked down.
    let stream = null;
    let lifecycleBound = false;
    let lockedDown = false;
    // lastEpoch is the server's per-process token (see server.epoch). The server
    // resets its per-scope seq to 0 on restart, so without this a long-lived
    // client holding a high lastSeq would read every post-restart delta as
    // "seq <= lastSeq" (already applied) and silently stop syncing — the
    // data-loss incident's amplifier. A changed epoch means the seq space reset,
    // so we resync to adopt the new epoch+seq instead of ignoring the deltas.
    let lastEpoch = "";
    let lastEpochSeeded = false;

    // epochReset adopts the first epoch we see as the baseline and reports a
    // reset only on a genuine change. An empty epoch (older server build) is
    // ignored so the protocol degrades gracefully.
    function epochReset(epoch) {
      if (!epoch) return false;
      if (!lastEpochSeeded) {
        lastEpoch = epoch;
        lastEpochSeeded = true;
        return false;
      }
      return epoch !== lastEpoch;
    }

    // Felt-latency instrumentation. Every sample goes into the client recorder
    // ring (downloadable via the log button), and is mirrored to the console when
    // localStorage["dope.editmetrics"] === "1" so a tester with devtools sees it
    // live. monoNow uses the monotonic clock for the own-edit round-trip (immune
    // to wall-clock jumps); delivery latency necessarily uses Date.now() against
    // the server's emit stamp, so it carries clock skew (rough gauge, not exact).
    const feltConsole = (() => {
      try { return window.localStorage.getItem("dope.editmetrics") === "1"; }
      catch (_e) { return false; }
    })();
    const monoNow = () => (typeof performance !== "undefined" && performance.now
      ? performance.now() : Date.now());
    function feltMetric(type, data) {
      options.recorder?.event(type, data);
      if (feltConsole) {
        try { console.debug(`editmetric ${type} scope=${options.scope}`, data); } catch (_e) {}
      }
    }

    function save() {
      if (options.readonly) return;
      saveQueued = true;
      setSyncStatus("saving");
      scheduleSave(debounceMs);
    }

    function patch(path, value) {
      if (options.readonly) return;
      try {
        pending.add(path, value);
      } catch (error) {
        console.error(error);
        setSyncStatus("error");
        return;
      }
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
      if (options.readonly || patchInFlight || pending.queued() === 0) return;
      const ops = pending.take();
      patchInFlight = true;
      let saved = false;
      let retry = true;
      const tSend = monoNow();
      try {
        const response = await fetch(options.stateURL, {
          method: "PATCH",
          headers: {"Content-Type": "application/json"},
          body: JSON.stringify({ops}),
          // keepalive lets the request complete even if the page is navigating
          // or being backgrounded — without it, edits debounced/in-flight at the
          // moment of a reload are silently dropped. Ops are tiny, well under the
          // 64KB keepalive cap.
          keepalive: true,
        });
        if (!response.ok) {
          retry = response.status >= 500;
          throw new Error(await response.text());
        }
        const updated = await response.json();
        // Own-edit felt latency: keystroke-batch send to server-confirmed (the
        // moment the optimistic cell stops being "pending"). Single clock.
        feltMetric("patch-rtt", {rtt_ms: Math.round(monoNow() - tSend), ops: ops.length, status: response.status});
        pending.ack(ops);
        rememberLocalEcho(JSON.stringify(updated));
        options.onRemoteState?.(pending.overlay(updated), {local: true});
        saved = true;
      } catch (error) {
        pending.ack(ops);
        if (retry) {
          pending.requeue(ops);
        } else {
          // A 4xx means the server rejected these ops and a retry won't help, so
          // they are dropped — but never silently: log them and notify so the
          // loss is visible (in the console, the client recorder, and the sync
          // status) instead of a cell quietly reverting on the next render.
          console.error("dropped rejected patch ops", {error: String(error), ops});
          options.onWriteError?.({kind: "rejected", ops, error: String(error)});
        }
        setSyncStatus("error");
      } finally {
        patchInFlight = false;
        if (pending.queued() > 0) {
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
        patchInFlight ||
        patchTimer !== null ||
        pending.size() > 0;
    }

    function connect() {
      if (!lastSeqSeeded) {
        lastSeq = Number(options.getInitialSeq?.()) || 0;
        lastSeqSeeded = true;
      }
      if (!lastEpochSeeded) {
        const seededEpoch = options.getInitialEpoch?.();
        if (seededEpoch) {
          lastEpoch = String(seededEpoch);
          lastEpochSeeded = true;
        }
      }
      // Un-acked edits recovered from localStorage by createPendingOps (a previous
      // load refreshed mid-sync): show them overlaid on the seeded state right
      // away — with their pending spinner — then re-send (idempotent set-ops).
      if (!options.readonly && pending.queued() > 0) {
        options.recorder?.event("recovered-pending", {scope: options.scope, count: pending.queued()});
        options.onRemoteState?.(pending.overlay(options.getState ? options.getState() : {}), {local: true, recovered: true});
        setSyncStatus("saving");
        schedulePatch(0);
      }
      openStream();
      bindLifecycle();
      return stream;
    }

    // openStream (re)creates the EventSource, closing any prior one. Split from
    // connect() so recovery can re-open the stream without re-seeding seq/epoch,
    // re-running pending recovery, or re-binding lifecycle listeners.
    function openStream() {
      if (stream) {
        try { stream.close(); } catch (_err) { /* already closed */ }
      }
      const events = new EventSource(options.eventsURL);
      stream = events;
      if (options.onViewers) {
        events.addEventListener("viewers", (event) => {
          try {
            options.onViewers(JSON.parse(event.data)?.count);
          } catch (_error) {
            // ignore malformed viewer-count payloads
          }
        });
      }
      events.addEventListener("state", (event) => {
        let message;
        try {
          message = parseScopedEvent(event.data);
        } catch (_error) {
          return;
        }
        if (message.scope !== options.scope) return;

        if (Array.isArray(message.ops)) {
          // Scoped delta: apply the ops in place, but only if they chain onto
          // what we have. A gap means we missed an event, so refetch instead of
          // misapplying. Drop deltas mid-resync; the refetch supersedes them.
          if (resyncing) return;
          // Epoch changed → the server restarted and its seq reset to a low
          // number. Our lastSeq belongs to the dead seq space, so the seq<=lastSeq
          // guard below would silently drop every post-restart delta forever.
          // Resync to adopt the new epoch+seq instead. MUST precede the seq guard.
          if (epochReset(message.epoch)) {
            options.recorder?.event("epoch-change", {scope: options.scope, from: lastEpoch, to: String(message.epoch || ""), seq: Number(message.seq) || 0});
            void resync();
            return;
          }
          // Already applied: a coalesced viewer delta whose seq range we fetched
          // past on connect arrives with seq <= lastSeq. The state already
          // reflects it, so ignore it rather than read the older prevSeq as a gap.
          if ((Number(message.seq) || 0) <= lastSeq) {
            if (!hasPendingSave()) setSyncStatus("saved");
            return;
          }
          if ((Number(message.prevSeq) || 0) !== lastSeq) {
            options.recorder?.event("gap", {scope: options.scope, have: lastSeq, prevSeq: Number(message.prevSeq) || 0, seq: Number(message.seq) || 0});
            void resync();
            return;
          }
          let next = cloneJSON(options.getState ? options.getState() : {});
          for (const op of message.ops) {
            if (op.op && op.op !== "set") continue;
            next = setAtDeltaPath(next, op.path, op.value);
          }
          lastSeq = Number(message.seq) || lastSeq;
          options.recorder?.event("delta", {scope: options.scope, seq: lastSeq, prevSeq: Number(message.prevSeq) || 0, ops: message.ops.length});
          // Delivery leg: server emit (message.emitMs) to this client rendering
          // the delta — the latency a watching co-editor/viewer feels. Carries
          // client/server clock skew; read as a rough gauge.
          if (message.emitMs) {
            feltMetric("delta-latency", {delivery_ms: Date.now() - Number(message.emitMs), seq: lastSeq, ops: message.ops.length});
          }
          options.onRemoteState?.(pending.overlay(next), message);
          if (!hasPendingSave()) setSyncStatus("saved");
          return;
        }

        // Full-state snapshot (initial / wholesale PUT / non-PATCH mutation). It
        // carries the whole state plus its own seq+epoch, so it re-baselines us
        // unconditionally — even across a server restart (changed epoch) there's
        // nothing to resync; we just adopt it.
        const raw = JSON.stringify(message.data);
        if (message.seq) lastSeq = Number(message.seq) || lastSeq;
        if (message.epoch) {
          lastEpoch = String(message.epoch);
          lastEpochSeeded = true;
        }
        options.recorder?.event("snapshot", {scope: options.scope, seq: lastSeq});
        if (consumeLocalEcho(raw)) {
          if (!hasPendingSave()) setSyncStatus("saved");
          return;
        }
        options.onRemoteState?.(pending.overlay(message.data), message);
        if (!hasPendingSave()) setSyncStatus("saved");
      });
      events.addEventListener("lockdown", () => {
        // Server entered static mode: drop the stream so the page reloads into
        // the static snapshot, instead of letting EventSource auto-reconnect.
        // Latch lockedDown so visibility recovery doesn't re-open it meanwhile.
        lockedDown = true;
        events.close();
        options.onLockdown?.();
      });
      events.addEventListener("open", () => options.recorder?.event("sse-open", {scope: options.scope, have: lastSeq}));
      events.onerror = () => {
        setSyncStatus("reconnecting");
        options.recorder?.event("sse-error", {scope: options.scope, have: lastSeq});
      };
    }

    // bindLifecycle wires tab/network listeners exactly once. connect() runs it
    // on first connect; recovery re-opens the stream without touching listeners.
    function bindLifecycle() {
      if (lifecycleBound) return;
      lifecycleBound = true;
      document.addEventListener("visibilitychange", () => {
        // Flush debounced edits the moment the tab is hidden or the page is
        // being navigated away from, so the 250ms debounce window can't swallow
        // the operator's last edits on reload. Paired with keepalive on the
        // PATCH, the flushed request still completes during unload.
        if (document.visibilityState === "hidden") {
          if (patchTimer) { window.clearTimeout(patchTimer); patchTimer = null; }
          if (saveTimer) { window.clearTimeout(saveTimer); saveTimer = null; }
          void flushPatch();
          void flushSave();
          return;
        }
        recoverStream();
      });
      window.addEventListener("pageshow", recoverStream);
      window.addEventListener("online", recoverStream);
    }

    // recoverStream re-opens a dead SSE stream and resyncs. iOS aggressively
    // freezes backgrounded tabs, silently killing the socket; native
    // EventSource auto-reconnect frequently never recovers on resume, leaving
    // the status stuck on a spinning "reconnecting". Guarding on
    // readyState === OPEN keeps a healthy stream from ever being churned, so
    // the steady-state cost is zero — we only act on a genuinely dead stream.
    function recoverStream() {
      if (lockedDown) return;
      if (document.visibilityState !== "visible") return;
      if (stream && stream.readyState === EventSource.OPEN) return;
      options.recorder?.event("sse-recover", {scope: options.scope, readyState: stream?.readyState ?? null});
      openStream();
      void resync();
    }

    // resync refetches the full state after a gap and realigns lastSeq from the
    // X-State-Seq header so the next delta chains. Jittered so a fleet of viewers
    // that all gap on the same dropped event don't refetch in lockstep.
    async function resync() {
      if (resyncing || !options.stateURL) return;
      resyncing = true;
      try {
        await new Promise((r) => window.setTimeout(r, Math.floor(Math.random() * 400)));
        const response = await fetch(options.stateURL);
        if (!response.ok) return;
        const seqHeader = response.headers.get("X-State-Seq");
        const epochHeader = response.headers.get("X-State-Epoch");
        const data = await response.json();
        if (seqHeader != null) lastSeq = Number(seqHeader) || 0;
        // Adopt the server's current epoch so post-resync deltas chain instead of
        // re-triggering an epoch reset every event.
        if (epochHeader) {
          lastEpoch = epochHeader;
          lastEpochSeeded = true;
        }
        options.recorder?.event("resync", {scope: options.scope, seq: lastSeq, epoch: lastEpoch});
        options.onRemoteState?.(pending.overlay(data), {scope: options.scope, resync: true});
        if (!hasPendingSave()) setSyncStatus("saved");
      } catch (error) {
        console.error(error);
      } finally {
        resyncing = false;
      }
    }

    return {connect, flushSave, flushPatch, hasPendingSave, save, patch, isPending: (path) => pending.has(path)};
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
    let refreshFrame = 0;
    let stickyStyleCache = null;

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
      document.addEventListener("scroll", scheduleRefresh, {capture: true, passive: true});
      window.addEventListener("scroll", scheduleRefresh, {passive: true});
      window.addEventListener("resize", scheduleRefresh);
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
      remote.color = message.color || "var(--blue)";
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
      const probes = stickyProbes(frame);
      for (const probe of probes) {
        const sticky = probe.node;
        if (sticky === target || sticky.contains(target) || target.contains(sticky)) continue;
        const style = probe.style;
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

    function scheduleRefresh() {
      if (refreshFrame) return;
      refreshFrame = requestAnimationFrame(() => {
        refreshFrame = 0;
        refresh();
      });
    }

    function refresh() {
      stickyStyleCache = new WeakMap();
      try {
        for (const remote of remotes.values()) {
          renderRemote(remote);
        }
      } finally {
        stickyStyleCache = null;
      }
    }

    function stickyProbes(frame) {
      const nodes = frame.querySelectorAll(".sticky, thead th");
      const out = [];
      const cache = stickyStyleCache;
      for (const node of nodes) {
        let style;
        if (cache) {
          style = cache.get(node);
          if (!style) {
            style = window.getComputedStyle(node);
            cache.set(node, style);
          }
        } else {
          style = window.getComputedStyle(node);
        }
        out.push({node, style});
      }
      return out;
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
      if (refreshFrame) {
        cancelAnimationFrame(refreshFrame);
        refreshFrame = 0;
      }
      source?.close();
      source = null;
      sendInactive();
      root.removeEventListener("focusin", handleFocusOrClick, true);
      root.removeEventListener("click", handleFocusOrClick, true);
      document.removeEventListener("keydown", handleKeydown, true);
      document.removeEventListener("scroll", scheduleRefresh, {capture: true});
      window.removeEventListener("scroll", scheduleRefresh);
      window.removeEventListener("resize", scheduleRefresh);
      for (const userID of Array.from(remotes.keys())) removeRemote(userID);
    }

    return {connect, disconnect, publish, publishCurrent, publishFromElement, refresh: scheduleRefresh};
  }

  function normalizeVenue(venue) {
    if (!venue) return null;
    if (typeof venue === "number" || typeof venue === "string") {
      const number = Number(venue);
      return Number.isFinite(number) && number > 0 ? {number, title: ""} : null;
    }
    const number = Number(venue.number ?? venue.Number);
    if (!Number.isFinite(number) || number <= 0) return null;
    const title = String(venue.title ?? venue.Title ?? "").trim();
    return {number, title};
  }

  function formatVenue(venue) {
    const normalized = normalizeVenue(venue);
    if (!normalized) return "";
    return normalized.title ? `${normalized.number}: ${normalized.title}` : String(normalized.number);
  }

  function formatBattleVenue(venue) {
    const normalized = normalizeVenue(venue);
    if (!normalized) return "";
    return normalized.title ? `пл. ${normalized.number} (${normalized.title})` : `пл. ${normalized.number}`;
  }

  function formatBattleVenueShort(venue) {
    const normalized = normalizeVenue(venue);
    return normalized ? `пл. ${normalized.number}` : "";
  }

  function statusLabel(status) {
    if (status === "finished") return "закончен";
    if (status === "pending") return "ожидает";
    return "активен";
  }

  function formatNumber(value) {
    return Number.isFinite(Number(value)) ? formatDisplayText(value) : "";
  }

  function formatPlace(place) {
    return place > 0 ? String(place) : "";
  }

  function stageType(stage) {
    return stage?.stage_type || stage?.type || "";
  }

  function stageTabLabel(stage) {
    if (stageType(stage) === "reseed") return "Пересев";
    switch (stage.code) {
    case "r16_run1":
      return "1/16-1";
    case "r16_run2":
      return "1/16-2";
    case "r8":
      return "1/8";
    case "r4":
      return "1/4";
    case "r2":
      return "1/2";
    case "final":
      return "Финал";
    default:
      return stage.title || stage.code;
    }
  }

  function teamListCell(teams) {
    const cell = document.createElement("td");
    cell.className = "teams-cell";
    (teams || []).forEach((team) => {
      const row = document.createElement("span");
      row.textContent = team.name;
      cell.appendChild(row);
    });
    return cell;
  }

  function buildVenuesTable(venues, options = {}) {
    const editable = Boolean(options.editable);
    const onTitleChange = typeof options.onTitleChange === "function" ? options.onTitleChange : null;
    const wrapper = document.createElement("div");
    wrapper.className = "results-wrapper venues-results-wrapper";

    const table = document.createElement("table");
    table.className = "results-table venues-results-table";
    const thead = document.createElement("thead");
    const header = document.createElement("tr");
    header.appendChild(th("№", "results-place-head"));
    header.appendChild(th("Название", "results-team-head venues-title-head"));
    thead.appendChild(header);
    table.appendChild(thead);

    const tbody = document.createElement("tbody");
    const list = venues || [];
    list.forEach((venue, index) => {
      const row = document.createElement("tr");
      row.className = "results-row";
      if (index === 0) row.classList.add("results-group-first");
      if (index === list.length - 1) row.classList.add("results-group-last");
      row.appendChild(td(venue.number, "results-place venues-number"));
      const titleCell = document.createElement("td");
      titleCell.className = "results-team venues-title-cell";
      if (editable && onTitleChange) {
        const input = document.createElement("input");
        input.className = "venue-input";
        input.value = venue.title;
        input.dataset.committedTitle = venue.title;
        input.addEventListener("change", () => {
          const title = input.value.trim();
          if (!title) {
            input.value = input.dataset.committedTitle;
            return;
          }
          if (title === input.dataset.committedTitle) return;
          input.dataset.committedTitle = title;
          onTitleChange(venue.number, title);
        });
        titleCell.appendChild(input);
      } else {
        titleCell.textContent = venue.title;
      }
      row.appendChild(titleCell);
      tbody.appendChild(row);
    });
    table.appendChild(tbody);
    wrapper.appendChild(table);
    return wrapper;
  }

  // Roster ("Составы") — the fest-level team→players list, shared by every game
  // page (EK/OD/KSI, host and viewer). The data is the same for all games in a
  // fest, so it is fetched once per festID and cached for the page's lifetime.
  const rosterCache = new Map();

  function fetchFestRoster(festID) {
    if (!festID) return Promise.resolve([]);
    if (rosterCache.has(festID)) return rosterCache.get(festID);
    const promise = fetch(`/api/fest/${encodeURIComponent(festID)}/roster`)
      .then((response) => {
        if (!response.ok) throw new Error(`roster ${response.status}`);
        return response.json();
      })
      .then((data) => (data && Array.isArray(data.teams) ? data.teams : []))
      .catch((err) => {
        // Don't cache a failure — let a later render retry the fetch.
        rosterCache.delete(festID);
        throw err;
      });
    rosterCache.set(festID, promise);
    return promise;
  }

  // rating.chgk.info deep links: team/player names in the Составы view link to
  // their rating pages when a rating id is known.
  const RATING_TEAM_URL = "https://rating.chgk.info/teams/";
  const RATING_PLAYER_URL = "https://rating.chgk.info/players/";

  // rosterNameNode returns a link to `href` (an external rating page) when one is
  // given, otherwise a plain span — both carrying `className` so styling is the
  // same whether or not a rating id was available.
  function rosterNameNode(text, href, className) {
    if (href) {
      const a = document.createElement("a");
      a.className = `${className} quiet-link`;
      a.href = href;
      a.target = "_blank";
      a.rel = "noopener noreferrer";
      a.textContent = text;
      return a;
    }
    const span = document.createElement("span");
    span.className = className;
    span.textContent = text;
    return span;
  }

  // buildRosterTable renders the team→players table using the shared results-table
  // design-system styling. One row per team: number, name (+ city), player list.
  // Team and player names become rating.chgk.info links when a rating id exists.
  function buildRosterTable(teams) {
    const wrapper = document.createElement("div");
    wrapper.className = "results-wrapper roster-results-wrapper";
    const list = teams || [];
    if (list.length === 0) {
      const empty = document.createElement("p");
      empty.className = "roster-empty";
      empty.textContent = "Составы пока не заданы.";
      wrapper.appendChild(empty);
      return wrapper;
    }

    const hasNumbers = list.some((team) => Number(team.number) > 0);
    const table = document.createElement("table");
    table.className = "results-table roster-results-table";

    const thead = document.createElement("thead");
    const header = document.createElement("tr");
    if (hasNumbers) header.appendChild(th("№", "results-place-head"));
    header.appendChild(th("Команда", "results-team-head"));
    header.appendChild(th("Игроки", "roster-players-head"));
    thead.appendChild(header);
    table.appendChild(thead);

    const tbody = document.createElement("tbody");
    list.forEach((team, index) => {
      const row = document.createElement("tr");
      row.className = "results-row";
      if (index === 0) row.classList.add("results-group-first");
      if (index === list.length - 1) row.classList.add("results-group-last");

      if (hasNumbers) {
        row.appendChild(td(Number(team.number) > 0 ? team.number : "", "results-place"));
      }

      const teamCell = document.createElement("td");
      teamCell.className = "results-team roster-team-cell";
      const teamHref = Number(team.ratingID) > 0 ? `${RATING_TEAM_URL}${team.ratingID}` : "";
      // Same DOM shape as buildEKStatsTable's name cell, so the shared
      // fade-on-overflow + hover/focus popover (results-team styling) applies to
      // clipped team names — markNameOverflow toggles .results-team-truncated.
      const nameWrap = document.createElement("span");
      nameWrap.className = "results-team-name-wrap";
      const nameEl = rosterNameNode(team.name || "", teamHref, "results-team-name roster-team-name");
      nameEl.tabIndex = 0;
      nameEl.setAttribute("aria-label", team.name || "");
      nameWrap.appendChild(nameEl);
      teamCell.appendChild(nameWrap);
      const namePopover = document.createElement("span");
      namePopover.className = "popover results-team-name-popover";
      namePopover.textContent = team.name || "";
      teamCell.appendChild(namePopover);
      if (team.city) {
        const city = document.createElement("span");
        city.className = "roster-team-city";
        city.textContent = team.city;
        teamCell.appendChild(city);
      }
      row.appendChild(teamCell);

      const playersCell = document.createElement("td");
      playersCell.className = "roster-players-cell";
      const players = Array.isArray(team.players) ? team.players : [];
      if (players.length === 0) {
        playersCell.classList.add("empty");
        playersCell.textContent = "—";
      } else {
        players.forEach((player) => {
          // Tolerate both the current {name, ratingID} shape and a bare string.
          const info = typeof player === "string" ? {name: player} : (player || {});
          const chip = document.createElement("span");
          chip.className = "roster-player";
          const href = Number(info.ratingID) > 0 ? `${RATING_PLAYER_URL}${info.ratingID}` : "";
          chip.appendChild(rosterNameNode(info.name || "", href, "roster-player-name"));
          playersCell.appendChild(chip);
        });
      }
      row.appendChild(playersCell);
      tbody.appendChild(row);
    });
    table.appendChild(tbody);
    wrapper.appendChild(table);
    return wrapper;
  }

  // buildRosterView returns a container node for the "Составы" tab that fills
  // itself asynchronously: it shows a loading line, fetches the fest roster, then
  // swaps in the table (or an error line on failure). Safe to drop straight into
  // a tab pane by any page — no roster data needs to be threaded through.
  function buildRosterView(festID) {
    const container = document.createElement("div");
    container.className = "roster-view";
    const loading = document.createElement("p");
    loading.className = "roster-empty";
    loading.textContent = "Загрузка составов…";
    container.appendChild(loading);

    fetchFestRoster(festID)
      .then((teams) => {
        container.replaceChildren(buildRosterTable(teams));
        // Flag clipped team names so the shared fade + popover kick in, and
        // re-check whenever the container's width changes (tab switch, resize).
        // The popover itself is already handled: the CSS-only variant on OD/KSI,
        // and the page-bound floating popover on the EK host/viewer roots.
        const remeasure = () => markNameOverflow(container, {
          cellSelector: ".roster-team-cell",
          nameSelector: ".results-team-name",
          truncatedClass: "results-team-truncated",
        });
        requestAnimationFrame(remeasure);
        if (typeof ResizeObserver === "function") {
          new ResizeObserver(remeasure).observe(container);
        }
      })
      .catch(() => {
        const error = document.createElement("p");
        error.className = "roster-empty";
        error.textContent = "Не удалось загрузить составы.";
        container.replaceChildren(error);
      });
    return container;
  }

  // installCellNavBar mounts a floating ↑/↓ bar pinned just above the on-screen
  // keyboard for advancing between editable cells. Mobile numeric keypads
  // (inputmode=numeric/decimal) have no Return key on iOS, so this is the only
  // way to step cell-to-cell without dismissing the keypad. Rendered only on
  // coarse-pointer (touch) devices — on desktop, Enter/Tab already do this.
  //
  // The caller drives visibility with show()/hide(); buttons fire onPrev/onNext
  // on `pointerdown` with the default prevented, so the focused input is never
  // blurred and the keyboard stays up while we programmatically move focus.
  function installCellNavBar(options = {}) {
    const coarse = typeof window.matchMedia === "function" &&
      window.matchMedia("(pointer: coarse)").matches;
    if (!coarse) return {show: () => {}, hide: () => {}};

    const {onPrev, onNext, prevLabel = "▲", nextLabel = "▼"} = options;
    const bar = document.createElement("div");
    bar.className = "entry-nav-bar";
    bar.hidden = true;
    const make = (label, aria, handler) => {
      const button = document.createElement("button");
      button.type = "button";
      button.textContent = label;
      button.setAttribute("aria-label", aria);
      button.addEventListener("pointerdown", (event) => {
        event.preventDefault();
        handler?.();
      });
      return button;
    };
    bar.append(
      make(prevLabel, "Предыдущая ячейка", onPrev),
      make(nextLabel, "Следующая ячейка", onNext),
    );
    document.body.appendChild(bar);

    let visible = false;
    // Pin to the visual viewport's box (see installVirtualKeypad): iOS resolves
    // fixed + right:0 against the document width when the page scrolls
    // horizontally, overflowing the screen and skewing the arrows.
    const position = () => {
      if (!visible) return;
      const vv = window.visualViewport;
      if (vv) {
        bar.style.left = `${Math.round(vv.offsetLeft)}px`;
        bar.style.right = "auto";
        bar.style.width = `${Math.round(vv.width)}px`;
        bar.style.top = `${Math.round(vv.offsetTop + vv.height - bar.offsetHeight)}px`;
        bar.style.bottom = "auto";
      } else {
        bar.style.left = "0px";
        bar.style.right = "0px";
        bar.style.width = "auto";
        bar.style.top = "auto";
        bar.style.bottom = "0px";
      }
    };
    const vv = window.visualViewport;
    if (vv) {
      vv.addEventListener("resize", position);
      vv.addEventListener("scroll", position);
    }
    return {
      show() {
        visible = true;
        bar.hidden = false; // unhide before measuring offsetHeight
        position();
      },
      hide() {
        visible = false;
        bar.hidden = true;
      },
    };
  }

  // installVirtualKeypad mounts a full on-screen numeric keypad pinned to the
  // bottom of the visual viewport. It replaces the OS keyboard for digit-only
  // cell entry on touch devices: the host <input> sets inputmode="none" so
  // iOS/Android suppress their native keypad (which looks out of place and,
  // on iOS, lacks a Return key), and these keys drive the input via callbacks.
  // Layout: a navigation row (← ↑ ↓ →) above a 3-column digit pad (1–9, then a
  // double-width 0 and ⌫). Rendered only on coarse-pointer devices — on desktop
  // the physical keyboard and arrow-key navigation already cover this, so it
  // returns no-ops. Buttons fire on `pointerdown` with the default prevented so
  // the focused input is never blurred and its caret/selection survive editing.
  function installVirtualKeypad(options = {}) {
    const coarse = typeof window.matchMedia === "function" &&
      window.matchMedia("(pointer: coarse)").matches;
    if (!coarse) return {show: () => {}, hide: () => {}, visible: () => false, height: () => 0};

    const {onDigit, onBackspace, onNav} = options;
    const pad = document.createElement("div");
    pad.className = "entry-keypad";
    pad.hidden = true;

    const key = (label, aria, className, handler) => {
      const button = document.createElement("button");
      button.type = "button";
      button.className = className;
      button.textContent = label;
      button.setAttribute("aria-label", aria);
      button.addEventListener("pointerdown", (event) => {
        event.preventDefault();
        handler?.();
      });
      return button;
    };

    const navRow = document.createElement("div");
    navRow.className = "entry-keypad-nav";
    navRow.append(
      key("←", "Предыдущая колонка", "entry-keypad-key entry-keypad-arrow", () => onNav?.(-1, 0)),
      key("↑", "Предыдущая строка", "entry-keypad-key entry-keypad-arrow", () => onNav?.(0, -1)),
      key("↓", "Следующая строка", "entry-keypad-key entry-keypad-arrow", () => onNav?.(0, 1)),
      key("→", "Следующая колонка", "entry-keypad-key entry-keypad-arrow", () => onNav?.(1, 0)),
    );

    const digits = document.createElement("div");
    digits.className = "entry-keypad-digits";
    for (let n = 1; n <= 9; n++) {
      digits.appendChild(key(String(n), String(n), "entry-keypad-key", () => onDigit?.(String(n))));
    }
    digits.appendChild(key("0", "0", "entry-keypad-key entry-keypad-zero", () => onDigit?.("0")));
    digits.appendChild(key("⌫", "Удалить", "entry-keypad-key entry-keypad-back", () => onBackspace?.()));

    pad.append(navRow, digits);
    document.body.appendChild(pad);

    let isVisible = false;
    // Pin to the visual viewport's box explicitly. iOS Safari resolves
    // position:fixed + right:0 against the document width when the page scrolls
    // horizontally (our entry table is wide), which overflows the screen — so we
    // set left/width/top from visualViewport instead of relying on left/right:0.
    const position = () => {
      if (!isVisible) return;
      const vv = window.visualViewport;
      if (vv) {
        pad.style.left = `${Math.round(vv.offsetLeft)}px`;
        pad.style.right = "auto";
        pad.style.width = `${Math.round(vv.width)}px`;
        pad.style.top = `${Math.round(vv.offsetTop + vv.height - pad.offsetHeight)}px`;
        pad.style.bottom = "auto";
      } else {
        pad.style.left = "0px";
        pad.style.right = "0px";
        pad.style.width = "auto";
        pad.style.top = "auto";
        pad.style.bottom = "0px";
      }
    };
    const vv = window.visualViewport;
    if (vv) {
      vv.addEventListener("resize", position);
      vv.addEventListener("scroll", position);
    }
    return {
      show() {
        isVisible = true;
        pad.hidden = false; // unhide before measuring offsetHeight
        position();
      },
      hide() {
        isVisible = false;
        pad.hidden = true;
      },
      visible: () => isVisible,
      height: () => (isVisible ? pad.offsetHeight : 0),
    };
  }

  function createFloatingPopover(options) {
    const root = options.root;
    const specs = options.specs || [];
    if (!root || specs.length === 0) {
      return {bind: () => {}, hide: () => {}, position: () => {}};
    }

    let popoverNode = null;
    let active = null;

    function triggerFor(target) {
      if (!(target instanceof Element)) return null;
      for (const spec of specs) {
        const trigger = target.closest(spec.trigger);
        if (trigger && root.contains(trigger)) return trigger;
      }
      return null;
    }

    function specFor(trigger) {
      return specs.find((spec) => trigger.matches(spec.trigger)) || null;
    }

    function ensureNode() {
      if (!popoverNode) {
        popoverNode = document.createElement("div");
        popoverNode.className = "popover floating-name-popover";
        document.body.appendChild(popoverNode);
      }
      return popoverNode;
    }

    function show(trigger) {
      const spec = specFor(trigger);
      const source = spec ? trigger.querySelector(spec.popover) : null;
      const text = source?.textContent?.trim() || "";
      if (!spec || !text) {
        hide();
        return;
      }
      const popover = ensureNode();
      popover.textContent = text;
      popover.classList.add("visible");
      active = {trigger, spec};
      position();
    }

    function hide() {
      if (!popoverNode) return;
      popoverNode.classList.remove("visible", "above");
      popoverNode.textContent = "";
      popoverNode.style.removeProperty("top");
      popoverNode.style.removeProperty("left");
      popoverNode.style.removeProperty("max-width");
      active = null;
    }

    function position() {
      if (!active || !popoverNode) return;
      const {trigger, spec} = active;
      if (!document.body.contains(trigger) || !trigger.matches(spec.trigger)) {
        hide();
        return;
      }
      const anchor = trigger.querySelector(spec.anchor) || trigger;
      const rect = anchor.getBoundingClientRect();
      if (rect.width <= 0 || rect.height <= 0 || rect.bottom < 0 || rect.top > window.innerHeight) {
        hide();
        return;
      }

      const margin = 8;
      const popover = popoverNode;
      popover.style.maxWidth = `${Math.max(80, Math.min(420, window.innerWidth - margin * 2))}px`;
      popover.style.visibility = "hidden";
      popover.classList.add("visible");

      const width = popover.offsetWidth;
      const height = popover.offsetHeight;
      const maxLeft = Math.max(margin, window.innerWidth - width - margin);
      const left = clamp(rect.left, margin, maxLeft);
      const belowTop = rect.bottom - 2;
      const aboveTop = rect.top - height + 2;
      const shouldOpenUp = belowTop + height > window.innerHeight - margin && rect.top > window.innerHeight - rect.bottom;
      const maxTop = Math.max(margin, window.innerHeight - height - margin);
      const top = clamp(shouldOpenUp ? aboveTop : belowTop, margin, maxTop);

      popover.classList.toggle("above", shouldOpenUp);
      popover.style.left = `${Math.round(left)}px`;
      popover.style.top = `${Math.round(top)}px`;
      popover.style.visibility = "";
    }

    function onPointerOver(event) {
      // On touch, pointerover fires while swiping across cells; showing here
      // would pop the popover on every swipe. Touch shows via tap (see onTapEnd).
      if (event.pointerType === "touch") return;
      const trigger = triggerFor(event.target);
      if (!trigger || active?.trigger === trigger) return;
      show(trigger);
    }

    let tapStart = null;
    const TAP_MOVE_THRESHOLD = 10;

    function onTapStart(event) {
      if (event.pointerType !== "touch") return;
      tapStart = {x: event.clientX, y: event.clientY};
    }

    function onTapEnd(event) {
      if (event.pointerType !== "touch" || !tapStart) return;
      const moved = Math.hypot(event.clientX - tapStart.x, event.clientY - tapStart.y);
      tapStart = null;
      if (moved > TAP_MOVE_THRESHOLD) return; // a swipe, not a tap
      const trigger = triggerFor(event.target);
      if (trigger) {
        if (active?.trigger !== trigger) show(trigger);
      } else {
        hide();
      }
    }

    function onPointerOut(event) {
      if (event.pointerType === "touch") return;
      const trigger = active?.trigger;
      if (!trigger || !(event.target instanceof Node) || !trigger.contains(event.target)) return;
      if (event.relatedTarget instanceof Node && trigger.contains(event.relatedTarget)) return;
      if (!trigger.matches(":focus-within")) hide();
    }

    function onFocusIn(event) {
      const trigger = triggerFor(event.target);
      if (trigger) show(trigger);
    }

    function onFocusOut(event) {
      const trigger = active?.trigger;
      if (!trigger || !(event.target instanceof Node) || !trigger.contains(event.target)) return;
      window.setTimeout(() => {
        if (!trigger.matches(":focus-within") && !trigger.matches(":hover")) hide();
      }, 0);
    }

    let positionFrame = 0;
    function schedulePosition() {
      if (positionFrame) return;
      positionFrame = requestAnimationFrame(() => {
        positionFrame = 0;
        position();
      });
    }

    function onPointerDownOutside(event) {
      if (!active || event.pointerType !== "touch") return;
      if (event.target instanceof Node && active.trigger.contains(event.target)) return;
      hide();
    }

    function bind() {
      document.documentElement.classList.add("floating-popovers-enabled");
      document.addEventListener("pointerover", onPointerOver);
      document.addEventListener("pointerout", onPointerOut);
      document.addEventListener("focusin", onFocusIn);
      document.addEventListener("focusout", onFocusOut);
      document.addEventListener("pointerdown", onPointerDownOutside, true);
      document.addEventListener("pointerdown", onTapStart, true);
      document.addEventListener("pointerup", onTapEnd, true);
      window.addEventListener("scroll", schedulePosition, {capture: true, passive: true});
    }

    return {bind, hide, position};
  }

  const SYNC_STATUS_LABELS = {
    saved: "Синхронизировано",
    saving: "Синхронизация",
    reconnecting: "Переподключение",
    error: "Ошибка",
  };

  function createStatusReporter(statusNode) {
    if (!statusNode) return () => {};
    return function setStatus(state) {
      statusNode.dataset.state = state;
      const label = SYNC_STATUS_LABELS[state] || SYNC_STATUS_LABELS.saving;
      statusNode.setAttribute("aria-label", label);
      statusNode.title = label;
    };
  }

  // The standalone ✏️/👀 icons were folded into the ☰ menu (menu.js).
  // These now register the menu's context-aware jump item instead of mounting
  // an icon; .refresh() re-points it after SPA navigation. statusNode is kept
  // for call-site compatibility but unused.
  function mountEditorLink() {
    const set = () => window.dopeMenu?.setJump({
      label: "Редактировать",
      href: editorHrefForCurrentLocation(),
      title: "Открыть в режиме редактирования",
    });
    set();
    return {refresh: set};
  }

  function editorHrefForCurrentLocation() {
    return "/host" + window.location.pathname + window.location.search;
  }

  // mountUnnumberedBanner shows a sticky notice when the fest has teams without
  // numbers. Team number is the universal team identity, so the server blocks
  // result editing until every team is numbered (409); this points the host at
  // the numbers page. Idempotent — re-mounting is a no-op while the banner is up.
  function mountUnnumberedBanner(festID) {
    if (!festID || document.querySelector(".dope-unnumbered-banner")) return null;
    const bar = document.createElement("div");
    bar.className = "dope-unnumbered-banner";
    Object.assign(bar.style, {
      position: "sticky",
      top: "0",
      zIndex: "2147483600",
      background: "var(--amber-bg)",
      color: "var(--amber-text-strong)",
      font: "13px/1.4 system-ui, sans-serif",
      padding: "8px 12px",
      textAlign: "center",
      borderBottom: "1px solid var(--amber-border)",
    });
    bar.append("Командам не присвоены номера — редактирование результатов заблокировано. ");
    const link = document.createElement("a");
    link.href = `/host/fest/${festID}/numbers`;
    link.textContent = "Присвоить номера";
    Object.assign(link.style, {color: "inherit", fontWeight: "600", textDecoration: "underline"});
    bar.appendChild(link);
    document.body.prepend(bar);
    return bar;
  }

  function mountViewerLink() {
    const set = () => window.dopeMenu?.setJump({
      label: "Страница зрителя",
      href: viewerHrefForCurrentLocation(),
      title: "Открыть зрительскую страницу",
      external: true,
    });
    set();
    return {refresh: set};
  }

  function viewerHrefForCurrentLocation() {
    const path = window.location.pathname.replace(/^\/host(?=\/|$)/, "");
    return (path || "/") + window.location.search;
  }

  // mountGameDownloads registers the game's archive download links in the ☰ menu:
  // "Скачать XLSX" for everyone, and "Скачать .json.gz" (current state + edit
  // history) for hosts only. apiBase is the game's /api/fest/.../games/... base.
  function mountGameDownloads(opts) {
    const apiBase = opts && opts.apiBase;
    if (!apiBase || !window.dopeMenu?.setExtras) return;
    const items = [{
      label: "Скачать XLSX",
      href: `${apiBase}/export.xlsx`,
      title: "Скачать таблицу игры в формате XLSX",
      download: true,
    }];
    if (opts.canEdit) {
      items.push({
        label: "Скачать .json.gz",
        href: `${apiBase}/export.json.gz`,
        title: "Скачать текущее состояние игры и историю правок",
        download: true,
      });
    }
    window.dopeMenu.setExtras(items);
  }

  function parseGameRoute(pathname = window.location.pathname) {
    const host = pathname.match(/^\/host\/fest\/([^/]+)\/game\/([^/]+)/);
    if (host) {
      return {
        viewer: false,
        festID: host[1],
        gameID: host[2],
        apiBase: `/api/fest/${host[1]}/games/${host[2]}`,
      };
    }
    const pub = pathname.match(/^\/fest\/([^/]+)\/game\/([^/]+)/);
    if (pub) {
      return {
        viewer: true,
        festID: pub[1],
        gameID: pub[2],
        apiBase: `/api/fest/${pub[1]}/games/${pub[2]}`,
      };
    }
    return {};
  }

  // createLocalCache wraps one localStorage slot in the read/write-with-try/catch
  // discipline every page used to copy verbatim: reads degrade to null and writes
  // are silently dropped when storage is unavailable (private mode, quota, disabled
  // cookies). The page owns the key string and what JSON shape it stores.
  function createLocalCache(key) {
    return {
      key,
      read() {
        try {
          const raw = window.localStorage.getItem(key);
          return raw ? JSON.parse(raw) : null;
        } catch (_err) {
          return null;
        }
      },
      write(value) {
        if (value == null) return;
        try {
          window.localStorage.setItem(key, JSON.stringify(value));
        } catch (_err) {
          // ignore (quota / disabled / private mode)
        }
      },
    };
  }

  // createEpochTracker follows the server's per-process epoch token (see
  // server.epoch). The per-scope seq resets to 0 on a restart, so cached
  // MatchViews keep a high seq the new space never reaches and every post-restart
  // delta would be silently dropped as "already applied" — the page freezes. The
  // first non-empty epoch becomes the baseline; thereafter changed() reports true
  // once the token flips (the cue to reload and re-seed, since the stage cache
  // merges monotonically by seq and can't adopt the lower fresh seqs). Empty
  // epochs (older server builds) are ignored.
  function createEpochTracker() {
    let lastEpoch = "";
    return {
      changed(message) {
        const epoch = message?.epoch ? String(message.epoch) : "";
        if (!epoch) return false;
        if (lastEpoch === "") {
          lastEpoch = epoch;
          return false;
        }
        return epoch !== lastEpoch;
      },
      get epoch() {
        return lastEpoch;
      },
    };
  }

  // notifyEmbeddedResize posts the current document height to the parent frame so
  // an embedding host can size its iframe to the content. A no-op outside an
  // embed (no parent, or not the ?embed=1 view).
  function notifyEmbeddedResize(embedded) {
    if (!embedded || window.parent === window) return;
    window.requestAnimationFrame(() => {
      window.parent.postMessage({
        type: "dope:resize",
        height: Math.max(document.documentElement.scrollHeight, document.body.scrollHeight),
      }, window.location.origin);
    });
  }

  // gameEventsURL builds the SSE endpoint for a fest/game scope. The game id is
  // optional: fest-level pages omit it so the server streams the whole fest.
  function gameEventsURL(festID, gameID) {
    const fest = `fest_id=${encodeURIComponent(festID)}`;
    const game = gameID ? `&game_id=${encodeURIComponent(gameID)}` : "";
    return `/events?${fest}${game}`;
  }

  // scheduleStaticReload reloads the page after ~5s (jittered 4-7s) so a fleet of
  // static viewers spreads its reloads across the window instead of stampeding the
  // server the instant lockdown lifts.
  function scheduleStaticReload() {
    window.setTimeout(() => window.location.reload(), 4000 + Math.floor(Math.random() * 3000));
  }

  // fetchGameData loads the three cold payloads a game page needs — scheme, state,
  // and (when the route is fest-scoped) the fest view — in parallel, throwing the
  // server's error text on any non-OK response. The fest fetch is skipped when the
  // route carries neither an apiBase nor a festID.
  async function fetchGameData(route) {
    const festURL = route.apiBase || (route.festID ? `/api/fest/${route.festID}` : "");
    const [schemeResp, stateResp, festResp] = await Promise.all([
      fetch(`${route.apiBase}/scheme`),
      fetch(`${route.apiBase}/state`),
      festURL ? fetch(festURL) : Promise.resolve(null),
    ]);
    if (!schemeResp.ok) throw new Error(await schemeResp.text());
    if (!stateResp.ok) throw new Error(await stateResp.text());
    if (festResp && !festResp.ok) throw new Error(await festResp.text());
    return {
      scheme: await schemeResp.json(),
      state: await stateResp.json(),
      fest: festResp ? await festResp.json() : null,
    };
  }

  // createGameDataLoader centralizes the stale-while-revalidate hydration flow the
  // OD and KSI pages share: render instantly from the server-inlined __GAME_INIT__
  // payload, else from the localStorage snapshot, else from a cold parallel fetch;
  // and in the first two cases kick a background revalidation. The page supplies
  // `adopt(snapshot, source)` — which assigns its own scheme/state/fest closures and
  // renders — and an optional `revalidate()`. `source` is "init" | "cache" | "fetch";
  // on "init" the snapshot also carries the raw `init` payload for seq/epoch/banner
  // wiring that only the inlined path has. Returns {load, cache} where `load()`
  // mirrors the old loadAll(): it resolves synchronously off init/cache (revalidation
  // runs detached) and awaits the network only on a cold start.
  function createGameDataLoader({route, cachePrefix, adopt, revalidate}) {
    const cache = createLocalCache(`${cachePrefix}:game:${route.festID || ""}:${route.gameID || ""}`);
    function writeSnapshot(snap) {
      if (snap && snap.scheme && snap.state) cache.write({scheme: snap.scheme, state: snap.state, fest: snap.fest ?? null});
    }
    function kickRevalidate() {
      if (revalidate) Promise.resolve().then(revalidate).catch((error) => console.error(error));
    }
    async function load() {
      const init = window.__GAME_INIT__;
      if (init && init.scheme && init.state) {
        window.__GAME_INIT__ = null;
        const snap = {scheme: init.scheme, state: init.state, fest: init.fest || null};
        adopt({...snap, init}, "init");
        writeSnapshot(snap);
        kickRevalidate();
        return;
      }
      const cached = cache.read();
      if (cached && cached.scheme && cached.state) {
        adopt({scheme: cached.scheme, state: cached.state, fest: cached.fest || null}, "cache");
        kickRevalidate();
        return;
      }
      const fresh = await fetchGameData(route);
      adopt(fresh, "fetch");
      writeSnapshot(fresh);
    }
    return {load, cache, writeSnapshot};
  }

  // markNameOverflow flags every cell under `root` whose inner name (and optional
  // city) is clipped, so the page can show a fade + popover. Reads are batched
  // ahead of writes so the measure loop never triggers a reflow mid-pass.
  function markNameOverflow(root, cfg) {
    if (!root) return;
    const cells = root.querySelectorAll(cfg.cellSelector);
    const readings = new Array(cells.length);
    for (let i = 0; i < cells.length; i++) {
      const name = cells[i].querySelector(cfg.nameSelector);
      readings[i] = Boolean(name && name.scrollWidth > name.clientWidth + 1);
    }
    for (let i = 0; i < cells.length; i++) {
      cells[i].classList.toggle(cfg.truncatedClass, readings[i]);
      if (cfg.citySelector && cfg.cityTruncatedClass) {
        const city = cells[i].querySelector(cfg.citySelector);
        city?.classList.toggle(cfg.cityTruncatedClass, city.scrollWidth > city.clientWidth + 1);
      }
    }
  }

  function createTeamNameOverflowController({root, detailed, results}) {
    function updateDetailed(targetRoot = root) {
      markNameOverflow(targetRoot, detailed);
    }
    function updateResults(targetRoot = root) {
      markNameOverflow(targetRoot, results);
    }
    let frame = 0;
    function schedule(targetRoot = root) {
      if (frame) cancelAnimationFrame(frame);
      frame = requestAnimationFrame(() => {
        frame = 0;
        updateDetailed(targetRoot);
        updateResults(targetRoot);
      });
    }
    return {schedule, updateDetailed, updateResults};
  }

  function createCellRangeSelection(options) {
    const {
      root,
      cellSelector = ".answer-cell",
      readonly = () => false,
      coordOf,
      cellAtCoord,
      serialize = (cell) => (cell?.textContent || "").trim(),
      parse = (text) => String(text || "").trim(),
      applyValues,
      onSelectionChange,
      onActiveChange,
      // cycle(cell) -> next value (in applyValues' value space) for a touch tap.
      // When provided, tapping a cell on a touch device advances it through its
      // states (e.g. empty → right → wrong → empty), the only way to enter data
      // on mobile where there is no physical +/- keyboard.
      cycle = null,
      classes = {},
    } = options;
    const cls = {
      selected: "cell-selected",
      anchor: "cell-selection-anchor",
      top: "cell-selection-top",
      bottom: "cell-selection-bottom",
      left: "cell-selection-left",
      right: "cell-selection-right",
      ...classes,
    };
    const allClasses = [cls.selected, cls.anchor, cls.top, cls.bottom, cls.left, cls.right];
    const isReadonly = typeof readonly === "function" ? readonly : () => Boolean(readonly);

    let anchor = null;
    let focusCoord = null;
    let dragState = null;
    let suppressNextClick = false;
    let tapStart = null;

    function rect() {
      if (!anchor || !focusCoord) return null;
      return {
        rowStart: Math.min(anchor.row, focusCoord.row),
        rowEnd: Math.max(anchor.row, focusCoord.row),
        colStart: Math.min(anchor.col, focusCoord.col),
        colEnd: Math.max(anchor.col, focusCoord.col),
      };
    }

    function clearClasses() {
      root.querySelectorAll(`${cellSelector}.${cls.selected}, ${cellSelector}.${cls.anchor}`).forEach((cell) => {
        cell.classList.remove(...allClasses);
      });
    }

    function renderClasses() {
      clearClasses();
      const r = rect();
      if (!r) return;
      for (let row = r.rowStart; row <= r.rowEnd; row++) {
        for (let col = r.colStart; col <= r.colEnd; col++) {
          const cell = cellAtCoord({row, col});
          if (!cell) continue;
          cell.classList.add(cls.selected);
          if (row === r.rowStart) cell.classList.add(cls.top);
          if (row === r.rowEnd) cell.classList.add(cls.bottom);
          if (col === r.colStart) cell.classList.add(cls.left);
          if (col === r.colEnd) cell.classList.add(cls.right);
        }
      }
      const anchorCell = cellAtCoord(anchor);
      if (anchorCell) anchorCell.classList.add(cls.anchor);
    }

    function selectedCells() {
      const out = [];
      const r = rect();
      if (!r) return out;
      for (let row = r.rowStart; row <= r.rowEnd; row++) {
        for (let col = r.colStart; col <= r.colEnd; col++) {
          const cell = cellAtCoord({row, col});
          if (cell) out.push(cell);
        }
      }
      return out;
    }

    function setSelection(newAnchor, newFocus = newAnchor, opts = {}) {
      anchor = newAnchor ? {row: newAnchor.row, col: newAnchor.col} : null;
      focusCoord = newFocus ? {row: newFocus.row, col: newFocus.col} : null;
      renderClasses();
      onSelectionChange?.({anchor, focus: focusCoord, rect: rect()});
      const focusCell = cellAtCoord(focusCoord);
      if (focusCell) {
        if (opts.focus !== false) focusCell.focus({preventScroll: opts.preventScroll});
        onActiveChange?.(focusCell, focusCoord);
      }
    }

    function clearSelection() {
      anchor = null;
      focusCoord = null;
      clearClasses();
      onSelectionChange?.(null);
    }

    function deleteSelected() {
      if (isReadonly()) return false;
      const cells = selectedCells();
      if (cells.length === 0) return false;
      const empty = parse("");
      const edits = [];
      for (const cell of cells) edits.push({cell, value: empty});
      applyValues?.(edits);
      return true;
    }

    function copySelection(event) {
      const r = rect();
      if (!r) return false;
      const lines = [];
      for (let row = r.rowStart; row <= r.rowEnd; row++) {
        const cols = [];
        for (let col = r.colStart; col <= r.colEnd; col++) {
          const cell = cellAtCoord({row, col});
          cols.push(cell ? serialize(cell) : "");
        }
        lines.push(cols.join("\t"));
      }
      event.clipboardData?.setData("text/plain", lines.join("\n"));
      event.preventDefault();
      return true;
    }

    function pasteSelection(event) {
      if (isReadonly() || !anchor) return false;
      const text = event.clipboardData?.getData("text/plain") || "";
      if (!text) return false;
      event.preventDefault();
      const normalized = text.replace(/\r\n/g, "\n").replace(/\r/g, "\n");
      const lines = normalized.split("\n");
      if (lines.length > 1 && lines[lines.length - 1] === "") lines.pop();
      const grid = lines.map((line) => line.split("\t"));
      if (grid.length === 0) return false;
      const r = rect();
      const startRow = r.rowStart;
      const startCol = r.colStart;
      const edits = [];
      let lastRow = startRow;
      let lastCol = startCol;
      for (let rOff = 0; rOff < grid.length; rOff++) {
        const cols = grid[rOff];
        for (let cOff = 0; cOff < cols.length; cOff++) {
          const cell = cellAtCoord({row: startRow + rOff, col: startCol + cOff});
          if (!cell) continue;
          edits.push({cell, value: parse(cols[cOff])});
          lastRow = startRow + rOff;
          lastCol = startCol + cOff;
        }
      }
      if (edits.length > 0) applyValues?.(edits);
      setSelection({row: startRow, col: startCol}, {row: lastRow, col: lastCol}, {focus: true});
      return true;
    }

    function handleMouseDown(event) {
      if (event.button !== 0 || isReadonly()) return;
      const cell = event.target.closest?.(cellSelector);
      if (!cell || !root.contains(cell)) return;
      if (event.target instanceof HTMLInputElement || event.target instanceof HTMLTextAreaElement || event.target instanceof HTMLSelectElement) return;
      const coord = coordOf(cell);
      if (!coord) return;
      event.preventDefault();
      suppressNextClick = Boolean(event.shiftKey && anchor);
      const nextAnchor = event.shiftKey && anchor ? anchor : coord;
      setSelection(nextAnchor, coord, {preventScroll: true});
      dragState = {anchor: nextAnchor, focus: coord};
      document.addEventListener("mouseup", handleMouseUp, {once: true});
    }

    function handleMouseUp() {
      dragState = null;
    }

    function handleMouseOver(event) {
      if (!dragState || isReadonly()) return;
      const cell = event.target.closest?.(cellSelector);
      if (!cell || !root.contains(cell)) return;
      const coord = coordOf(cell);
      if (!coord) return;
      if (coord.row === dragState.focus.row && coord.col === dragState.focus.col) return;
      dragState.focus = coord;
      setSelection(dragState.anchor, coord, {focus: false});
    }

    function handleClickCapture(event) {
      if (suppressNextClick) {
        suppressNextClick = false;
        event.stopPropagation();
      }
    }

    function isEditableTarget(target) {
      return target instanceof HTMLInputElement
        || target instanceof HTMLTextAreaElement
        || target instanceof HTMLSelectElement
        || Boolean(target?.isContentEditable);
    }

    function handleCopy(event) {
      if (isEditableTarget(event.target)) return;
      if (!root.contains(event.target) && !root.contains(document.activeElement)) return;
      copySelection(event);
    }

    function handlePaste(event) {
      if (isEditableTarget(event.target)) return;
      if (!root.contains(event.target) && !root.contains(document.activeElement)) return;
      pasteSelection(event);
    }

    // Touch taps cycle a cell's value via `cycle` (see option docs). We track
    // the touch from pointerdown so we can tell a tap from a scroll: if the
    // finger moves more than a few px or lifts off a different cell, it was a
    // scroll and we leave the value alone. Gated on pointerType === "touch", so
    // mouse clicks (desktop select/drag) are unaffected.
    const TAP_MOVE_TOLERANCE = 10;

    function handlePointerDown(event) {
      if (event.pointerType !== "touch" || !cycle || isReadonly()) {
        tapStart = null;
        return;
      }
      const cell = event.target.closest?.(cellSelector);
      if (!cell || !root.contains(cell) || isEditableTarget(event.target)) {
        tapStart = null;
        return;
      }
      tapStart = {cell, x: event.clientX, y: event.clientY};
    }

    function handlePointerUp(event) {
      if (event.pointerType !== "touch" || !cycle) return;
      const start = tapStart;
      tapStart = null;
      if (!start || isReadonly()) return;
      const cell = event.target.closest?.(cellSelector);
      if (!cell || cell !== start.cell) return;
      if (Math.abs(event.clientX - start.x) > TAP_MOVE_TOLERANCE
        || Math.abs(event.clientY - start.y) > TAP_MOVE_TOLERANCE) return;
      const value = cycle(cell);
      if (value === undefined || value === null) return;
      applyValues?.([{cell, value}]);
      const coord = coordOf(cell);
      if (coord) setSelection(coord, coord, {focus: false});
    }

    function handlePointerCancel() {
      tapStart = null;
    }

    function bind() {
      root.addEventListener("mousedown", handleMouseDown);
      root.addEventListener("mouseover", handleMouseOver);
      root.addEventListener("click", handleClickCapture, true);
      root.addEventListener("pointerdown", handlePointerDown);
      root.addEventListener("pointerup", handlePointerUp);
      root.addEventListener("pointercancel", handlePointerCancel);
      document.addEventListener("copy", handleCopy);
      document.addEventListener("paste", handlePaste);
    }

    function unbind() {
      root.removeEventListener("mousedown", handleMouseDown);
      root.removeEventListener("mouseover", handleMouseOver);
      root.removeEventListener("click", handleClickCapture, true);
      root.removeEventListener("pointerdown", handlePointerDown);
      root.removeEventListener("pointerup", handlePointerUp);
      root.removeEventListener("pointercancel", handlePointerCancel);
      document.removeEventListener("copy", handleCopy);
      document.removeEventListener("paste", handlePaste);
    }

    return {
      bind,
      unbind,
      setSelection,
      clearSelection,
      deleteSelected,
      selectedCells,
      refresh: renderClasses,
      get anchor() { return anchor; },
      get focus() { return focusCoord; },
      get rect() { return rect(); },
    };
  }

  // createViewerCounter renders a live "NN👀" concurrent-viewer tally
  // immediately to the left of the sync-status tick. The span is created and
  // inserted dynamically (no markup change needed) and stays hidden until a
  // positive count arrives. setCount is driven by "viewers" SSE events.
  function createViewerCounter(statusNode) {
    if (!statusNode || !statusNode.parentElement) {
      return {setCount: () => {}};
    }
    const node = document.createElement("span");
    node.className = "viewers-count";
    node.hidden = true;
    node.setAttribute("aria-label", "Зрителей онлайн");
    // Number and eyes are separate children so the flex `gap` spaces them — a
    // single "N👀" text node would render them touching.
    const num = document.createElement("span");
    num.className = "viewers-count-num";
    const eyes = document.createElement("span");
    eyes.className = "viewers-count-eyes";
    eyes.textContent = "\u{1F440}";
    eyes.setAttribute("aria-hidden", "true");
    node.append(num, eyes);
    statusNode.parentElement.insertBefore(node, statusNode);
    return {
      setCount(count) {
        const n = Number(count);
        if (!Number.isFinite(n) || n <= 0) {
          node.hidden = true;
          num.textContent = "";
          return;
        }
        num.textContent = String(n);
        node.title = `Зрителей онлайн: ${n}`;
        node.hidden = false;
      },
    };
  }

  function fitEKStageTeamName(cell, name) {
    if (!cell || !name) return false;
    const baseSize = parseFloat(getComputedStyle(name).fontSize) || 13;
    const minSize = 9;
    const vertOverflows = () => name.scrollHeight > name.clientHeight + 1;
    const horizOverflows = () => name.scrollWidth > name.clientWidth + 1;
    name.style.fontSize = "";
    if (vertOverflows()) {
      let size = Math.floor(baseSize) - 1;
      while (size >= minSize) {
        name.style.fontSize = `${size}px`;
        if (!vertOverflows()) break;
        size -= 1;
      }
      if (size < minSize) name.style.fontSize = `${minSize}px`;
    }
    return vertOverflows() || horizOverflows();
  }

  // computeEKPlayerStats aggregates per-player individual stats across every
  // battle of an EK game. `stages` is the payload from /stages/matches:
  // [{code, matches: [MatchView, ...]}, ...]. Only regular themes are counted —
  // shootout ("перестрелка") themes are a tiebreaker and are excluded, matching
  // the Σ+ semantics shown in a battle (TeamView.plus ignores shootouts too).
  //
  // Players are keyed by (team, player) so namesakes on different teams stay
  // separate. The team-share column ("% от команды") is a positive player's
  // share among the team's positive contributors (denominator = sum of only the
  // positive players' Σ); players with Σ <= 0 are 0 (see the share loop below).
  // Returns rows sorted by Σ descending (then Σ+, then name).
  function computeEKPlayerStats(stages) {
    const values = [10, 20, 30, 40, 50]; // answer index → nominal value
    const players = new Map();   // key → stat row
    const battleSeen = new Map(); // key → Set of battle ids (for the Бои count)
    for (const stage of stages || []) {
      for (const match of stage.matches || []) {
        const battleID = `${stage.code || ""}${match.code || ""}`;
        for (const team of match.teams || []) {
          const teamName = team.name || "";
          for (const theme of team.themes || []) {
            const playerName = String(theme.player || "").trim();
            if (!playerName) continue;
            const key = `${teamName}${playerName}`;
            let row = players.get(key);
            if (!row) {
              row = {
                player: playerName,
                team: teamName,
                sum: 0,
                plus: 0,
                battles: 0,
                right: [0, 0, 0, 0, 0],
                wrong: [0, 0, 0, 0, 0],
                rightTotal: 0,
                share: 0,
              };
              players.set(key, row);
              battleSeen.set(key, new Set());
            }
            const seen = battleSeen.get(key);
            if (!seen.has(battleID)) {
              seen.add(battleID);
              row.battles++;
            }
            (theme.answers || []).forEach((mark, i) => {
              const value = values[i] || 0;
              if (mark === "right") {
                row.sum += value;
                row.plus += value;
                row.right[i]++;
                row.rightTotal++;
              } else if (mark === "wrong") {
                row.sum -= value;
                row.wrong[i]++;
              }
            });
          }
        }
      }
    }
    const rows = Array.from(players.values());
    // "% от команды": a positive player's share among the team's POSITIVE
    // contributors. A player with Σ <= 0 is 0 (they didn't help), and the
    // denominator is the sum of only the positive players' Σ — so the team's
    // positive players' shares add up to 100%, independent of how negative the
    // rest of the team went.
    const teamPositiveSum = new Map();
    for (const row of rows) {
      if (row.sum > 0) teamPositiveSum.set(row.team, (teamPositiveSum.get(row.team) || 0) + row.sum);
    }
    for (const row of rows) {
      const total = teamPositiveSum.get(row.team) || 0;
      row.share = row.sum > 0 && total > 0 ? row.sum / total : 0;
    }
    rows.sort((a, b) =>
      b.sum - a.sum ||
      b.plus - a.plus ||
      a.player.localeCompare(b.player, "ru"));
    return rows;
  }

  // buildEKStatsTable renders the rows from computeEKPlayerStats into the
  // "Статистика" table. Columns: Игрок, Команда, Σ, Σ+, Бои, 50/40/30/20/10
  // (correct counts, descending nominal), −50…−10 (wrong counts, shown as a
  // plain positive count), and the team-share percentage. Counts are always
  // shown (including 0). Name cells reuse the results-team truncate+fade+popover
  // structure so long names behave like everywhere else. Shared host/viewer.
  function buildEKStatsTable(rows) {
    const wrapper = document.createElement("div");
    wrapper.className = "results-wrapper ek-stats-wrapper";
    if (!rows || rows.length === 0) {
      const empty = document.createElement("p");
      empty.className = "empty";
      empty.textContent = "Пока нет данных: ни одного ответа не отмечено.";
      wrapper.appendChild(empty);
      return wrapper;
    }

    const table = document.createElement("table");
    table.className = "results-table ek-stats-table";
    const thead = document.createElement("thead");
    const head = document.createElement("tr");
    head.appendChild(th("Игрок", "results-team-head ek-stats-name-head ek-stats-player-head"));
    head.appendChild(th("Команда", "results-team-head ek-stats-name-head ek-stats-team-head"));
    head.appendChild(th("Σ", "number ek-stats-sum-head"));
    head.appendChild(th("Σ+", "number"));
    head.appendChild(th("Бои", "number"));
    for (const value of [50, 40, 30, 20, 10]) {
      head.appendChild(th(value, "number narrow"));
    }
    for (const value of [50, 40, 30, 20, 10]) {
      head.appendChild(th(`-${value}`, "number narrow ek-stats-wrong-head"));
    }
    head.appendChild(th("% от команды", "number ek-stats-share-head"));
    thead.appendChild(head);
    table.appendChild(thead);

    const tbody = document.createElement("tbody");
    const nameCell = (text, className) => {
      const cell = document.createElement("td");
      cell.className = `results-team ek-stats-name ${className}`;
      const wrap = document.createElement("span");
      wrap.className = "results-team-name-wrap";
      const name = document.createElement("span");
      name.className = "results-team-name";
      name.textContent = text;
      name.tabIndex = 0;
      name.setAttribute("aria-label", text);
      wrap.appendChild(name);
      cell.appendChild(wrap);
      const popover = document.createElement("span");
      popover.className = "popover results-team-name-popover";
      popover.textContent = text;
      cell.appendChild(popover);
      return cell;
    };
    rows.forEach((row, index) => {
      const tr = document.createElement("tr");
      tr.className = "results-row";
      if (index === 0) tr.classList.add("results-group-first");
      if (index === rows.length - 1) tr.classList.add("results-group-last");
      tr.appendChild(nameCell(row.player, "ek-stats-player"));
      tr.appendChild(nameCell(row.team, "ek-stats-team"));
      tr.appendChild(td(row.sum, "number ek-stats-sum"));
      tr.appendChild(td(row.plus, "number"));
      tr.appendChild(td(row.battles, "number"));
      for (let i = 4; i >= 0; i--) {
        tr.appendChild(td(row.right[i] || 0, "number narrow"));
      }
      for (let i = 4; i >= 0; i--) {
        tr.appendChild(td(row.wrong[i] || 0, "number narrow ek-stats-wrong"));
      }
      tr.appendChild(td(`${Math.round(row.share * 100)}%`, "number ek-stats-share"));
      tbody.appendChild(tr);
    });
    table.appendChild(tbody);
    wrapper.appendChild(table);
    return wrapper;
  }

  // applyDeltaOps returns a deep clone of `base` with scoped set-ops applied,
  // via the shared setAtDeltaPath (also used by createPendingOps.overlay), so the
  // read-only viewer can reconstruct a full match view from a delta without the
  // host sync controller. Non-"set" ops are skipped.
  function applyDeltaOps(base, ops) {
    let next = base == null ? {} : JSON.parse(JSON.stringify(base));
    for (const op of ops || []) {
      if (op && op.op && op.op !== "set") continue;
      next = setAtDeltaPath(next, op?.path || [], op?.value);
    }
    return next;
  }

  function setAtDeltaPath(root, path, value) {
    if (!path || path.length === 0) return value;
    const [segment, ...rest] = path;
    if (typeof segment === "number") {
      const arr = Array.isArray(root) ? root : [];
      while (arr.length <= segment) arr.push(null);
      arr[segment] = setAtDeltaPath(arr[segment], rest, value);
      return arr;
    }
    const obj = root && typeof root === "object" && !Array.isArray(root) ? root : {};
    obj[segment] = setAtDeltaPath(obj[segment], rest, value);
    return obj;
  }

  // ---- Client-side state recorder ------------------------------------------
  // A best-effort black box for diagnosis: a ring of timeline EVENTS (SSE
  // open/close, applied deltas/snapshots, resyncs, sent/rejected patches) and a
  // ring of periodic STATE snapshots, persisted to localStorage so an operator
  // can download a JSON log after something looked wrong. It pairs with the two
  // other evidence sources: the server audit is what COMMITTED, a HAR is what
  // crossed the WIRE, and this is what THIS client believed and rendered — the
  // only one that can reveal optimistic-but-never-committed state. Every
  // localStorage touch is guarded; a quota error trims the oldest half instead
  // of throwing.
  const RECORDER_EVENT_CAP = 1500;
  const RECORDER_SNAPSHOT_CAP = 40;

  function recorderNow() {
    try {
      return new Date().toISOString();
    } catch (_e) {
      return "";
    }
  }

  function cheapHash(str) {
    let h = 5381;
    for (let i = 0; i < str.length; i++) h = ((h << 5) + h + str.charCodeAt(i)) | 0;
    return h;
  }

  function createClientRecorder(options) {
    const scope = (options && options.scope) || "page";
    const evKey = `dope.rec.ev:${scope}`;
    const snapKey = `dope.rec.snap:${scope}`;
    // Per page-load id so a downloaded log spanning a reload stays separable.
    const session = Math.random().toString(36).slice(2, 10);
    let store = null;
    try {
      store = window.localStorage;
    } catch (_e) {
      store = null;
    }

    function load(key) {
      if (!store) return [];
      try {
        return JSON.parse(store.getItem(key) || "[]");
      } catch (_e) {
        return [];
      }
    }
    function save(key, arr) {
      if (!store) return;
      try {
        store.setItem(key, JSON.stringify(arr));
      } catch (_e) {
        try {
          store.setItem(key, JSON.stringify(arr.slice(Math.floor(arr.length / 2))));
        } catch (_e2) {
          /* give up silently — recording must never break the page */
        }
      }
    }
    function push(key, cap, record) {
      const arr = load(key);
      arr.push(record);
      while (arr.length > cap) arr.shift();
      save(key, arr);
    }

    function event(type, data) {
      push(evKey, RECORDER_EVENT_CAP, {t: recorderNow(), s: session, type, ...(data || {})});
    }

    let lastSnapshotHash = null;
    function snapshot(reason, state, meta) {
      let json = null;
      try {
        json = state === undefined ? null : JSON.stringify(state);
      } catch (_e) {
        json = null;
      }
      const hash = json ? cheapHash(json) : null;
      // Skip an idle "tick" that changed nothing, so quiet periods don't fill
      // the ring with identical copies.
      if (reason === "tick" && hash !== null && hash === lastSnapshotHash) return;
      lastSnapshotHash = hash;
      push(snapKey, RECORDER_SNAPSHOT_CAP, {
        t: recorderNow(),
        s: session,
        reason,
        len: json ? json.length : 0,
        ...(meta || {}),
        state: json ? JSON.parse(json) : null,
      });
    }

    function dump() {
      return {
        scope,
        session,
        ua: typeof navigator !== "undefined" ? navigator.userAgent : "",
        href: typeof location !== "undefined" ? location.href : "",
        exportedAt: recorderNow(),
        events: load(evKey),
        snapshots: load(snapKey),
      };
    }
    function download() {
      try {
        const blob = new Blob([JSON.stringify(dump(), null, 2)], {type: "application/json"});
        const url = URL.createObjectURL(blob);
        const a = document.createElement("a");
        a.href = url;
        a.download = `dope-log-${scope.replace(/[^\w.-]+/g, "_")}-${recorderNow().replace(/[:.]/g, "-")}.json`;
        document.body.appendChild(a);
        a.click();
        a.remove();
        window.setTimeout(() => URL.revokeObjectURL(url), 0);
      } catch (_e) {
        /* download is best-effort */
      }
    }
    function clear() {
      try {
        if (store) {
          store.removeItem(evKey);
          store.removeItem(snapKey);
        }
      } catch (_e) {
        /* ignore */
      }
    }

    return {scope, session, event, snapshot, dump, download, clear, enabled: Boolean(store)};
  }

  // installClientRecorder wires a recorder for a page: periodic state snapshots,
  // lifecycle markers, and (when showButton) a small floating "download log"
  // button. Returns the recorder — pass it to createStateSync so its SSE timeline
  // is captured too — or null when localStorage is unavailable.
  function installClientRecorder(options) {
    options = options || {};
    const recorder = createClientRecorder({scope: options.scope});
    if (!recorder.enabled) return null;
    const getState = typeof options.getState === "function" ? options.getState : null;
    const intervalMs = Number.isFinite(options.intervalMs) ? options.intervalMs : 5000;
    const snap = (reason) => recorder.snapshot(reason, getState ? getState() : undefined, options.getMeta ? options.getMeta() : null);
    snap("init");
    if (intervalMs > 0) window.setInterval(() => snap("tick"), intervalMs);
    if (typeof document !== "undefined") {
      document.addEventListener("visibilitychange", () => {
        recorder.event("visibility", {state: document.visibilityState});
        if (document.visibilityState === "hidden") snap("hidden");
      });
      if (options.showButton !== false) mountRecorderButton(recorder, options.label);
    }
    if (typeof window !== "undefined") {
      window.addEventListener("pagehide", () => recorder.event("pagehide", {}));
    }
    return recorder;
  }

  function mountRecorderButton(recorder, label) {
    if (document.querySelector(".dope-rec-btn")) return; // one per page
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "dope-rec-btn";
    btn.textContent = label || "Скачать лог";
    btn.title = "Скачать журнал состояния этой вкладки (для диагностики)";
    Object.assign(btn.style, {
      position: "fixed",
      bottom: "8px",
      right: "8px",
      zIndex: "2147483000",
      font: "12px/1.2 system-ui, sans-serif",
      padding: "4px 8px",
      background: "var(--diag-bg)",
      color: "var(--diag-fg)",
      border: "0",
      borderRadius: "6px",
      cursor: "pointer",
      opacity: "0.5",
    });
    btn.addEventListener("mouseenter", () => (btn.style.opacity = "1"));
    btn.addEventListener("mouseleave", () => (btn.style.opacity = "0.5"));
    btn.addEventListener("click", () => recorder.download());
    document.body.appendChild(btn);
  }

  window.DopeTable = {
    th,
    td,
    option,
    applyDeltaOps,
    createClientRecorder,
    installClientRecorder,
    createViewerCounter,
    buildFlatScoreTable,
    buildTwoRowScoreTable,
    computePlaces,
    setText,
    formatDisplayText,
    isFormControl,
    clamp,
    sameArray,
    cssEscape,
    createNodeIndex,
    createScoreTableIndex,
    scoreCellSpecs,
    setNodeText,
    setMarkClass,
    canPatchScoreShape,
    patchScoreTable,
    renderGameBreadcrumbs,
    parseScopedEvent,
    createStateSync,
    createPendingOps,
    createHostPresence,
    normalizeVenue,
    formatVenue,
    formatBattleVenue,
    formatBattleVenueShort,
    statusLabel,
    formatNumber,
    formatPlace,
    stageType,
    stageTabLabel,
    teamListCell,
    buildVenuesTable,
    fetchFestRoster,
    buildRosterTable,
    buildRosterView,
    createFloatingPopover,
    installCellNavBar,
    installVirtualKeypad,
    createStatusReporter,
    mountEditorLink,
    mountViewerLink,
    mountUnnumberedBanner,
    mountGameDownloads,
    parseGameRoute,
    markNameOverflow,
    createLocalCache,
    gameEventsURL,
    createEpochTracker,
    notifyEmbeddedResize,
    scheduleStaticReload,
    fetchGameData,
    createGameDataLoader,
    createTeamNameOverflowController,
    fitEKStageTeamName,
    createCellRangeSelection,
    computeEKPlayerStats,
    buildEKStatsTable,
  };
})();
