// instance.ts — what this xy offers. A server offers everything; a local one,
// the desktop app's, names what it leaves out in /api/auth/me's `instance`
// (internal/server/local.go). Pages learn it from requireLogin, before they
// draw anything that depends on it.

export type Feature = "login" | "telegram_login" | "tokens" | "members" | "invites" | "passphrase" | "offline" | "activity" | "admin";

export interface InstanceCaps {
  mode: string;
  off: string[];
  pick?: boolean;
}

let off = new Set<string>();
let local = false;
let pick = false;

function setInstance(caps: InstanceCaps | null | undefined): void {
  local = caps?.mode === "local";
  off = new Set(caps?.off ?? []);
  pick = caps?.pick === true;
}

function offers(feature: Feature): boolean { return !off.has(feature); }

function isLocal(): boolean { return local; }

// picksFiles: the desktop app opens its own file dialog for the page, which a
// webview panel cannot (internal/server/importpick.go).
function picksFiles(): boolean { return pick; }

export const xyInstance = { setInstance, offers, isLocal, picksFiles };
