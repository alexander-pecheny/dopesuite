// The datetime field (kit's `datetimefield`): a text input that takes a typed
// or pasted «2026-09-04 19:00», with the kit's own Monday-first calendar
// popping beside it. The browser's built-in controls follow the browser locale
// and cannot be told otherwise — a Monday-first grid and a 24-hour clock are
// not on offer — so the kit draws both. The text input stays what the form
// posts, and the calendar only ever writes into it.

const TEXT = "[data-datetime-text]";
const OPEN = "[data-datetime-open]";

// The week starts on Monday everywhere this kit is used.
const WEEKDAYS = ["пн", "вт", "ср", "чт", "пт", "сб", "вс"];

const MONTH = new Intl.DateTimeFormat("ru", {month: "long"});
const FULL = new Intl.DateTimeFormat("ru", {day: "numeric", month: "long", year: "numeric"});

export interface ParsedValue {
  date: string; // YYYY-MM-DD
  time: string; // HH:MM, "" when the field carries none
}

// parseValue reads what the field holds. A bare date parses; anything else —
// «завтра», a lone «19:00» — is none of the calendar's business.
export function parseValue(text: string): ParsedValue | null {
  const m = text.trim().match(/^(\d{4})-(\d{2})-(\d{2})(?:[T ](\d{2}):(\d{2}))?/);
  if (!m) return null;
  return {
    date: `${m[1]}-${m[2]}-${m[3]}`,
    time: m[4] !== undefined && m[5] !== undefined ? `${m[4]}:${m[5]}` : "",
  };
}

// composeValue writes a pick: the new date over the old, the time kept as
// typed — the minutes are often what a person corrects by hand afterwards.
export function composeValue(date: string, time: string): string {
  const t = time.trim();
  return t ? `${date} ${t}` : date;
}

// gridOf lays one month out Monday-first: how many leading cells belong to the
// previous month, and how long the month itself is.
export function gridOf(year: number, month: number): {lead: number; days: number} {
  return {
    lead: (new Date(year, month, 1).getDay() + 6) % 7,
    days: new Date(year, month + 1, 0).getDate(),
  };
}

const pad2 = (n: number): string => String(n).padStart(2, "0");

// A time is complete once it carries its minutes: «19:00», «19.00», «1900».
const WHOLE_TIME = /^(\d{1,2}\D\d{2}|\d{4})$/;

// normalizeTime reads a 24-hour wall clock out of whatever was typed — «19:00»,
// «19.30», «1930», «9». The browser's own time input is the one control the kit
// cannot make speak 24 hours: it follows the browser locale and offers AM/PM,
// which nobody writing these times uses. "" is what does not parse.
export function normalizeTime(raw: string): string {
  const text = raw.trim();
  if (!text) return "";
  let hours = "";
  let minutes = "";
  const split = text.match(/^(\d{1,2})\D(\d{1,2})$/);
  if (split) {
    [, hours, minutes] = split;
  } else {
    if (!/^\d{1,4}$/.test(text)) return "";
    hours = text.length <= 2 ? text : text.slice(0, text.length - 2);
    minutes = text.length <= 2 ? "0" : text.slice(-2);
  }
  const h = Number(hours);
  const m = Number(minutes);
  if (h > 23 || m > 59) return "";
  return `${pad2(h)}:${pad2(m)}`;
}

