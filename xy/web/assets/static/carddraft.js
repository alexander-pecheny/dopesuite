// carddraft.js — the card editor's draft/dirty kernel, lifted out of board.js.
//
// The card detail view carries a working draft of a card's 4s description, its
// handout-generation settings (meta) and its alias, against the last-persisted
// baseline. Deciding whether that draft is "dirty" — which drives the Save
// buttons — is the editor's trickiest rule: a brand-new card has no baseline
// (dirty once it has any content or alias), the alias is its own column with its
// own baseline, and blank strings normalize to null the way the server's optBlob
// does. Those pure rules live here so jstest can exercise them without the DOM;
// board.js keeps the DOM and calls this.

// normalizeMeta / normalizeAlias mirror the server's optBlob convention: a blank
// string means "no value". Meta is kept verbatim when non-blank; the alias is
// also trimmed (it is a short label, meta is free markup).
export function normalizeMeta(v) { return v && v.trim() ? v : null; }
export function normalizeAlias(v) { return v && v.trim() ? v.trim() : null; }

// contentDirty decides whether «Сохранить» is enabled. A new card (isNew) is
// dirty once it has any 4s content or an alias; an existing card is dirty when
// its description or handout settings differ from the persisted baseline. The
// alias is deliberately excluded from the existing-card check — it saves on its
// own button (see aliasDirty).
export function contentDirty(s) {
  return s.isNew
    ? s.desc.trim() !== "" || (s.alias || null) !== null
    : s.desc !== s.savedDesc || (s.meta || null) !== (s.savedMeta || null);
}

// aliasDirty compares an already-normalized (string|null) alias against the
// persisted one.
export function aliasDirty(current, savedAlias) {
  return (current || null) !== (savedAlias || null);
}

// create() holds one card's draft + baseline. board.js drives it: open() on a
// persisted card, blank() for a new one, the desc/meta/alias setters as the
// views change, commit* after a successful save.
export function create() {
  const st = { desc: "", meta: null, alias: null, savedDesc: "", savedMeta: null, savedAlias: null };
  return {
    get desc() { return st.desc; },
    set desc(v) { st.desc = v; },
    get meta() { return st.meta; },
    set meta(v) { st.meta = v; },
    get alias() { return st.alias; },
    set alias(v) { st.alias = v; },
    get savedAlias() { return st.savedAlias; },
    open(desc, meta, alias) {
      st.desc = desc; st.meta = meta; st.alias = alias;
      st.savedDesc = desc; st.savedMeta = meta; st.savedAlias = alias;
    },
    blank() { st.desc = ""; st.meta = null; st.alias = null; },
    contentDirty(isNew) {
      return contentDirty({ isNew, desc: st.desc, savedDesc: st.savedDesc, meta: st.meta, savedMeta: st.savedMeta, alias: st.alias });
    },
    aliasDirty(current) { return aliasDirty(current, st.savedAlias); },
    commitContent(desc, meta) { st.savedDesc = desc; st.savedMeta = meta; },
    commitAlias(alias) { st.savedAlias = alias; st.alias = alias; },
    normalizedMeta() { return normalizeMeta(st.meta); },
    normalizedAlias() { return normalizeAlias(st.alias); },
  };
}

export const xyCardDraft = { create, contentDirty, aliasDirty, normalizeMeta, normalizeAlias };

if (typeof window !== "undefined") window.xyCardDraft = xyCardDraft;
