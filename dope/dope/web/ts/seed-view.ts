import S from "./i18nstrings.js";

// The Посев tab a seeded Game shows its host: the declared source, the one
// import button (or the xlsx upload), and the ladder — active seeds, declines,
// waitlist. The server side is one for every Game (the /seed-import routes);
// brain, Троечка and Хамса draw the same tab, so it lives here once.

interface SeedImportRow {
  sourceRank?: number;
  seedNumber?: number;
  teamID?: number;
  name?: string;
  city?: string;
  declined?: boolean;
  waitlist?: boolean;
}

interface SeedImportData {
  source?: string;
  drawSize?: number;
  activeCount?: number;
  rows?: SeedImportRow[];
}

export interface SeedViewOptions {
  apiBase: string | undefined;
  // The [init] seed: word the scheme declares — xlsx gets the upload.
  source: () => string;
  // Redraw the page, keeping its scroll: the tab is rebuilt from state.
  rerender: () => void;
  // An import reseats every Slot. The page gets the fest view fetched afresh
  // (null if that failed) — the Сетка reads its names from it, not from the
  // бои — and refetches what it shows of the бои itself.
  afterChange: (fest: SeedFestView | null) => Promise<void>;
}

// The part of the Game's fest view (GET apiBase) an import changes.
export interface SeedFestView {
  stages?: Array<{code?: string} | null>;
}

async function fetchFestView(apiBase: string | undefined): Promise<SeedFestView | null> {
  try {
    const response = await fetch(`${apiBase}`);
    return response.ok ? await response.json() as SeedFestView : null;
  } catch {
    return null;
  }
}

export interface SeedView {
  build(): HTMLElement;
}

export function createSeedView(options: SeedViewOptions): SeedView {
  let seedImport: SeedImportData | null = null;
  let seedError = "";
  let seedLoaded = false;
  let seedBusy = false;
  const {apiBase, rerender} = options;

  async function seedAction(run: () => Promise<Response>): Promise<void> {
    if (seedBusy) return;
    seedBusy = true;
    rerender();
    try {
      const response = await run();
      if (!response.ok) throw new Error(await response.text());
      seedImport = await response.json() as SeedImportData;
      seedError = "";
      await options.afterChange(await fetchFestView(apiBase));
    } catch (error) {
      seedError = error instanceof Error ? error.message.trim() : String(error);
    }
    seedBusy = false;
    rerender();
  }

  function load(): void {
    if (seedLoaded) return;
    seedLoaded = true;
    fetch(`${apiBase}/seed-import`)
      .then(async (response) => {
        if (!response.ok) throw new Error(await response.text());
        const data = await response.json() as SeedImportData;
        // An action's response may have landed while this GET was in flight —
        // the freshly imported ladder must not be clobbered by the stale read.
        if (!seedImport) {
          seedImport = data;
          rerender();
        }
      })
      .catch(async (error: unknown) => {
        seedError = error instanceof Error ? error.message.trim() : String(error);
        rerender();
      });
  }

  function build(): HTMLElement {
    load();
    const wrap = document.createElement("div");
    wrap.className = "brain-protocol";
    const source = options.source();

    const bar = document.createElement("div");
    bar.className = "brain-seed-bar";
    if (source === "xlsx") {
      const file = document.createElement("input");
      file.type = "file";
      file.accept = ".xlsx";
      file.className = "brain-seed-file";
      const upload = document.createElement("button");
      upload.type = "button";
      upload.className = "btn";
      upload.textContent = S.brain.seed.upload();
      upload.disabled = seedBusy;
      upload.addEventListener("click", () => {
        const chosen = file.files?.[0];
        if (!chosen) {
          seedError = S.brain.seed.noFile();
          rerender();
          return;
        }
        const body = new FormData();
        body.append("file", chosen);
        void seedAction(() => fetch(`${apiBase}/seed-import/xlsx`, {method: "POST", body}));
      });
      bar.append(file, upload);
    } else {
      const importButton = document.createElement("button");
      importButton.type = "button";
      importButton.className = "btn";
      importButton.textContent = source === "random" ? S.brain.seed.draw() : S.brain.seed.importFrom(source);
      importButton.disabled = seedBusy;
      importButton.addEventListener("click", () => {
        void seedAction(() => fetch(`${apiBase}/seed-import/run`, {method: "POST"}));
      });
      bar.appendChild(importButton);
    }
    wrap.appendChild(bar);

    if (seedError) {
      const error = document.createElement("p");
      error.className = "brain-seed-error";
      error.textContent = seedError;
      wrap.appendChild(error);
    }

    const rows = seedImport?.rows || [];
    if (!rows.length) {
      const empty = document.createElement("p");
      empty.className = "roster-empty";
      empty.textContent = S.brain.seed.empty();
      wrap.appendChild(empty);
      return wrap;
    }

    const table = document.createElement("table");
    table.className = "match-table brain-seed-table";
    const thead = document.createElement("thead");
    const head = document.createElement("tr");
    for (const text of [S.brain.seedHead.seed(), S.brain.seedHead.team(), S.brain.seedHead.city(), S.brain.seedHead.rank(), S.brain.seedHead.declined()]) {
      const th = document.createElement("th");
      th.textContent = text;
      head.appendChild(th);
    }
    thead.appendChild(head);
    table.appendChild(thead);
    const tbody = document.createElement("tbody");
    for (const row of rows) {
      const tr = document.createElement("tr");
      tr.classList.toggle("brain-seed-waitlist", Boolean(row.waitlist));
      tr.classList.toggle("brain-seed-declined", Boolean(row.declined));
      const seed = document.createElement("td");
      seed.className = "number";
      seed.textContent = row.declined ? "—" : row.waitlist ? S.brain.seed.waitlist() : String(row.seedNumber || "");
      const name = document.createElement("td");
      name.className = "brain-seed-name";
      name.textContent = row.name || "";
      const city = document.createElement("td");
      city.textContent = row.city || "";
      const rank = document.createElement("td");
      rank.className = "number";
      rank.textContent = String(row.sourceRank || "");
      const decline = document.createElement("td");
      const checkbox = document.createElement("input");
      checkbox.type = "checkbox";
      checkbox.checked = Boolean(row.declined);
      checkbox.disabled = seedBusy;
      checkbox.addEventListener("change", () => {
        void seedAction(() => fetch(`${apiBase}/seed-import/decline`, {
          method: "POST",
          headers: {"Content-Type": "application/json"},
          body: JSON.stringify({teamID: row.teamID, declined: checkbox.checked}),
        }));
      });
      decline.appendChild(checkbox);
      tr.append(seed, name, city, rank, decline);
      tbody.appendChild(tr);
    }
    table.appendChild(tbody);
    wrap.appendChild(table);
    return wrap;
  }

  return {build};
}
