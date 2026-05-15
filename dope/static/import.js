const form = document.getElementById("importForm");
const input = document.getElementById("schemeInput");
const message = document.getElementById("importMessage");
const loadSample = document.getElementById("loadSample");
const statusNode = document.getElementById("status");
const tournamentID = new URLSearchParams(window.location.search).get("tournament_id");

loadSample.addEventListener("click", async () => {
  setStatus("saving");
  try {
    const response = await fetch("/static/schemes/studchr-ek-2026.json");
    if (!response.ok) throw new Error(await response.text());
    input.value = await response.text();
    setMessage("JSON загружен");
    setStatus("saved");
  } catch (error) {
    setMessage(error.message);
    setStatus("error");
  }
});

form.addEventListener("submit", async (event) => {
  event.preventDefault();
  if (!tournamentID) {
    setMessage("missing tournament_id");
    setStatus("error");
    return;
  }
  setStatus("saving");
  try {
    const parsed = JSON.parse(input.value);
    const response = await fetch(`/api/import?tournament_id=${encodeURIComponent(tournamentID)}`, {
      method: "POST",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify(parsed),
    });
    if (!response.ok) throw new Error(await response.text());
    await response.json();
    setStatus("saved");
    window.location.href = `/host/tournament/${encodeURIComponent(tournamentID)}`;
  } catch (error) {
    setMessage(error.message);
    setStatus("error");
  }
});

function setMessage(text) {
  message.textContent = text;
}

function setStatus(state) {
  const labels = {
    saved: "Готово",
    saving: "Импорт",
    error: "Ошибка",
  };
  statusNode.dataset.state = state;
  statusNode.setAttribute("aria-label", labels[state] || labels.saved);
  statusNode.title = labels[state] || labels.saved;
}
