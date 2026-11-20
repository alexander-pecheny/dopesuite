// boardfonts.ts — a board's Font Families (ADR-0025) as the image editor and
// the exports see them: the live ones, a Family's zip by hash, and uploading a
// new one. The server keeps each as encrypted meta plus an encrypted zip; the
// hash that names a Family lives only inside the meta.
//
// Only the newest Family of a name is live, and only it is offered. An older
// one stays readable by hash for the images that still name it, until they
// are moved onto the new one.
import { xyCrypto } from "./crypto.js";
import type { DataKey } from "./crypto.js";
import { xySync } from "./sync.js";
import S from "./i18nstrings.js";
import { buildFamily } from "./xyimagefonts.js";
import type { FontFamily, FontSource } from "./xyimagefonts.js";
import type { BoardFont } from "./unlock.js";
import { postFamily } from "./fonttransfer.js";

export interface BoardFontsDeps {
  boardId: number;
  state(): { fonts: BoardFont[] };
  dk(): DataKey;
  // A new upload replaced an older Family of the same name; the images set
  // in the older one are to be moved onto it.
  onReplaced?(old: BoardFont, now: BoardFont): Promise<void>;
}

export interface BoardFonts extends FontSource {
  family(hash: string): FontFamily | undefined;
  // byHash is the Family with this hash, live or replaced.
  byHash(hash: string): BoardFont | undefined;
  // forget drops a Family that was deleted.
  forget(row: BoardFont): void;
}


export function boardFonts(deps: BoardFontsDeps): BoardFonts {
  const zips = new Map<string, Promise<Uint8Array<ArrayBuffer>>>();

  function families(): FontFamily[] {
    const newest = new Map<string, BoardFont>();
    for (const f of [...deps.state().fonts].sort((a, b) => a.id - b.id)) newest.set(f.name, f);
    return [...newest.values()].sort((a, b) => a.name.localeCompare(b.name));
  }

  function byHash(hash: string): BoardFont | undefined {
    return deps.state().fonts.find((f) => f.hash === hash);
  }

  function zip(hash: string): Promise<Uint8Array<ArrayBuffer>> {
    let p = zips.get(hash);
    if (!p) {
      p = (async () => {
        const row = byHash(hash);
        if (!row) throw new Error(S.board.fonts.missing());
        const res = await fetch(`/api/fonts/${row.id}`, { credentials: "same-origin" });
        if (!res.ok) throw new Error((await res.text()).trim() || `HTTP ${res.status}`);
        return xyCrypto.decBytes(deps.dk(), new Uint8Array(await res.arrayBuffer()));
      })();
      p.catch(() => zips.delete(hash));
      zips.set(hash, p);
    }
    return p;
  }

  async function store(family: FontFamily, bytes: Uint8Array<ArrayBuffer>): Promise<BoardFont> {
    const row = await postFamily(deps.boardId, deps.dk(), family, bytes);
    deps.state().fonts.push(row);
    zips.set(family.hash, Promise.resolve(bytes));
    return row;
  }

  // upload adds a Family. The same Faces uploaded again are the Family the
  // board already has. A new set under a name already in use replaces that
  // Family, once the person agrees that its images may lay out differently.
  async function upload(files: File[]): Promise<FontFamily> {
    if (!xySync.isOnline()) throw new Error(S.board.fonts.offline());
    const built = await buildFamily(files);
    const same = byHash(built.family.hash);
    if (same) return same;
    const prev = families().find((f) => f.name === built.family.name);
    if (prev && !confirm(S.board.fonts.replace(prev.name))) throw new Error(S.board.fonts.notReplaced(prev.name));
    const row = await store(built.family, built.zip);
    const old = prev ? byHash(prev.hash) : undefined;
    if (old && deps.onReplaced) await deps.onReplaced(old, row);
    return row;
  }

  function forget(row: BoardFont): void {
    const list = deps.state().fonts;
    const at = list.indexOf(row);
    if (at >= 0) list.splice(at, 1);
    zips.delete(row.hash);
  }

  return { families, zip, upload, byHash, forget, family: byHash };
}
