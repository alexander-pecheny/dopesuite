// unlock.ts — the board's boot + unlock + snapshot-load flow, lifted out of
// board.js. Owns the whole path from page open to a decrypted BoardState:
// requireLogin → cached-DK fast path or the passphrase overlay → fetch (or
// mirror) the ciphertext snapshot → decrypt every field → hand the state to the
// board via onState. Everything it touches outside that path is injected
// (UnlockDeps) — including the crypto/sync/network singletons, so tests run on
// plain-object fakes with no DOM and no IndexedDB.
import { xySizes } from "./app.js";
import type { AuthMe, Sizes } from "./app.js";
import type { SyncStatus } from "./sync.js";

// Structural mirrors of crypto.ts's DataKey/BoardKeymeta. Deliberately not
// imported: crypto.ts pulls the vendored scrypt, and this module's whole point
// is that its dependency graph stays fake-able (tests import the built file
// under node with no vendor chain, no IndexedDB, no DOM).
export interface DataKey {
  key: CryptoKey;
  raw: Uint8Array<ArrayBuffer>;
}
export interface BoardKeymeta {
  kdf_salt: string;
  kdf_params: string;
  wrapped_key: string;
  verify_token: string;
}

// ---- the decrypted board state load() produces ----
// Exactly what board.js's load() built onto `state`; render()/cardsOf consume
// these fields. (members/memberNames/me are NOT here — boardmembers.js owns
// them and merges its roster onto the board's state object separately.)
export interface BoardList {
  id: number;
  type: string;
  rank: string;
  groupId: number | null;
  title: string;
}
export interface BoardGroup {
  id: number;
  name: string;
}
export interface BoardCard {
  id: number;
  listId: number;
  kind: string;
  rank: string;
  desc: string;
  handoutMeta: string | null;
  alias: string | null;
  createdAt: string | null;
}
export interface BoardLabel {
  id: number;
  kind: string;
  name: string;
  color: string;
}
export interface UnreadFlags {
  content?: boolean;
  comments?: boolean;
}
export interface BoardState {
  role: string;
  name: string;
  lists: BoardList[];
  groups: BoardGroup[];
  cards: BoardCard[];
  labels: BoardLabel[];
  cardLabels: Record<string, number[]>;
  unread: Record<string, UnreadFlags>;
  sizes: Sizes;
  defaultAuthor: string;
  cardTitle: string;
}

// ---- the ciphertext snapshot (GET /api/boards/{id} shape, as load reads it) ----
export interface Snapshot {
  role?: string;
  schema_version?: number;
  name?: string;
  name_enc?: string;
  sizes?: unknown;
  default_author?: string;
  card_title?: string;
  card_labels?: Record<string, number[]>;
  unread?: Record<string, UnreadFlags>;
  lists?: Array<{ id: number; type: string; rank: string; group_id?: number | null; title_enc: string }>;
  groups?: Array<{ id: number; name_enc: string }>;
  cards?: Array<{
    id: number; list_id: number; kind: string; rank: string;
    description_enc: string; handout_meta_enc?: string | null; alias_enc?: string | null; created_at?: string | null;
  }>;
  labels?: Array<{ id: number; kind: string; name_enc: string; color_enc: string }>;
  [key: string]: unknown;
}

