// Create-game form: show the settings section of the game type the select
// holds, and the fields that apply to it. Keyed on data-game-create-form /
// data-game-settings / data-game-types / data-game-entrants.
(() => {
  const form = document.querySelector<HTMLElement>("[data-game-create-form]");
  const select = form?.querySelector<HTMLSelectElement>('select[name="game_type"]');
  if (!form || !select) return;
  // A field shown for some formats only is hidden for the rest, and its
  // inputs disabled, because a hidden input still posts.
  const offer = (box: HTMLElement, offered: boolean) => {
    box.hidden = !offered;
    box.querySelectorAll<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>("input, textarea, select").forEach((input) => {
      input.disabled = !offered;
    });
  };
  const title = form.querySelector<HTMLInputElement>('input[name="title"]');
  const sync = () => {
    const selected = select.value;
    // An empty title is the format's: the chosen option's text.
    if (title) title.placeholder = select.selectedOptions[0]?.textContent || "";
    form.querySelectorAll<HTMLElement>("[data-game-settings]").forEach((section) => {
      section.hidden = section.dataset.gameSettings !== selected;
    });
    form.querySelectorAll<HTMLElement>("[data-game-types]").forEach((box) => {
      offer(box, (box.dataset.gameTypes || "").split(" ").includes(selected));
    });
    // The flat formats seat the whole fest roster and refuse a chosen list.
    const picker = form.querySelector<HTMLElement>("[data-game-entrants]");
    if (picker) offer(picker, (picker.dataset.gameEntrants || "").split(" ").includes(selected));
  };
  select.addEventListener("change", sync);
  sync();
})();

export {};
