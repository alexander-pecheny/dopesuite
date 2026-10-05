// The choice sources xy's suggest fields draw from: timezones and towns. The
// picker itself is the kit's (dopeuikit/assets/ts/suggest.ts, imported here as
// ./kit/suggest.js) — it is shared so a field cannot silently ship without one,
// which is how /profile and the first-run modal ended up with a bare timezone
// box while the session form had a working picker.

import { allZones, zoneOffset } from "./sessions.js";
import { TOWNS } from "./towns.js";
import type { Choice } from "./kit/suggest.js";

// Zones offered at most, and how many of them may come from town names.
const MAX_ZONE_CHOICES = 12;
const MAX_TOWN_ZONE_CHOICES = 8;
const MAX_TOWN_CHOICES = 10;

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
  if (!needle) return zones.slice(0, MAX_ZONE_CHOICES).map((z) => ({ value: z, label: z, hint: zoneOffset(z) }));

  const out: Choice[] = [];
  const seen = new Set<string>();
  // A city the user typed beats an IANA id that merely contains the letters.
  for (const c of TOWNS) {
    if (!c.zone || seen.has(c.zone)) continue;
    if (!c.name.toLowerCase().startsWith(needle)) continue;
    seen.add(c.zone);
    out.push({ value: c.zone, label: c.name, hint: `${c.zone} · ${zoneOffset(c.zone)}` });
    if (out.length >= MAX_TOWN_ZONE_CHOICES) break;
  }
  for (const z of zones) {
    if (out.length >= MAX_ZONE_CHOICES || seen.has(z)) continue;
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
  return pool.slice(0, MAX_TOWN_CHOICES).map((c) => ({
    value: c.name,
    label: c.name,
    hint: c.zone ? zoneOffset(c.zone) : "",
  }));
}
