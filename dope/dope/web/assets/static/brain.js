// Брейн host + viewer page. Renders one round-robin cross-table per group from
// the fest view, and — for hosts — the per-бой protocol editor (K questions × 2
// teams, each a mark + answering player). Брейн has no themes, so it reads/writes
// its own bouts API (match_questions) rather than the EK theme editor. Reuses
// window.DopeTable for the cross-table, SSE parsing and helpers.
(() => {
  "use strict";
  const gameTable = window.DopeTable;
  const mount = document.getElementById("brainTable");
  if (!gameTable || !mount) return;

  const init = window.__HOST_INIT__ || {};
  const route = init.route || {};
  const festID = route.festID;
  const gameID = route.gameID;
  const canEdit = Boolean(init.canEdit);
  const viewer = !location.pathname.startsWith("/host/");
  const apiBase = `/api/fest/${festID}/games/${gameID}`;
  let festView = init.fest || null;
  let openBoutCode = null;

  const statusNode = document.getElementById("status");
  const breadcrumbsNode = document.getElementById("gameBreadcrumbs");
  const setStatus = gameTable.createStatusReporter(statusNode);

  function renderBreadcrumbs() {
    if (!breadcrumbsNode || !festID) return;
    gameTable.renderGameBreadcrumbs(breadcrumbsNode, {
      festHref: viewer ? `/fest/${festID}` : `/host/fest/${festID}`,
      festTitle: (festView && festView.title) || "Фест",
      gameTitle: (festView && festView.gameName) || "Брейн",
    });
  }

  const METRIC_HEADERS = ["О", "+", "−", "+/−", "М"];

  function formatDiff(diff) {
    if (diff > 0) return "+" + diff;
    return String(diff);
  }

  // buildGroup projects one stage's бои into cross-table rows. Team positions are
  // taken from the order бои are listed (бой codes are emitted in schedule order,
  // so the first appearances line up with the seed positions 1..N).
  function buildGroup(stage) {
    const matches = stage.matches || [];
    const order = [];
    const indexByName = new Map();
    const indexOf = (name) => {
      if (!indexByName.has(name)) {
        indexByName.set(name, order.length);
        order.push(name);
      }
      return indexByName.get(name);
    };
    const bouts = new Map(); // "i-j" -> {code, own, opp, finished}
    for (const match of matches) {
      const teams = match.teams || [];
      if (teams.length < 2) continue;
      const i = indexOf(teams[0].name);
      const j = indexOf(teams[1].name);
      const finished = match.status === "finished";
      bouts.set(i + "-" + j, { code: match.code, own: teams[0].total || 0, opp: teams[1].total || 0, finished });
      bouts.set(j + "-" + i, { code: match.code, own: teams[1].total || 0, opp: teams[0].total || 0, finished });
    }
    const n = order.length;
    const rows = order.map((name, i) => {
      const cells = [];
      let points = 0;
      let taken = 0;
      let conceded = 0;
      for (let j = 0; j < n; j++) {
        if (i === j) {
          cells.push(null);
          continue;
        }
        const bout = bouts.get(i + "-" + j);
        if (!bout) {
          cells.push({ text: "" });
          continue;
        }
        cells.push({ text: `${bout.own} : ${bout.opp}`, boutCode: bout.code });
        taken += bout.own;
        conceded += bout.opp;
        if (bout.finished) points += bout.own > bout.opp ? 2 : bout.own === bout.opp ? 1 : 0;
      }
      return { name, cells, points, taken, conceded, diff: taken - conceded };
    });
    const ranked = rows.map((row, i) => ({ i, ...row })).sort(
      (a, b) => b.points - a.points || b.diff - a.diff || b.taken - a.taken,
    );
    const places = new Array(n).fill("");
    for (let r = 0; r < ranked.length; ) {
      let end = r;
      while (
        end + 1 < ranked.length &&
        ranked[end + 1].points === ranked[r].points &&
        ranked[end + 1].diff === ranked[r].diff &&
        ranked[end + 1].taken === ranked[r].taken
      ) {
        end++;
      }
      for (let k = r; k <= end; k++) places[ranked[k].i] = r + 1;
      r = end + 1;
    }
    rows.forEach((row, i) => {
      row.metrics = [row.points, row.taken, row.conceded, formatDiff(row.diff), places[i] || ""];
    });
    return rows;
  }

  const TABS = [
    { key: "tables", label: "Групповой этап" },
    { key: "protocols", label: "Протоколы" },
  ];
  let activeTab = "tables";
  const brainTabsRoot = document.getElementById("brainTabs");

  function renderTabs() {
    if (!brainTabsRoot) return;
    brainTabsRoot.replaceChildren();
    for (const tab of TABS) {
      const btn = document.createElement("button");
      btn.type = "button";
      btn.className = "match-tab" + (activeTab === tab.key ? " active" : "");
      btn.textContent = tab.label;
      btn.setAttribute("role", "tab");
      btn.setAttribute("aria-selected", activeTab === tab.key ? "true" : "false");
      btn.addEventListener("click", () => {
        if (activeTab === tab.key) return;
        activeTab = tab.key;
        closeEditor();
        renderTabs();
        render();
      });
      brainTabsRoot.appendChild(btn);
    }
  }

  function groupBlock(stage) {
    const block = document.createElement("section");
    block.className = "brain-group";
    const heading = document.createElement("h2");
    heading.className = "brain-group-title";
    heading.textContent = stage.title || stage.code;
    block.appendChild(heading);
    return block;
  }

  function render() {
    editorPanel = null;
    mount.textContent = "";
    if (!festView) {
      mount.textContent = "Загрузка…";
      return;
    }
    const stages = festView.stages || [];
    if (!stages.length) {
      mount.textContent = "Группы ещё не созданы.";
      return;
    }
    if (activeTab === "protocols") renderProtocols(stages);
    else renderTables(stages);
    if (openBoutCode) refreshBoutEditor();
  }

  function renderTables(stages) {
    const grid = document.createElement("div");
    grid.className = "brain-groups";
    for (const stage of stages) {
      const block = groupBlock(stage);
      block.appendChild(gameTable.buildCrossTable({
        className: "brain-cross-table",
        teams: buildGroup(stage),
        metricHeaders: METRIC_HEADERS,
        onCellClick: canEdit ? openBout : undefined,
      }));
      grid.appendChild(block);
    }
    mount.appendChild(grid);
  }

  // renderProtocols lists every бой per group with its score, click-through to
  // the бой editor (host). Mirrors the KINSBF «протоколы» sheet.
  function renderProtocols(stages) {
    const grid = document.createElement("div");
    grid.className = "brain-groups";
    for (const stage of stages) {
      const block = groupBlock(stage);
      const table = document.createElement("table");
      table.className = "cross-table brain-protocol-list";
      const tbody = document.createElement("tbody");
      for (const match of stage.matches || []) {
        const teams = match.teams || [];
        const a = teams[0] || {};
        const b = teams[1] || {};
        const tr = document.createElement("tr");
        tr.appendChild(gameTable.td(match.title || match.code, "brain-protocol-code"));
        tr.appendChild(gameTable.td(a.name || "", "cross-team"));
        tr.appendChild(gameTable.td(`${a.total || 0} : ${b.total || 0}`, "brain-protocol-score"));
        tr.appendChild(gameTable.td(b.name || "", "cross-team"));
        if (canEdit) {
          tr.classList.add("cross-cell-link");
          tr.tabIndex = 0;
          tr.addEventListener("click", () => openBout(match.code));
        }
        tbody.appendChild(tr);
      }
      table.appendChild(tbody);
      block.appendChild(table);
      grid.appendChild(block);
    }
    mount.appendChild(grid);
  }

  // --- бой protocol editor -------------------------------------------------

  let editorPanel = null;
  let editorBout = null;

  function openBout(code) {
    openBoutCode = code;
    fetchBout(code).then((bout) => {
      editorBout = bout;
      showEditor();
    });
  }

  function closeEditor() {
    openBoutCode = null;
    editorBout = null;
    if (editorPanel) {
      editorPanel.remove();
      editorPanel = null;
    }
  }

  function fetchBout(code) {
    return fetch(`${apiBase}/bouts/${encodeURIComponent(code)}`, { credentials: "same-origin" })
      .then((response) => (response.ok ? response.json() : null));
  }

  function refreshBoutEditor() {
    if (!openBoutCode) return;
    fetchBout(openBoutCode).then((bout) => {
      if (!bout || openBoutCode !== bout.code) return;
      editorBout = bout;
      showEditor();
    });
  }

  function postEdit(body) {
    return fetch(`${apiBase}/bouts/${encodeURIComponent(editorBout.code)}/update`, {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    }).then((response) => {
      if (!response.ok) return null;
      return response.json();
    }).then((bout) => {
      if (bout) {
        editorBout = bout;
        showEditor();
      }
    });
  }

  const MARK_CYCLE = { "": "right", right: "wrong", wrong: "" };

  function showEditor() {
    if (!editorBout) return;
    if (!editorPanel) {
      editorPanel = document.createElement("div");
      editorPanel.className = "brain-editor";
      mount.appendChild(editorPanel);
    }
    editorPanel.textContent = "";

    const header = document.createElement("div");
    header.className = "brain-editor-head";
    const teams = editorBout.teams || [];
    const scoreA = teamTaken(teams[0]);
    const scoreB = teamTaken(teams[1]);
    header.textContent = `${teamName(teams[0])}  ${scoreA} : ${scoreB}  ${teamName(teams[1])}`;
    editorPanel.appendChild(header);

    const table = document.createElement("table");
    table.className = "match-table brain-bout-table";
    const thead = document.createElement("thead");
    const hr = document.createElement("tr");
    hr.appendChild(gameTable.th("", "brain-bout-corner"));
    for (let q = 0; q < editorBout.questionCount; q++) hr.appendChild(gameTable.th(String(q + 1), "brain-q-head"));
    thead.appendChild(hr);
    table.appendChild(thead);

    const tbody = document.createElement("tbody");
    teams.forEach((team, teamIndex) => {
      // player row
      const playerRow = document.createElement("tr");
      playerRow.className = "brain-player-row";
      playerRow.appendChild(gameTable.td(teamName(team), "brain-bout-team", { rowSpan: 2 }));
      for (let q = 0; q < editorBout.questionCount; q++) {
        const cell = gameTable.td(playerSelect(team, teamIndex, q), "brain-player-cell");
        playerRow.appendChild(cell);
      }
      tbody.appendChild(playerRow);
      // mark row
      const markRow = document.createElement("tr");
      for (let q = 0; q < editorBout.questionCount; q++) {
        markRow.appendChild(markCell(team, teamIndex, q));
      }
      tbody.appendChild(markRow);
    });
    table.appendChild(tbody);
    editorPanel.appendChild(table);

    const footer = document.createElement("div");
    footer.className = "brain-editor-foot";
    const finishBtn = document.createElement("button");
    finishBtn.type = "button";
    finishBtn.className = "btn";
    finishBtn.textContent = editorBout.finished ? "Открыть бой" : "Завершить бой";
    finishBtn.addEventListener("click", () => postEdit({ team: 0, finished: !editorBout.finished }));
    const closeBtn = document.createElement("button");
    closeBtn.type = "button";
    closeBtn.className = "btn btn-ghost";
    closeBtn.textContent = "Закрыть";
    closeBtn.addEventListener("click", closeEditor);
    footer.appendChild(finishBtn);
    footer.appendChild(closeBtn);
    editorPanel.appendChild(footer);
  }

  function teamName(team) {
    return team ? team.name || "" : "";
  }

  function teamTaken(team) {
    if (!team || !team.questions) return 0;
    return team.questions.filter((q) => q && q.mark === "right").length;
  }

  function markCell(team, teamIndex, questionIndex) {
    const question = (team.questions || [])[questionIndex] || {};
    const cell = gameTable.td(question.mark === "right" ? "+" : question.mark === "wrong" ? "−" : "", "answer-cell brain-mark");
    gameTable.setMarkClass(cell, question.mark);
    if (!editorBout.finished && team.teamID) {
      cell.tabIndex = 0;
      cell.addEventListener("click", () => {
        const next = MARK_CYCLE[question.mark || ""];
        postEdit({ team: teamIndex, question: questionIndex, setMark: true, mark: next });
      });
    }
    return cell;
  }

  function playerSelect(team, teamIndex, questionIndex) {
    const question = (team.questions || [])[questionIndex] || {};
    const select = document.createElement("select");
    select.className = "brain-player-select";
    select.disabled = editorBout.finished || !team.teamID;
    const blank = document.createElement("option");
    blank.value = "0";
    blank.textContent = "—";
    select.appendChild(blank);
    for (const player of team.roster || []) {
      const option = document.createElement("option");
      option.value = String(player.id);
      option.textContent = player.name;
      if (player.name === question.player) option.selected = true;
      select.appendChild(option);
    }
    select.addEventListener("change", () => {
      postEdit({ team: teamIndex, question: questionIndex, setPlayer: true, player: Number(select.value) });
    });
    return select;
  }

  // --- live sync -----------------------------------------------------------

  function connectEvents() {
    if (!festID || !gameID) return;
    const source = new EventSource(gameTable.gameEventsURL(festID, gameID));
    source.addEventListener("open", () => setStatus("saved"));
    source.addEventListener("error", () => setStatus("error"));
    source.addEventListener("state", (event) => {
      const parsed = gameTable.parseScopedEvent(event.data);
      if (!parsed || !parsed.scope || parsed.scope.indexOf("fest:") !== 0) return;
      if (parsed.data) {
        festView = parsed.data;
        render();
        renderBreadcrumbs();
      }
    });
  }

  renderBreadcrumbs();
  renderTabs();
  render();
  setStatus("saved");
  connectEvents();
})();
