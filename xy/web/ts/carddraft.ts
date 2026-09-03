// carddraft.ts — the card editor's draft/dirty kernel, lifted out of board.js.
//
// The card detail view carries a working draft of a card's 4s description and
// its handout-generation settings (meta) against the last-persisted baseline.
// Deciding whether that draft is "dirty" — which drives the Save button — is the
// editor's trickiest rule: a brand-new card has no baseline (dirty once it has
// any content), the alias is its own column with a baseline of its own, and
// blank strings normalize to null the way the server's optBlob does. Those pure
// rules live here so jstest can exercise them without the DOM; board.js keeps
// the DOM and calls this.

// normalizeMeta / normalizeAlias mirror the server's optBlob convention: a blank
// string means "no value". Meta is kept verbatim when non-blank; the alias is
// also trimmed (it is a short label, meta is free markup).
export function normalizeMeta(v: string | null | undefined): string | null { return v && v.trim() ? v : null; }
export function normalizeAlias(v: string | null | undefined): string | null { return v && v.trim() ? v.trim() : null; }

// The state contentDirty judges: the working draft next to its baseline.
export interface DraftState {
  isNew: boolean;
  desc: string;
  savedDesc: string;
  meta: string | null;
  savedMeta: string | null;
}

// contentDirty decides whether Save is enabled. A new card (isNew) is
// dirty once it has any 4s content; an existing card is dirty when its
// description or handout settings differ from the persisted baseline. The alias
// is no part of either — it autosaves, so counting it would offer to save
// something already stored.
export function contentDirty(s: DraftState): boolean {
  return s.isNew
    ? s.desc.trim() !== ""
    : s.desc !== s.savedDesc || (s.meta || null) !== (s.savedMeta || null);
}

// aliasDirty compares an already-normalized (string|null) alias against the
// persisted one.
export function aliasDirty(current: string | null, savedAlias: string | null): boolean {
  return (current || null) !== (savedAlias || null);
}

// The shape create() returns — one card's draft + baseline, driven by board.js.
// The alias keeps only a baseline here: its working value lives in the input it
// autosaves from, so there is nothing to hold between views.
export interface CardDraft {
  desc: string;
  meta: string | null;
  open(desc: string, meta: string | null, alias: string | null): void;
  contentDirty(isNew: boolean): boolean;
  aliasDirty(current: string | null): boolean;
  commitContent(desc: string, meta: string | null): void;
  commitAlias(alias: string | null): void;
  normalizedMeta(): string | null;
}

// create() holds one card's draft + baseline. board.js drives it: open() when a
// card is opened, the desc/meta setters as the views change, commit* after a
// successful save.
export function create(): CardDraft {
  const st: { desc: string; meta: string | null; savedDesc: string; savedMeta: string | null; savedAlias: string | null } =
    { desc: "", meta: null, savedDesc: "", savedMeta: null, savedAlias: null };
  return {
    get desc() { return st.desc; },
    set desc(v) { st.desc = v; },
    get meta() { return st.meta; },
    set meta(v) { st.meta = v; },
    open(desc, meta, alias) {
      st.desc = desc; st.meta = meta;
      st.savedDesc = desc; st.savedMeta = meta; st.savedAlias = alias;
    },
    contentDirty(isNew) {
      return contentDirty({ isNew, desc: st.desc, savedDesc: st.savedDesc, meta: st.meta, savedMeta: st.savedMeta });
    },
    aliasDirty(current) { return aliasDirty(current, st.savedAlias); },
    commitContent(desc, meta) { st.savedDesc = desc; st.savedMeta = meta; },
    commitAlias(alias) { st.savedAlias = alias; },
    normalizedMeta() { return normalizeMeta(st.meta); },
  };
}

export const xyCardDraft = { create, contentDirty, aliasDirty, normalizeMeta, normalizeAlias };
