// Profile password form (new password vs change password modes).

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
    setText(passwordMessage, "Пароли не совпадают.");
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
      setText(passwordMessage, "Пароль изменён.");
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

function setText(node: HTMLElement, text: string): void {
  node.textContent = text;
}

export {};
