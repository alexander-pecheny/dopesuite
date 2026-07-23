// Create-game form: reveal the settings section and the submit button for the
// game type currently selected. Extracted verbatim from the page's former inline
// <script>; keyed on data-game-create-form / data-game-settings / data-game-submit.
(() => {
  const form = document.querySelector<HTMLElement>("[data-game-create-form]");
  if (!form) return;
  const sync = () => {
    const selected = form.querySelector<HTMLInputElement>('input[name="game_type"]:checked')?.value || "";
    form.querySelectorAll<HTMLElement>("[data-game-settings]").forEach((section) => {
      section.hidden = section.dataset.gameSettings !== selected;
    });
    const submit = form.querySelector<HTMLElement>("[data-game-submit]");
    if (submit) submit.hidden = selected === "";
  };
  form.querySelectorAll<HTMLInputElement>('input[name="game_type"]').forEach((input) => input.addEventListener("change", sync));
  sync();
})();

export {};
