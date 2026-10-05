// ek-protocol.ts — the EK protocol's edits as the page sends them (ADR-0018):
// a cell edit on the sheet, the view path the writer tracks it under, and the
// wire op the server stores. Pure: every function takes what it reads. EK,
// Erudit-Sextet and individual SI share it.
//
// The sheet addresses a team by its slot in the bout and a player by name; the
// server's blob keys a team by its id, a player by id, and a host's place is a
// pin (ADR-0005). The two translations below are the whole of that difference.

// A place op's view path is participants/slot/place.
const PLACE_OP_PATH_LEN = 3;

// EKCellPayload is one cell edit as the sheet makes it.
export type EKCellPayload = {
  team: number;
  theme?: number;
  answer?: number;
  mark?: string;
  // The theme's whole seating, by display name: a seat edit sends the list it
  // leaves behind, never a difference.
  players?: string[];
  place?: number;
  shootout?: boolean;
};

// BlobOp is one wire operation against a match's Protocol state blob: a path of
// object keys / array indices, and the value to set there. Team sections are
// keyed by team id (a string), theme players are player ids, and a host's place
// is a pin: the server resolves nothing by name (ADR-0005).
export type BlobOp = {op?: "set" | "remove"; path: Array<string | number>; value?: unknown};

// ViewOp is an edit as the writer queues it: a path on the match view.
export type ViewOp = {path: Array<string | number>; value: unknown};

// EKBoutView is what the translation reads of a bout's view: who sits in each
// slot, and the people each may seat.
export interface EKBoutView {
  participants?: Array<{id?: number; roster?: Array<{id: number; name: string}>} | null | undefined>;
}

// opPath maps a cell edit to its match-view path (the server's matchDeltaOps
// shape). Null for a payload that is not a cell, which must not be tracked as
// an overlay.
export function opPath(payload: EKCellPayload): Array<string | number> | null {
  if (payload.place !== undefined) return ["participants", payload.team, "place"];
  const themesKey = payload.shootout ? "shootoutThemes" : "themes";
  if (payload.players !== undefined) return ["participants", payload.team, themesKey, payload.theme!, "players"];
  if (payload.mark !== undefined) return ["participants", payload.team, themesKey, payload.theme!, "answers", payload.answer!];
  return null;
}

// opValue is the value a cell edit sets at its path.
export function opValue(payload: EKCellPayload): unknown {
  if (payload.place !== undefined) return payload.place;
  if (payload.players !== undefined) return payload.players;
  return payload.mark;
}

// blobOp translates a queued view-path op into the blob path the server stores
// it at: the team's slot becomes its id, a place becomes a pin (an empty one
// clears it, handing the place back to the scorer), and a player's name
// becomes their id. Null when the view no longer holds that team, or no longer
// knows a seated name: the op is then unsendable and is dropped rather than
// retried forever.
export function blobOp(op: ViewOp, view: EKBoutView): BlobOp | null {
  const [, slot, key, theme, leaf, answer] = op.path;
  const team = view.participants?.[slot as number];
  if (!team?.id) return null;
  const teamKey = String(team.id);
  if (op.path.length === PLACE_OP_PATH_LEN) {
    const path = ["participants", teamKey, "pin"];
    return op.value ? {path, value: op.value} : {op: "remove", path};
  }
  if (leaf === "players") {
    const ids: number[] = [];
    for (const name of (op.value as string[]) || []) {
      const member = (team.roster || []).find((player) => player.name === name);
      if (!member) return null;
      ids.push(member.id);
    }
    return {path: ["participants", teamKey, key as string, theme as number, "players"], value: ids};
  }
  return {path: ["participants", teamKey, key as string, theme as number, "answers", answer as number], value: op.value};
}
