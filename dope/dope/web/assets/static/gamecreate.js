// Create-game form: reveal the settings section and the submit button for the
// game type currently selected. Extracted verbatim from the page's former inline
// <script>; keyed on data-game-create-form / data-game-settings / data-game-submit.
(() => {
  const form = document.querySelector("[data-game-create-form]");
  if (!form) return;
  const sync = () => {
    const selected = form.querySelector('input[name="game_type"]:checked')?.value || "";
    form.querySelectorAll("[data-game-settings]").forEach((section) => {
      section.hidden = section.dataset.gameSettings !== selected;
    });
    const submit = form.querySelector("[data-game-submit]");
    if (submit) submit.hidden = selected === "";
  };
  form.querySelectorAll('input[name="game_type"]').forEach((input) => input.addEventListener("change", sync));
  sync();
})();
