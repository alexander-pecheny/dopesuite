// The game-page shell (ADR-0003): owns everything format-independent — the
// init payload, SSE state sync, and renderer resolution. The page modules
// (od.ts, si.ts, host.ts, viewer.ts) still boot themselves on import; new
// Protocol pages register a ProtocolRenderer and call runGamePage, and the
// page modules migrate onto the same seam renderer by renderer.

import type { GameInitPayload, ProtocolRenderer, RendererRegistry, StateOp, StateSync } from './contracts';

declare global {
  interface Window {
    __GAME_INIT__?: unknown;
    DopeShell: { registry: RendererRegistry; runGamePage(code: string, mountId: string): void };
  }
}

const renderers = new Map<string, ProtocolRenderer>();

const registry: RendererRegistry = {
  register(renderer: ProtocolRenderer): void {
    if (renderers.has(renderer.code)) {
      throw new Error(`renderer ${renderer.code} already registered`);
    }
    renderers.set(renderer.code, renderer);
  },
  get(code: string): ProtocolRenderer | undefined {
    return renderers.get(code);
  },
};

function gameInit(): GameInitPayload {
  const raw = window.__GAME_INIT__;
  if (!raw || typeof raw !== 'object') {
    throw new Error('missing __GAME_INIT__');
  }
  return raw as GameInitPayload;
}

/** Wire a StateSync over the page's PATCH endpoint and SSE stream. */
function createSync(init: GameInitPayload): StateSync {
  let current: unknown = init.state;
  let render: () => void = () => {};
  return {
    get state(): unknown {
      return current;
    },
    onRender(callback: () => void): void {
      render = callback;
    },
    async submit(ops: StateOp[]): Promise<void> {
      const body = JSON.stringify({
        ops: ops.map((op) => ({ op: op.k === 'set' ? 'set' : op.k, path: pointerToSegments(op.p), value: op.v })),
      });
      const resp = await fetch(`/api/fest/${init.festID}/games/${init.gameID}/state`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body,
      });
      if (!resp.ok) {
        throw new Error(`state patch failed: ${resp.status}`);
      }
      current = await resp.json();
      render();
    },
  };
}

/** "/entries/3/0" → ["entries", 3, 0], the wire shape of a patch path. */
export function pointerToSegments(pointer: string): Array<string | number> {
  return pointer
    .replace(/^\//, '')
    .split('/')
    .map((segment) => {
      const unescaped = segment.replaceAll('~1', '/').replaceAll('~0', '~');
      return /^(0|[1-9][0-9]*)$/.test(unescaped) ? Number(unescaped) : unescaped;
    });
}

function runGamePage(code: string, mountId: string): void {
  const renderer = registry.get(code);
  if (!renderer) {
    throw new Error(`no renderer registered for protocol ${code}`);
  }
  const root = document.getElementById(mountId);
  if (!root) {
    throw new Error(`missing mount #${mountId}`);
  }
  const init = gameInit();
  renderer.mount(root, init, createSync(init));
}

window.DopeShell = { registry, runGamePage };
