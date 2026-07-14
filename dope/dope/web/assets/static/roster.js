// Add-player-override dialog: bind each datalist input to its hidden id field
// (only a value chosen from the suggestions is valid) and block submit until both
// the player and the team resolve to an id. Dialog open/close, the delete confirm,
// and the edit-row dialogs are handled by pageforms.js data-attributes.
(() => {
  const form = document.querySelector("[data-player-override-form]");
  if (!form) return;
  const bindSuggest = (inputSelector, hiddenSelector, listID) => {
    const input = form.querySelector(inputSelector);
    const hidden = form.querySelector(hiddenSelector);
    const options = Array.from(document.getElementById(listID)?.options || []);
    const sync = () => {
      const found = options.find((option) => option.value === input.value);
      hidden.value = found?.dataset.id || "";
      input.setCustomValidity(hidden.value ? "" : "Выберите значение из подсказки");
    };
    input.addEventListener("input", sync);
    input.addEventListener("change", sync);
    return { sync, input, hidden };
  };
  const syncPlayer = bindSuggest("[data-player-override-player]", "[data-player-override-player-id]", "playerOverridePlayers");
  const syncTeam = bindSuggest("[data-player-override-team]", "[data-player-override-team-id]", "playerOverrideTeams");
  form.addEventListener("submit", (event) => {
    syncPlayer.sync();
    syncTeam.sync();
    if (syncPlayer.hidden.value && syncTeam.hidden.value) return;
    event.preventDefault();
    (syncPlayer.hidden.value ? syncTeam.input : syncPlayer.input).reportValidity();
  });
})();
