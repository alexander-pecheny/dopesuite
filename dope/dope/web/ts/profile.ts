// Profile password form (new password vs change password modes) and the
// timezone a person's slot times are written in.
import {autocomplete} from "../../../../dopeuikit/assets/ts/suggest.js";
import {guessZone, zoneChoices} from "./zones.js";

import S from "./i18nstrings.js";

function byId<T extends HTMLElement>(id: string): T {
  const node = document.getElementById(id);
  if (!node) throw new Error(`profile page is missing #${id}`);
  return node as T;
}

const passwordForm = byId<HTMLFormElement>("passwordForm");
const passwordMessage = byId("passwordMessage");
const hasPassword = passwordForm.dataset.hasPassword === "1";

passwordForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  setText(passwordMessage, "");

  const newPassword = byId<HTMLInputElement>("newPassword").value;
  const confirmPassword = byId<HTMLInputElement>("confirmPassword").value;
  if (newPassword !== confirmPassword) {
    setText(passwordMessage, S.widgets.profile.mismatch());
    return;
  }

  const body: { new_password: string; current_password?: string } = { new_password: newPassword };
  if (hasPassword) {
    body.current_password = byId<HTMLInputElement>("currentPassword").value;
  }

  try {
    await fetchVoid("/api/auth/password", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify(body),
    });
    passwordForm.reset();
    if (hasPassword) {
      setText(passwordMessage, S.widgets.profile.changed());
    } else {
      // Reload so the form switches into "change password" mode.
      window.location.reload();
    }
  } catch (error) {
    setText(passwordMessage, error instanceof Error ? error.message : String(error));
  }
});

async function fetchVoid(url: string, init: RequestInit): Promise<void> {
  const response = await fetch(url, init);
  if (!response.ok) {
    const text = (await response.text()).trim();
    throw new Error(text || `HTTP ${response.status}`);
  }
}

const tzForm = byId<HTMLFormElement>("tzForm");
const tzValue = byId<HTMLInputElement>("tzValue");
const tzMessage = byId("tzMessage");

// An account that has not answered yet starts from the device's own zone.
if (!tzValue.value) tzValue.value = guessZone();
autocomplete(tzValue, zoneChoices);

tzForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  setText(tzMessage, "");
  try {
    await fetchVoid("/api/auth/timezone", {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify({timezone: tzValue.value.trim()}),
    });
    setText(tzMessage, S.host.profile.tzSaved());
  } catch (error) {
    setText(tzMessage, error instanceof Error ? error.message : String(error));
  }
});

function setText(node: HTMLElement, text: string): void {
  node.textContent = text;
}

export {};