// ---- injected seams ----
// The unlock overlay's four nodes, passed as elements (structural types, so
// tests fake them as plain objects).
export interface UnlockUI {
  overlay: { hidden: boolean | string };
  form: { addEventListener(type: "submit", handler: (e: { preventDefault(): void }) => void): void };
  pass: { value: string; focus(): void };
  message: { textContent: string | null };
}
// The slices of xyCrypto / xySync / xyApp the flow uses. Injected rather than
// imported so the tests need neither the vendored scrypt nor IndexedDB; the
// orchestrator passes the real singletons.
export interface UnlockCrypto {
  loadCachedDK(boardId: number): Promise<DataKey | null>;
  unlockBoard(passphrase: string, keymeta: BoardKeymeta): Promise<DataKey>;
  cacheDK(boardId: number, dk: DataKey): Promise<void>;
  decField(dk: DataKey, b64: string): Promise<string>;
}
export interface UnlockSync {
  start(): void;
  onStatus(cb: (st: SyncStatus) => void): unknown;
  onBoardSynced(cb: (boardId: number) => void): unknown;
  isOnline(): boolean;
  pendingCountForBoard(boardId: number): Promise<number>;
  saveSnapshot(boardId: number, snap: Snapshot): Promise<void>;
  // Typed unknown (the mirror stores whatever shape sync.ts models); load()
  // casts back to Snapshot at this one boundary.
  loadSnapshot(boardId: number): Promise<unknown>;
}
export interface UnlockNet {
  requireLogin(): Promise<AuthMe | { offline: true } | null>;
  fetchJSON(url: string): Promise<unknown>;
  jpost(url: string, body: unknown): Promise<unknown>;
}
// The header badge combines a transient per-action state (set) with the
// persistent sync state (onSync); board.js's refreshBadge merges the two.
export interface UnlockStatus {
  set(op: "saving" | "saved" | "error"): void;
  onSync(st: SyncStatus): void;
}
export interface UnlockDeps {
  boardId: number;
  ui: UnlockUI;
  crypto: UnlockCrypto;
  sync: UnlockSync;
  net: UnlockNet;
  status: UnlockStatus;
  applySizes(sizes: Sizes): void;
  onDK(dk: DataKey): void;
  // offline: the state was rendered from the local mirror, not a fresh server
  // snapshot. Covers render + notif badge + members roster + deep link.
  onState(state: BoardState, info: { offline: boolean }): void;
  // No snapshot and no network: board.js hides the kanban and titles the page
  // "Доска недоступна офлайн" (status.set("error") has already fired).
  onUnavailable(): void;
}

export interface Unlock {
  boot(): Promise<void>;
  load(): Promise<void>;
  showUnlock(): void;
}

