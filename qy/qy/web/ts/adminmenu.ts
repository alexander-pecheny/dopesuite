// An admin finds the admin page in the site-wide menu, on every page.
import S from "./i18nstrings";

function addAdminLink(): void {
  fetch("/api/auth/me", { headers: { Accept: "application/json" }, credentials: "same-origin" })
    .then((res) => (res.ok ? (res.json() as Promise<{ admin?: boolean }>) : null))
    .then((me) => {
      if (!me?.admin) return;
      window.dopeMenu?.setExtras([{ icon: "settings", label: S.admin.page.title(), href: "/admin" }]);
    })
    .catch(() => {});
}

addAdminLink();
