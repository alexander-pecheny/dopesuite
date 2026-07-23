// Add-player-override dialog: bind each datalist input to its hidden id field
// (only a value chosen from the suggestions is valid) and block submit until both
// the player and the team resolve to an id. Dialog open/close, the delete confirm,
// and the edit-row dialogs are handled by pageforms.js data-attributes.
(() => {
  const form = document.querySelector<HTMLElement>("[data-player-override-form]");
  if (!form) return;
  const query = <T extends Element>(selector: string): T => {
    const node = form.querySelector<T>(selector);
    if (!node) throw new Error(`players page is missing ${selector}`);
    return node;
  };
  const bindSuggest = (inputSelector: string, hiddenSelector: string, listID: string) => {
    const input = query<HTMLInputElement>(inputSelector);
    const hidden = query<HTMLInputElement>(hiddenSelector);
    const list = document.getElementById(listID) as HTMLDataListElement | null;
    const options = Array.from(list?.options || []);
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

export {};
