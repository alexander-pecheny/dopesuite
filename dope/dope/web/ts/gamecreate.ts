// Create-game form: reveal the settings section and the submit button for the
// game type currently selected. Extracted verbatim from the page's former inline
// <script>; keyed on data-game-create-form / data-game-settings / data-game-submit.
(() => {
  const form = document.querySelector<HTMLElement>("[data-game-create-form]");
  if (!form) return;
  const picker = form.querySelector<HTMLElement>("[data-game-entrants]");
  const seatsChosen = (picker?.dataset.gameEntrants || "").split(" ").filter(Boolean);
  const sync = () => {
    const selected = form.querySelector<HTMLInputElement>('input[name="game_type"]:checked')?.value || "";
    form.querySelectorAll<HTMLElement>("[data-game-settings]").forEach((section) => {
      section.hidden = section.dataset.gameSettings !== selected;
    });
    if (picker) {
      // The flat formats seat the whole fest roster and refuse a chosen list,
      // so the picker is hidden for them — and its boxes disabled, because a
      // hidden checkbox still posts.
      const offered = seatsChosen.includes(selected);
      picker.hidden = !offered;
      picker.querySelectorAll<HTMLInputElement>('input[name="entrant_id"]').forEach((box) => {
        box.disabled = !offered;
      });
    }
    const submit = form.querySelector<HTMLElement>("[data-game-submit]");
    if (submit) submit.hidden = selected === "";
  };
  form.querySelectorAll<HTMLInputElement>('input[name="game_type"]').forEach((input) => input.addEventListener("change", sync));
  sync();
})();

export {};
