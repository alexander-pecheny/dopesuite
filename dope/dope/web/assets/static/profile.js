const passwordForm = document.getElementById("passwordForm");
const passwordMessage = document.getElementById("passwordMessage");
const hasPassword = passwordForm.dataset.hasPassword === "1";

passwordForm.addEventListener("submit", async (event) => {
  event.preventDefault();
  setText(passwordMessage, "");

  const newPassword = document.getElementById("newPassword").value;
  const confirmPassword = document.getElementById("confirmPassword").value;
  if (newPassword !== confirmPassword) {
    setText(passwordMessage, "Пароли не совпадают.");
    return;
  }

  const body = {new_password: newPassword};
  if (hasPassword) {
    body.current_password = document.getElementById("currentPassword").value;
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
    setText(passwordMessage, error.message);
  }
});

async function fetchVoid(url, init) {
  const response = await fetch(url, init);
  if (!response.ok) {
    const text = (await response.text()).trim();
    throw new Error(text || `HTTP ${response.status}`);
  }
}

function setText(node, text) {
  node.textContent = text;
}
