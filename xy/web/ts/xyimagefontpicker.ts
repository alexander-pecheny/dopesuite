// xyimagefontpicker.ts — the image editor's font fields (ADR-0025): a text
// box's Family (the bundled Noto Sans, or one uploaded to the board) and its
// Face, an upload button beside them, and the loading of the Faces boxes use.
import { xyApp } from "./app.js";
import S from "./i18nstrings.js";
import { FontRefused, faceRef, loadFace, memoryFonts } from "./xyimagefonts.js";
import type { FaceRef, FamilyFace, FontFamily, FontSource } from "./xyimagefonts.js";
import type { TextStyle } from "./xyimagefreeze.js";

const { el, errMsg } = xyApp;

// The bundled family, which has no Face of its own: bold and italic pick
// among its four files.
export const NOTO_FAMILY = "Noto Sans";
const WEIGHT = { regular: 400, bold: 700 };
// How much worse a Face of the wrong slant is than one of the wrong weight,
// in weight units, when a box moves to another Family.
const FACE_ITALIC_COST = 1000;
const NOTO_STYLES: ReadonlyArray<[string, boolean, boolean]> = [["regular", false, false], ["bold", true, false], ["italic", false, true], ["bolditalic", true, true]];

export interface PickerHost {
  fonts?: FontSource; // in memory for the page's life when the host has none
  // The Faces boxes name that could not be loaded; such a box shows in Noto
  // Sans and cannot be saved.
  missing: Set<string>;
  render(): void; // lay the canvas out again
  renderProps(): void; // rebuild the properties panel
  say(text: string): void; // the status line
  // A Family was uploaded: boxes set in an older one of its name move onto it.
  uploaded(fam: FontFamily): Promise<void>;
  button(title: string, onClick: () => void): HTMLElement;
}

export interface FontPicker {
  fonts: FontSource;
  fields(s: TextStyle): HTMLElement[];
  useFace(ref: FaceRef): Promise<void>;
  input: HTMLInputElement; // the hidden file field, which the host mounts
}

// closestFace is the Face of a Family nearest the box's current look, so
// switching from bold Noto Sans to an upload stays bold where it can.
export function closestFace(fam: FontFamily, s: TextStyle): FamilyFace {
  const italic = s.font ? /italic|oblique/i.test(s.font.style) : s.italic;
  const weight = s.bold && !s.font ? WEIGHT.bold : WEIGHT.regular;
  const score = (f: FamilyFace): number => (f.italic === italic ? 0 : FACE_ITALIC_COST) + Math.abs(f.weight - weight);
  return fam.faces.reduce((a, b) => (score(b) < score(a) ? b : a));
}

function notoStyleLabel(key: string): string {
  return ({ regular: S.xyimage.props.regular(), bold: S.xyimage.props.bold(), italic: S.xyimage.props.italic(), bolditalic: S.xyimage.props.boldItalic() } as Record<string, string>)[key];
}

function fontError(err: unknown): string {
  if (!(err instanceof FontRefused)) return errMsg(err);
  const why: Record<FontRefused["reason"], (file: string) => string> = {
    woff: S.xyimage.fonts.woff, variable: S.xyimage.fonts.variable, collection: S.xyimage.fonts.collection,
    notfont: S.xyimage.fonts.notfont, empty: S.xyimage.fonts.empty, toobig: S.xyimage.fonts.toobig,
  };
  return why[err.reason](err.file);
}

function selectOf(opts: [string, string][], value: string, cls: string): HTMLSelectElement {
  const sel = el("select", { class: cls }, opts.map(([v, label]) => el("option", { value: v }, label))) as HTMLSelectElement;
  sel.value = value;
  return sel;
}

// styleOptions is what the Face picker offers: Noto Sans's four, the Faces of
// the box's Family, or, for a Family the board no longer has, its one Face.
function styleOptions(s: TextStyle, fam: FontFamily | undefined): [string, string][] {
  if (!s.font) return NOTO_STYLES.map(([k]): [string, string] => [k, notoStyleLabel(k)]);
  if (fam) return fam.faces.map((f): [string, string] => [f.path, f.style]);
  return [[s.font.face, s.font.style]];
}

