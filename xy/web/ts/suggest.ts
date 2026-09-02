// xy's own choices for the shared suggest dropdown: the timezone picker and
// the town picker. The dropdown itself lives in dopeuikit (both apps use it).

import { allZones, zoneOffset } from "./sessions.js";
import { TOWNS } from "./towns.js";
import { autocomplete } from "../../../dopeuikit/assets/ts/suggest.js";
import type { Choice } from "../../../dopeuikit/assets/ts/suggest.js";

export { autocomplete };
export type { Choice };

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
