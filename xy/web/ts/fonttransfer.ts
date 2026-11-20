// fonttransfer.ts — putting Font Families (ADR-0025) on a board: storing one,
// and bringing along the ones copied pictures are set in. Shared by the
// board's own uploads (boardfonts.ts), a card copied across boards
// (transfer.ts) and every Bundle apply (bundleapply.ts).
import { xyCrypto } from "./crypto.js";
import type { DataKey } from "./crypto.js";
import type { FontFamily } from "./xyimagefonts.js";
import type { BoardFont } from "./unlock.js";

// META_VERSION is the meta's own version: bumped on a change of meaning only.
const META_VERSION = 1;

// postFamily stores a Family on a board: its meta and its zip, each
// encrypted under that board's key.
export async function postFamily(boardId: number, key: DataKey, family: FontFamily, zip: Uint8Array<ArrayBuffer>): Promise<BoardFont> {
  const fd = new FormData();
  fd.set("meta", await xyCrypto.encField(key, JSON.stringify({ v: META_VERSION, hash: family.hash, name: family.name, faces: family.faces })));
  fd.set("blob", new Blob([await xyCrypto.encBytes(key, zip)]), "blob");
  const res = await fetch(`/api/boards/${boardId}/fonts`, { method: "POST", body: fd, credentials: "same-origin" });
  if (!res.ok) throw new Error((await res.text()).trim() || `HTTP ${res.status}`);
  const { id, size } = (await res.json()) as { id: number; size: number };
  return { hash: family.hash, name: family.name, faces: family.faces, id, size };
}

// FamilySource is where a Transfer reads a Family from: the source board's
// fonts, or a Bundle's.
export interface FamilySource {
  family(hash: string): FontFamily | undefined;
  zip(hash: string): Promise<Uint8Array<ArrayBuffer> | null>;
}

// copyFamilies puts on a board the Families some pictures name that it lacks
// (has: the hashes it holds, added to as it goes). A Family the source cannot
// hand over is reported by name, or by hash when even that is unknown; the
// picture then exports only as its raster until the font is uploaded again.
export async function copyFamilies(hashes: Iterable<string>, from: FamilySource, to: { boardId: number; dk: DataKey; has: Set<string>; stored?(row: BoardFont): void }): Promise<string[]> {
  const missing: string[] = [];
  for (const h of hashes) {
    if (to.has.has(h)) continue;
    const fam = from.family(h);
    const zip = fam ? await from.zip(h).catch(() => null) : null;
    if (!fam || !zip) { missing.push(fam?.name ?? h); continue; }
    // Stored first: an optional call skips its arguments when there is no one
    // to call, so this must not ride inside one.
    const row = await postFamily(to.boardId, to.dk, fam, zip);
    to.stored?.(row);
    to.has.add(h);
  }
  return missing;
}
