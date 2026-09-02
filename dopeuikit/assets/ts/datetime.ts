// The datetime field (kit's `datetimefield`): a text input that takes a typed
// or pasted «2026-09-04 19:00», with a real datetime-local kept out of sight
// beside it for the calendar. A bare datetime-local is segmented — pasting into
// one does nothing — so the text input is what the form posts and the picker
// only ever writes into it.

const TEXT = "[data-datetime-text]";

// asPicker is what a datetime-local takes ("2026-09-04T19:00"); asText is what
// the field shows and posts.
export function asPicker(text: string): string {
  const match = text.trim().match(/^(\d{4})-(\d{2})-(\d{2})[T ](\d{2}):(\d{2})/);
  return match ? `${match[1]}-${match[2]}-${match[3]}T${match[4]}:${match[5]}` : "";
}

export function asText(picker: string): string {
  return picker.replace("T", " ").slice(0, 16);
}

export function mountDatetimeField(field: HTMLElement): void {
  const text = field.querySelector<HTMLInputElement>(TEXT);
  const picker = field.querySelector<HTMLInputElement>("[data-datetime-picker]");
  const button = field.querySelector<HTMLElement>("[data-datetime-open]");
  if (!text || !picker) return;

  // Two things must not re-open the picker: the focus that follows the click of
  // the same tap, and the focus the field takes back while the picker's own
  // change/blur settles — either would flicker it open and shut.
  const QUIET_MS = 300;
  let quietUntil = 0;
  const quiet = (): void => {
    quietUntil = Date.now() + QUIET_MS;
  };

  const open = (): void => {
    if (Date.now() < quietUntil) return;
    quiet();
    picker.value = asPicker(text.value);
    try {
      // showPicker needs a user gesture and throws without one. A browser that
      // lacks it gets no picker from here: focusing the hidden input would take
      // the caret out of the text field and make it untypeable.
      const withPicker = picker as HTMLInputElement & {showPicker?: () => void};
      withPicker.showPicker?.();
    } catch {
      // Not a gesture, or the browser refused: the field is still typeable.
    }
  };

  button?.addEventListener("click", open);
  // A tap is a pointerup and a click; focus alone is not a gesture everywhere,
  // so all three try, and the quiet window keeps them from stacking.
  text.addEventListener("click", open);
  text.addEventListener("focus", open);
  text.addEventListener("input", () => {
    picker.value = asPicker(text.value);
  });
  picker.addEventListener("change", () => {
    if (!picker.value) return;
    text.value = asText(picker.value);
    text.dispatchEvent(new Event("input", {bubbles: true}));
    quiet();
    // The minutes are often what a person wants to correct by hand.
    text.focus();
  });
  picker.addEventListener("blur", quiet);
}

export function mountDatetimeFields(doc: Document): void {
  doc.querySelectorAll<HTMLElement>("[data-datetime-field]").forEach(mountDatetimeField);
}