// matchFace is the Face of a new Family that stands in for a box's Face of a
// replaced one: the same file where it has one, else the same style, else
// the nearest.
export function matchFace(fam: FontFamily, s: TextStyle): FamilyFace {
  const ref = s.font;
  return fam.faces.find((f) => f.path === ref?.face) ?? fam.faces.find((f) => f.style === ref?.style) ?? closestFace(fam, s);
}

export function fontPicker(host: PickerHost): FontPicker {
  const { missing } = host;
  const fonts = host.fonts ?? memoryFonts();

  async function useFace(ref: FaceRef): Promise<void> {
    try {
      await loadFace(fonts, ref);
      missing.delete(ref.family);
    } catch {
      missing.add(ref.family);
    }
    host.render();
  }

  function setFace(s: TextStyle, fam: FontFamily, face: FamilyFace): void {
    s.font = faceRef(fam, face);
    void useFace(s.font);
  }

  function familySelect(s: TextStyle): HTMLSelectElement {
    const fams = fonts.families();
    const opts: [string, string][] = [["", NOTO_FAMILY], ...fams.map((f): [string, string] => [f.hash, f.name])];
    const font = s.font;
    if (font && !fams.some((f) => f.hash === font.zip)) opts.push([font.zip, S.xyimage.props.fontMissing(font.name)]);
    const sel = selectOf(opts, font?.zip ?? "", "input u-grow");
    sel.addEventListener("change", () => {
      const fam = fams.find((f) => f.hash === sel.value);
      if (fam) setFace(s, fam, closestFace(fam, s));
      else s.font = null;
      host.render();
      host.renderProps();
    });
    return sel;
  }

  function styleSelect(s: TextStyle): HTMLSelectElement {
    const fam = fonts.families().find((f) => f.hash === s.font?.zip);
    const noto = NOTO_STYLES.find(([, b, i]) => b === s.bold && i === s.italic) ?? NOTO_STYLES[0];
    const sel = selectOf(styleOptions(s, fam), s.font ? s.font.face : noto[0], "input");
    sel.addEventListener("change", () => {
      const face = fam?.faces.find((f) => f.path === sel.value);
      if (fam && face) setFace(s, fam, face);
      const pick = NOTO_STYLES.find(([k]) => k === sel.value);
      if (!s.font && pick) [, s.bold, s.italic] = pick;
      host.render();
    });
    return sel;
  }

  const input = el("input", { type: "file", accept: ".ttf,.otf,.zip", multiple: true, hidden: true }) as HTMLInputElement;
  let target: TextStyle | null = null;
  input.addEventListener("change", () => {
    const files = [...(input.files ?? [])];
    input.value = "";
    if (files.length && target && fonts.upload) void upload(target, files, fonts.upload);
  });

  async function upload(s: TextStyle, files: File[], send: (files: File[]) => Promise<FontFamily>): Promise<void> {
    host.say(S.xyimage.status.fontUploading());
    try {
      const fam = await send(files);
      setFace(s, fam, closestFace(fam, s));
      await host.uploaded(fam);
      host.say("");
      host.renderProps();
    } catch (err) { host.say(fontError(err)); }
  }

  function fields(s: TextStyle): HTMLElement[] {
    const button = fonts.upload ? host.button(S.xyimage.props.fontUpload(), () => { target = s; input.click(); }) : null;
    return [
      el("label", { class: "field" }, el("span", { text: S.xyimage.props.font() }),
        el("div", { class: "u-row u-gap-sm" }, familySelect(s), button)),
      el("label", { class: "field" }, el("span", { text: S.xyimage.props.face() }), styleSelect(s)),
    ];
  }

  return { fonts, fields, useFace, input };
}