export function createUnlock(deps: UnlockDeps): Unlock {
  const { boardId, ui, crypto, sync, net, status } = deps;
  let dk: DataKey | null = null;
  let loading = false;
  let visitPinged = false;

  // ---- boot + unlock ----
  async function boot(): Promise<void> {
    if (!(await net.requireLogin())) return;
    sync.start();
    sync.onStatus((st) => status.onSync(st));
    // When a board's queued edits fully reconcile with the server, reload so the
    // temp ids in view are replaced by the authoritative server ids.
    sync.onBoardSynced((b) => { if (b === boardId) void load(); });
    try {
      dk = await crypto.loadCachedDK(boardId);
    } catch (_) {}
    if (!dk) {
      showUnlock();
      return;
    }
    deps.onDK(dk);
    await load();
  }

  function showUnlock(): void {
    ui.overlay.hidden = false;
    ui.pass.focus();
  }

  ui.form.addEventListener("submit", async (e) => {
    e.preventDefault();
    ui.message.textContent = "";
    try {
      const keymeta = (await net.fetchJSON(`/api/boards/${boardId}/keymeta`)) as BoardKeymeta;
      dk = await crypto.unlockBoard(ui.pass.value, keymeta);
      await crypto.cacheDK(boardId, dk);
      ui.overlay.hidden = true;
      deps.onDK(dk);
      await load();
    } catch (err) {
      ui.message.textContent = err instanceof Error ? err.message : String(err);
    }
  });

  // Board names are plaintext server-side metadata now (only the board's data stays
  // encrypted). Backfill a legacy board's name once we've decrypted it on load — best-
  // effort, online-only; the server ignores it if the board is already migrated.
  function migrateBoardName(name: string): void {
    if (!name || !sync.isOnline()) return;
    net.jpost(`/api/boards/${boardId}/migrate-name`, { name }).catch(() => {});
  }

  // ---- load + decrypt snapshot ----
  // Source of truth: when online with an empty outbox, fetch the authoritative
  // snapshot and refresh the mirror. With local edits queued (or offline), render
  // the mirror, which the sync engine keeps current (server snapshot + applied
  // pending ops). After the queue drains, onBoardSynced reloads with real ids.
  async function load(): Promise<void> {
    if (loading) return; // dedupe overlapping refreshes (e.g. visibility + online)
    loading = true;
    status.set("saving");
    try {
      let snap: Snapshot | null | undefined;
      let fromMirror = false;
      const pending = await sync.pendingCountForBoard(boardId);
      if (sync.isOnline() && pending === 0) {
        try {
          snap = (await net.fetchJSON(`/api/boards/${boardId}`)) as Snapshot;
          await sync.saveSnapshot(boardId, snap);
        } catch (_) {
          snap = (await sync.loadSnapshot(boardId)) as Snapshot | null | undefined;
          fromMirror = true;
        }
      } else {
        snap = (await sync.loadSnapshot(boardId)) as Snapshot | null | undefined;
        fromMirror = true;
        if (!snap && sync.isOnline()) {
          snap = (await net.fetchJSON(`/api/boards/${boardId}`)) as Snapshot;
          await sync.saveSnapshot(boardId, snap);
          fromMirror = false;
        }
      }
      if (!snap) {
        status.set("error");
        deps.onUnavailable();
        return;
      }
      const key = dk as DataKey;
      // The caller's per-user display prefs (same on every board); absent (never
      // set) → defaults. Apply now so the board renders at the user's saved sizes.
      const sizes = xySizes.sanitize(snap.sizes);
      deps.applySizes(sizes);
      // Migrated boards (schema_version 2) carry a plaintext name; legacy boards still
      // need the DK to decrypt name_enc — and, since we now hold it, get backfilled.
      let name: string;
      if ((snap.schema_version ?? 0) >= 2) {
        name = snap.name ?? "";
      } else {
        name = await crypto.decField(key, snap.name_enc ?? "");
        migrateBoardName(name);
      }
      const state: BoardState = {
        role: snap.role || "editor",
        name,
        cardLabels: snap.card_labels || {},
        unread: snap.unread || {},
        sizes,
        defaultAuthor: snap.default_author || "",
        cardTitle: snap.card_title || "question",
        lists: await Promise.all((snap.lists || []).map(async (l) => ({
          id: l.id, type: l.type, rank: l.rank, groupId: l.group_id != null ? l.group_id : null,
          title: await crypto.decField(key, l.title_enc),
        }))),
        groups: await Promise.all((snap.groups || []).map(async (g) => ({
          id: g.id, name: await crypto.decField(key, g.name_enc),
        }))),
        cards: await Promise.all((snap.cards || []).map(async (c) => ({
          id: c.id, listId: c.list_id, kind: c.kind, rank: c.rank,
          desc: await crypto.decField(key, c.description_enc),
          handoutMeta: c.handout_meta_enc ? await crypto.decField(key, c.handout_meta_enc) : null,
          alias: c.alias_enc ? await crypto.decField(key, c.alias_enc) : null,
          createdAt: c.created_at || null,
        }))),
        labels: await Promise.all((snap.labels || []).map(async (l) => ({
          id: l.id, kind: l.kind,
          name: await crypto.decField(key, l.name_enc),
          color: await crypto.decField(key, l.color_enc),
        }))),
      };
      deps.onState(state, { offline: fromMirror });
      status.set("saved");
      pingVisit(); // stamp last-visit so the board list can order by it (online-only, once)
    } catch (e) {
      status.set("error");
      console.error(e);
    } finally {
      loading = false;
    }
  }

  // pingVisit stamps this board as most-recently-visited (for the board-list
  // ordering). Online-only best-effort, fired once per page session.
  function pingVisit(): void {
    if (visitPinged || !sync.isOnline()) return;
    visitPinged = true;
    net.jpost(`/api/boards/${boardId}/visit`, {}).catch(() => {});
  }

  return { boot, load, showUnlock };
}
