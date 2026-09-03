// dope's choices for the shared suggest dropdown: the profile timezone.
// The dropdown itself lives in dopeuikit (both apps use it); xy's picker
// additionally searches ChGK town names, which dope has no data for.

import type {Choice} from "../../../../dopeuikit/assets/ts/suggest.js";

// zoneOffset is the zone's current UTC offset, «UTC+3»-shaped. "" when the
// zone is not one this browser knows.
export function zoneOffset(zone: string, at: Date = new Date()): string {
  try {
    const fmt = new Intl.DateTimeFormat("en-US", {timeZone: zone, timeZoneName: "longOffset"});
    const part = fmt.formatToParts(at).find((p) => p.type === "timeZoneName");
    const raw = (part && part.value) || "GMT";
    const norm = raw.replace("GMT", "UTC");
    return norm === "UTC" ? "UTC+0" : norm.replace(/:00$/, "").replace(/UTC([+-])0(\d)/, "UTC$1$2");
  } catch {
    return "";
  }
}

// guessZone is the device's own zone — the prefill for an account that has
// not answered the profile question yet.
export function guessZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "";
  } catch {
    return "";
  }
}

export function allZones(): string[] {
  try {
    const f = Intl as unknown as {supportedValuesOf?: (k: string) => string[]};
    return f.supportedValuesOf ? f.supportedValuesOf("timeZone") : [];
  } catch {
    return [];
  }
}

export function zoneChoices(q: string): Choice[] {
  const needle = q.trim().toLowerCase();
  const zones = allZones();
  if (!needle) return zones.slice(0, 12).map((z) => ({value: z, label: z, hint: zoneOffset(z)}));
  const out: Choice[] = [];
  for (const z of zones) {
    if (out.length >= 12) break;
    if (!z.toLowerCase().includes(needle) && !zoneOffset(z).toLowerCase().includes(needle)) continue;
    out.push({value: z, label: z, hint: zoneOffset(z)});
  }
  return out;
}
