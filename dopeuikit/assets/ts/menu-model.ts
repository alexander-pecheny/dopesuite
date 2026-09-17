import S from "./i18nstrings.js";

// The pure kernel of the site-wide chrome: theme/contrast preference rules and
// the menu item model. menu.ts renders what this module decides.

export type ThemePref = "light" | "dark" | "system";
export type Theme = "light" | "dark";
export type Contrast = "regular" | "high";

// The body fonts a reader may choose between, in the order a picker should offer
// them: the default, then the sans faces, then the serif ones. The id is what
// <html data-font> carries and what core.css switches --font-sans on; "noto" is
// the default and the one core.css states without a data-font at all.
export type FontPref =
  | "noto"
  | "inter-fix-ra"
  | "ibm-plex-sans-fix"
  | "literata-fix"
  | "ibm-plex-serif-fix"
  | "stix-two-text-fix";
export const FONTS: readonly FontPref[] = [
  "noto",
  "inter-fix-ra",
  "ibm-plex-sans-fix",
  "literata-fix",
  "ibm-plex-serif-fix",
  "stix-two-text-fix",
];

// The roman face of each font, which is the one a page is overwhelmingly set in
// and therefore the only one worth preloading. The italics and the symbol
// subset are left to the stylesheet: a page that sets none never asks for them.
// Every file but Noto Sans's is named for its id (scripts/bodyfonts.py).
const ROMAN: Record<FontPref, string> = {
  "noto": "/static/fonts/noto-sans-var.woff2",
  "inter-fix-ra": "/static/fonts/inter-fix-ra.woff2",
  "ibm-plex-sans-fix": "/static/fonts/ibm-plex-sans-fix.woff2",
  "literata-fix": "/static/fonts/literata-fix.woff2",
  "ibm-plex-serif-fix": "/static/fonts/ibm-plex-serif-fix.woff2",
  "stix-two-text-fix": "/static/fonts/stix-two-text-fix.woff2",
};

// fontPreload is the href the chrome preloads for a choice. It is a function of
// the KNOWN preference rather than a fixed line in the head, so that a reader
// who switched fonts fetches one body face and not two.
export function fontPreload(pref: FontPref): string {
  return ROMAN[pref] ?? ROMAN["noto"];
}

// fontFromMe is the signed-in account's font, as /api/auth/me states it, and the
// truth the browser's own copy is reconciled against on every page. null means
// the account has no answer for this: nobody is signed in, the app serves no
// such field (dope has no picker), or the value is one this build cannot set.
export function fontFromMe(ok: boolean, data: unknown): FontPref | null {
  if (!ok) return null;
  const raw = (data as { ui_font?: unknown } | null)?.ui_font;
  return typeof raw === "string" && FONTS.includes(raw as FontPref) ? (raw as FontPref) : null;
}

export interface MenuJump {
  label: string;
  href: string;
  title?: string;
  external?: boolean;
}

export interface MenuExtra {
  label: string;
  // Starts a new cluster: the renderer draws a rule above this row. The model
  // drops one that would lead the menu or follow another, so a page can mark
  // every cluster head without knowing which of them survive its own filtering.
  divider?: boolean;
  // An icon NAME, not a node: this model is DOM-free so jstest can exercise it.
  // menu.ts turns it into the glyph.
  icon?: string;
  title?: string;
  href?: string;
  download?: boolean;
  onClick?: () => void;
}

export interface MenuAccount {
  loggedIn: boolean;
  username?: string | null;
}

export interface MenuConfig {
  profileHref?: string;
  profileLabel?: string;
  loginHref?: string;
  loginLabel?: string;
}

export type MenuItem =
  | { kind: "divider" }
  | { kind: "appearance"; icon: string }
  | { kind: "link"; label: string; href: string; title: string; external: boolean; download: boolean; icon?: string }
  | { kind: "action"; label: string; title: string; onClick: () => void; icon?: string };

export function pickPref<T extends string>(raw: string | null, allowed: readonly T[], fallback: T): T {
  return allowed.includes(raw as T) ? (raw as T) : fallback;
}

export function resolveTheme(pref: ThemePref, prefersDark: boolean): Theme {
  if (pref !== "system") return pref;
  return prefersDark ? "dark" : "light";
}

function link(label: string, href: string, title?: string, external?: boolean, download?: boolean, icon?: string): MenuItem {
  return { kind: "link", label, href, title: title ?? "", external: external ?? false, download: download ?? false, ...(icon ? { icon } : {}) };
}

// Item order: appearance, then the page-supplied jump (edit / audience page),
// then page extras (downloads/actions), then the account entry
// (profile when logged in, login otherwise; labels come from the app config).
export function menuItems(state: {
  jump: MenuJump | null;
  extras: MenuExtra[];
  account: MenuAccount | null;
  config: MenuConfig;
}): MenuItem[] {
  // Every row is a glyph and a word. The two the model owns — appearance and
  // the account entry — used to be the only ones without one, so the column of
  // icons broke at the top and the bottom.
  const items: MenuItem[] = [{ kind: "appearance", icon: "palette" }];
  if (state.jump) {
    items.push(link(state.jump.label, state.jump.href, state.jump.title, state.jump.external));
  }
  for (const extra of state.extras) {
    // A rule that would open the menu or double another separates nothing.
    if (extra.divider && items.length && items[items.length - 1].kind !== "divider") {
      items.push({ kind: "divider" });
    }
    if (extra.onClick) {
      items.push({ kind: "action", label: extra.label, title: extra.title ?? "", onClick: extra.onClick, ...(extra.icon ? { icon: extra.icon } : {}) });
    } else {
      items.push(link(extra.label, extra.href ?? "", extra.title, false, extra.download));
    }
  }
  if (state.account) {
    const cfg = state.config;
    items.push(
      state.account.loggedIn
        ? link(cfg.profileLabel || S.menu.account.profile(), cfg.profileHref || "/profile", "", false, false, "user")
        : link(cfg.loginLabel || S.menu.account.login(), cfg.loginHref || "/login", "", false, false, "log-in"),
    );
  }
  return items;
}

// Server-rendered pages with no JS of their own declare their jump statically
// via body data-jump-* attributes.
export function jumpFromDataset(d: Partial<Record<string, string>>): MenuJump | null {
  if (!d.jumpHref) return null;
  return {
    label: d.jumpLabel || S.menu.jump(),
    href: d.jumpHref,
    title: d.jumpTitle || "",
    external: d.jumpExternal === "1",
  };
}

// /api/auth/me contract: non-OK = anonymous; a body names the account by
// username or telegram handle.
export function accountFromMe(ok: boolean, data: unknown): MenuAccount {
  if (!ok) return { loggedIn: false, username: null };
  const body = (data ?? {}) as { username?: string; telegram?: string };
  return { loggedIn: true, username: body.username || body.telegram || null };
}
