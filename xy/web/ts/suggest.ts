// The one filtered dropdown for free-text fields: testers, towns, timezones —
// wherever a native <select> cannot go because the list is too long to scroll or
// has to be searched. Shared so a field cannot silently ship without one, which
// is how /profile and the first-run modal ended up with a bare timezone box
// while the session form had a working picker.

import { xyApp } from "./app.js";
import { allZones, zoneOffset } from "./sessions.js";
import { TOWNS } from "./towns.js";

const { el } = xyApp;

export interface Choice {
  value: string;
  label: string;
  hint?: string;
}

export function autocomplete(
  inp: HTMLInputElement,
  choices: (q: string) => Choice[],
  onPick?: (c: Choice) => void,
): void {
  let pop: HTMLElement | null = null;
  const dismiss = (): void => {
    if (pop) {
      pop.remove();
      pop = null;
    }
  };
  inp.addEventListener("blur", () => setTimeout(dismiss, 150));

  const draw = (): void => {
    dismiss();
    const hits = choices(inp.value);
    if (!hits.length) return;
    pop = el("div", { class: "menu-dropdown suggest-pop" });
    for (const h of hits) {
      pop.append(el("button", {
        class: "menu-item", type: "button",
        onmousedown: (e: Event) => {
          e.preventDefault();
          inp.value = h.value;
          inp.dispatchEvent(new Event("input", { bubbles: true }));
          dismiss();
          if (onPick) onPick(h);
        },
      },
        el("span", { text: h.label }),
        h.hint ? el("span", { class: "suggest-hint", text: h.hint }) : el("span"),
      ));
    }
    // The popup is absolutely positioned, so its parent must be the positioning
    // context — marked here rather than at bind time, because a field is often
    // wired before it is appended to anything.
    const host = inp.parentElement;
    if (!host) return;
    host.classList.add("suggest-anchor");
    host.append(pop);
    // top:100% would mean the bottom of the ANCHOR, which is only the field on a
    // form that wraps each one. /profile puts every input in a single column, so
    // the popup landed under the whole form. Measure from the input's own box.
    const hostBox = host.getBoundingClientRect();
    const inpBox = inp.getBoundingClientRect();
    pop.style.top = `${Math.round(inpBox.bottom - hostBox.top)}px`;
    pop.style.left = `${Math.round(inpBox.left - hostBox.left)}px`;
    pop.style.minWidth = `${Math.round(inpBox.width)}px`;
  };

  inp.addEventListener("input", draw);
  inp.addEventListener("focus", draw);
}

// Nobody should have to know that Almaty is Asia/Almaty, so the zone picker
// searches Russian city names as well as IANA ids.
let townsByZone: Map<string, string[]> | null = null;
function zoneTowns(): Map<string, string[]> {
  if (townsByZone) return townsByZone;
  townsByZone = new Map();
  for (const c of TOWNS) {
    if (!c.zone) continue;
    const list = townsByZone.get(c.zone);
    if (list) list.push(c.name);
    else townsByZone.set(c.zone, [c.name]);
  }
  return townsByZone;
}

export function zoneChoices(q: string): Choice[] {
  const needle = q.trim().toLowerCase();
  const zones = allZones();
  if (!needle) return zones.slice(0, 12).map((z) => ({ value: z, label: z, hint: zoneOffset(z) }));

  const out: Choice[] = [];
  const seen = new Set<string>();
  // A city the user typed beats an IANA id that merely contains the letters.
  for (const c of TOWNS) {
    if (!c.zone || seen.has(c.zone)) continue;
    if (!c.name.toLowerCase().startsWith(needle)) continue;
    seen.add(c.zone);
    out.push({ value: c.zone, label: c.name, hint: `${c.zone} · ${zoneOffset(c.zone)}` });
    if (out.length >= 8) break;
  }
  for (const z of zones) {
    if (out.length >= 12 || seen.has(z)) continue;
    if (!z.toLowerCase().includes(needle) && !zoneOffset(z).toLowerCase().includes(needle)) continue;
    seen.add(z);
    const towns = (zoneTowns().get(z) || []).slice(0, 2).join(", ");
    out.push({ value: z, label: z, hint: towns ? `${towns} · ${zoneOffset(z)}` : zoneOffset(z) });
  }
  return out;
}

// A town brings its timezone with it, so picking Almaty fills the zone too.
export function townChoices(q: string): Choice[] {
  const needle = q.trim().toLowerCase();
  const pool = needle ? TOWNS.filter((c) => c.name.toLowerCase().startsWith(needle)) : TOWNS;
  return pool.slice(0, 10).map((c) => ({
    value: c.name,
    label: c.name,
    hint: c.zone ? zoneOffset(c.zone) : "",
  }));
}
