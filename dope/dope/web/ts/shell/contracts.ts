// The typed contracts of the game-page stack (ADR-0003). These interfaces are
// the load-bearing seams of the unified frontend: the init payloads the server
// splices into the page, the state-sync surface the shell owns, and the
// renderer interface a Protocol implements to get a page — nothing else.

/** Init payload of a flat game page (od/si), spliced as __GAME_INIT__. */
export interface GameInitPayload {
  festID: number;
  gameID: number;
  scheme: unknown;
  state: unknown;
  fest: FestSummary | null;
  screenSettings: unknown;
  seq: number;
  epoch: string;
  canEdit: boolean;
  teamsUnnumbered: boolean;
  static: boolean;
}

export interface FestSummary {
  id: number;
  slug: string;
  title: string;
}

/** One pointer op of a state delta (mirrors store.BlobOp / OpMatchPatch). */
export interface StateOp {
  k: 'set' | 'remove' | 'ensure' | 'replace';
  p: string;
  v?: unknown;
}

/** The SSE state-sync surface the shell owns. Renderers never touch SSE. */
export interface StateSync {
  /** Current state document (the match blob / flat game state). */
  readonly state: unknown;
  /** Register the re-render callback; called coalesced after deltas apply. */
  onRender(callback: () => void): void;
  /** Send a batch of set-ops; resolves when the edit window committed. */
  submit(ops: StateOp[]): Promise<void>;
}

/**
 * A Protocol's page renderer: everything format-specific about one match view.
 * The shell owns the rest — chrome (topbar, tabs, breadcrumbs), SSE sync,
 * static-mode, theming — so a renderer cannot ship a page without the standard
 * frame (the structural fix for convention drift).
 */
export interface ProtocolRenderer {
  /** Protocol code this renderer serves (matches games.game_type). */
  readonly code: string;
  /** Build the match view into the shell-provided mount. */
  mount(root: HTMLElement, init: GameInitPayload, sync: StateSync): void;
}

/** The registry the shell resolves renderers from. */
export interface RendererRegistry {
  register(renderer: ProtocolRenderer): void;
  get(code: string): ProtocolRenderer | undefined;
}