export function mountDatetimeField(field: HTMLElement): void {
  const text = field.querySelector<HTMLInputElement>(TEXT);
  const button = field.querySelector<HTMLElement>(OPEN);
  if (!text || !button) return;

  let pop: HTMLElement | null = null;
  let stop: AbortController | null = null;
  let title: HTMLElement;
  let grid: HTMLElement;
  let timeInput: HTMLInputElement | null = null;
  let view = {year: 0, month: 0};
  const close = (): void => {
    stop?.abort();
    stop = null;
    timeInput = null;
    pop?.remove();
    pop = null;
  };

  // The calendar opens on the value's month — or on today's, when the field is
  // empty or says something it cannot read.
  const aim = (): void => {
    const parsed = parseValue(text.value);
    const now = new Date();
    view = parsed
      ? {year: Number(parsed.date.slice(0, 4)), month: Number(parsed.date.slice(5, 7)) - 1}
      : {year: now.getFullYear(), month: now.getMonth()};
  };

  const commit = (value: string): void => {
    text.value = value;
    text.dispatchEvent(new Event("input", {bubbles: true}));
  };

  const finish = (): void => {
    close();
    text.focus();
  };

  const pickDay = (iso: string): void => {
    const time = timeInput ? timeInput.value : (parseValue(text.value)?.time ?? "");
    commit(composeValue(iso, time));
    fill();
  };

  const step = (delta: number): void => {
    view.month += delta;
    if (view.month < 0) {
      view.month = 11;
      view.year -= 1;
    }
    if (view.month > 11) {
      view.month = 0;
      view.year += 1;
    }
    fill();
  };

  // fill redraws the title and the whole grid for view — the weekday headers
  // included, they are part of the same replace. The Date constructor does the
  // neighbouring-month arithmetic: day 0 is the last of the month before, day
  // 32 the first of the one after.
  const fill = (): void => {
    const selected = parseValue(text.value)?.date ?? "";
    const now = new Date();
    const today = `${now.getFullYear()}-${pad2(now.getMonth() + 1)}-${pad2(now.getDate())}`;
    title.textContent = `${MONTH.format(new Date(view.year, view.month, 1))} ${view.year}`;
    const cells: HTMLElement[] = [];
    for (const day of WEEKDAYS) {
      const cell = document.createElement("span");
      cell.className = "calendar-dow";
      cell.textContent = day;
      cells.push(cell);
    }
    const {lead, days} = gridOf(view.year, view.month);
    for (let i = 0; i < Math.ceil((lead + days) / 7) * 7; i += 1) {
      const d = new Date(view.year, view.month, i - lead + 1);
      const iso = `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}`;
      const cell = document.createElement("button");
      cell.type = "button";
      cell.className = "calendar-day" + (d.getMonth() === view.month ? "" : " is-out")
        + (iso === selected ? " is-selected" : "")
        + (iso === today ? " is-today" : "");
      if (iso === selected) cell.setAttribute("aria-current", "date");
      cell.setAttribute("aria-label", FULL.format(d));
      cell.textContent = String(d.getDate());
      cell.addEventListener("click", () => pickDay(iso));
      cells.push(cell);
    }
    grid.replaceChildren(...cells);
  };

  // Arrow keys walk the grid; with nothing focused yet, they start from the
  // selected day, the first of the month when there is none.
  const move = (event: Event): void => {
    const deltas: Record<string, number> = {ArrowLeft: -1, ArrowRight: 1, ArrowUp: -7, ArrowDown: 7};
    const delta = deltas[(event as KeyboardEvent).key];
    if (delta === undefined || !pop) return;
    const days = [...pop.querySelectorAll<HTMLButtonElement>(".calendar-day")];
    const at = days.indexOf(document.activeElement as HTMLButtonElement);
    const from = at >= 0 ? at : days.findIndex((d) => d.classList.contains("is-selected"));
    const next = days[from + delta] ?? (from === -1 ? days[0] : undefined);
    if (!next) return;
    event.preventDefault();
    next.focus();
  };

  const open = (): void => {
    if (pop) return;
    aim();
    pop = document.createElement("div");
    pop.className = "calendar-pop";
    pop.setAttribute("role", "dialog");
    pop.setAttribute("aria-label", "Календарь");

    const head = document.createElement("div");
    head.className = "u-row u-align-center u-gap-xs";
    const nav = (glyph: string, label: string, delta: number): HTMLElement => {
      const b = document.createElement("button");
      b.type = "button";
      b.className = "btn btn-ghost btn-small";
      b.setAttribute("aria-label", label);
      b.textContent = glyph;
      b.addEventListener("click", () => step(delta));
      return b;
    };
    title = document.createElement("span");
    title.className = "calendar-title";
    head.append(nav("‹", "Предыдущий месяц", -1), title, nav("›", "Следующий месяц", 1));

    grid = document.createElement("div");
    grid.className = "calendar-grid";

    const timeRow = document.createElement("div");
    timeRow.className = "u-row u-align-center u-justify-between u-gap-xs";
    const timeLabel = document.createElement("span");
    timeLabel.className = "calendar-tz";
    timeLabel.textContent = "Время";
    timeInput = document.createElement("input");
    timeInput.type = "text";
    timeInput.className = "calendar-time-input";
    timeInput.inputMode = "numeric";
    timeInput.maxLength = 5;
    timeInput.size = 5;
    timeInput.placeholder = "19:00";
    timeInput.setAttribute("aria-label", "Время");
    timeInput.value = parseValue(text.value)?.time || "";
    const writeTime = (time: string): void => {
      const date = parseValue(text.value)?.date;
      if (date) commit(composeValue(date, time));
    };
    timeInput.addEventListener("input", () => {
      if (timeInput && WHOLE_TIME.test(timeInput.value.trim())) {
        writeTime(normalizeTime(timeInput.value));
      }
    });
    // Half a time — «9», «19:0» — is finished for the person on the way out.
    timeInput.addEventListener("change", () => {
      if (!timeInput) return;
      timeInput.value = normalizeTime(timeInput.value);
      writeTime(timeInput.value);
    });
    timeInput.addEventListener("keydown", (event) => {
      if (event.key === "Enter") {
        event.preventDefault();
        if (timeInput) {
          timeInput.value = normalizeTime(timeInput.value);
          writeTime(timeInput.value);
        }
        finish();
      }
    });
    timeRow.append(timeLabel, timeInput);

    const foot = document.createElement("div");
    foot.className = "u-row u-align-center u-justify-between u-gap-xs";
    const clear = document.createElement("button");
    clear.type = "button";
    clear.className = "btn btn-ghost btn-small";
    clear.textContent = "Очистить";
    clear.addEventListener("click", () => {
      if (timeInput) timeInput.value = "";
      commit("");
      finish();
    });
    const done = document.createElement("button");
    done.type = "button";
    done.className = "btn btn-primary btn-small";
    done.textContent = "Готово";
    done.addEventListener("click", finish);
    foot.append(clear, done);

    // The zone a value is written in, when the page says one: the caption that
    // keeps a picked wall-clock unambiguous.
    const zone = text.dataset.datetimeTz || "";
    let tzRow: HTMLElement | null = null;
    if (zone) {
      tzRow = document.createElement("div");
      tzRow.className = "u-row u-align-center u-justify-between u-gap-xs";
      const tz = document.createElement("span");
      tz.className = "calendar-tz";
      tz.textContent = `Часовой пояс: ${zone}`;
      tzRow.append(tz);
    }

    pop.append(head, grid, timeRow, foot);
    if (tzRow) pop.append(tzRow);
    field.append(pop);

    stop = new AbortController();
    const opts = {signal: stop.signal};
    document.addEventListener("pointerdown", (event) => {
      const target = event.target;
      if (!(target instanceof Node)) {
        close();
        return;
      }
      if (pop && (pop.contains(target) || button.contains(target))) return;
      close();
    }, opts);
    pop.addEventListener("keydown", (event) => {
      if ((event as KeyboardEvent).key === "Escape") {
        close();
        text.focus();
        return;
      }
      move(event);
    }, opts);

    fill();
  };

  button.addEventListener("click", () => (pop ? close() : open()));

  // A click in the text field opens the calendar too — the same tap the old
  // native picker answered — but focus alone does not: tabbing through the
  // form must not pop calendars open.
  text.addEventListener("click", open);
}

export function mountDatetimeFields(doc: Document): void {
  doc.querySelectorAll<HTMLElement>("[data-datetime-field]").forEach(mountDatetimeField);
}
