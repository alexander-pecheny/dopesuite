// The pure kernel of the site-wide chrome: theme/contrast preference rules and
// the menu item model. menu.ts renders what this module decides.

export type ThemePref = "light" | "dark" | "system";
export type Theme = "light" | "dark";
export type Contrast = "regular" | "high";

export interface MenuJump {
  label: string;
  href: string;
  title?: string;
  external?: boolean;
}

export interface MenuExtra {
  label: string;
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
  | { kind: "appearance" }
  | { kind: "link"; label: string; href: string; title: string; external: boolean; download: boolean }
  | { kind: "action"; label: string; title: string; onClick: () => void };

export function pickPref<T extends string>(raw: string | null, allowed: readonly T[], fallback: T): T {
  return allowed.includes(raw as T) ? (raw as T) : fallback;
}

export function resolveTheme(pref: ThemePref, prefersDark: boolean): Theme {
  if (pref !== "system") return pref;
  return prefersDark ? "dark" : "light";
}

function link(label: string, href: string, title?: string, external?: boolean, download?: boolean): MenuItem {
  return { kind: "link", label, href, title: title ?? "", external: external ?? false, download: download ?? false };
}

// Item order: Оформление, then the page-supplied jump (Редактировать / Страница
// зрителя), then page extras (downloads/actions), then the account entry
// (profile when logged in, login otherwise; labels come from the app config).
export function menuItems(state: {
  jump: MenuJump | null;
  extras: MenuExtra[];
  account: MenuAccount | null;
  config: MenuConfig;
}): MenuItem[] {
  const items: MenuItem[] = [{ kind: "appearance" }];
  if (state.jump) {
    items.push(link(state.jump.label, state.jump.href, state.jump.title, state.jump.external));
  }
  for (const extra of state.extras) {
    if (extra.onClick) {
      items.push({ kind: "action", label: extra.label, title: extra.title ?? "", onClick: extra.onClick });
    } else {
      items.push(link(extra.label, extra.href ?? "", extra.title, false, extra.download));
    }
  }
  if (state.account) {
    const cfg = state.config;
    items.push(
      state.account.loggedIn
        ? link(cfg.profileLabel || "Профиль", cfg.profileHref || "/profile")
        : link(cfg.loginLabel || "Вход", cfg.loginHref || "/login"),
    );
  }
  return items;
}

// Server-rendered pages with no JS of their own declare their jump statically
// via body data-jump-* attributes.
export function jumpFromDataset(d: Partial<Record<string, string>>): MenuJump | null {
  if (!d.jumpHref) return null;
  return {
    label: d.jumpLabel || "Перейти",
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
