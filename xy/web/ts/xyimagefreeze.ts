// xyimagefreeze.ts — the Edited Image's Frozen Lines and its vector overlay
// (ADR-0024). The browser lays a text box out; freezeBox reads back where it
// actually put every grapheme and keeps one entry per word, with its x and its
// baseline in millimetres. overlaySVG writes those words and the black boxes
// as an SVG that typst places as it is, so no export ever lays the text out
// again.

import type { FaceRef } from "./xyimagefonts.js";

const MM_PER_IN = 25.4;
const PT_PER_IN = 72;
export const PT_MM = MM_PER_IN / PT_PER_IN;
// Coordinates are written to a thousandth of a millimetre.
const PRECISION = 1000;
const WEIGHT = { regular: 400, bold: 700 };

export type Align = "left" | "center" | "right" | "justify";
export type VAlign = "top" | "middle" | "bottom";

export interface TextStyle {
  size: number; // pt
  leading: number; // multiple of the size
  tracking: number; // pt
  align: Align;
  valign: VAlign;
  bold: boolean;
  italic: boolean;
  hyphens: boolean;
  // An uploaded Face (ADR-0025). It is a weight and a style of its own, so
  // bold and italic apply only to the bundled Noto Sans, which has no Face.
  font?: FaceRef | null;
}

export interface Rect { x: number; y: number; w: number; h: number } // mm

export interface FrozenWord { text: string; x: number; y: number } // mm, y = baseline

export interface FrozenBox {
  style: TextStyle;
  lines: FrozenWord[][];
}

const HYPHENS = new Set(["-", "‐", "‑", "–", "—"]);
const SOFT_HYPHEN = "­";
const LINE_JUMP = 0.5; // of the font size: a top that moves more starts a line

interface Glyph { g: string; left: number; top: number; space: boolean }

// Intl.Segmenter is in every browser xy supports, but not in the lib the
// toolchain targets.
interface Segmenter { segment(s: string): Iterable<{ segment: string; index: number }> }
type SegmenterCtor = new (locale: string, o: { granularity: "grapheme" }) => Segmenter;

function glyphsOf(body: HTMLElement): Glyph[] {
  const seg = new (Intl as unknown as { Segmenter: SegmenterCtor }).Segmenter("ru", { granularity: "grapheme" });
  const walker = document.createTreeWalker(body, NodeFilter.SHOW_TEXT);
  const range = document.createRange();
  const out: Glyph[] = [];
  for (let n = walker.nextNode(); n; n = walker.nextNode()) {
    const data = n.nodeValue ?? "";
    for (const s of seg.segment(data)) {
      const space = /^\s+$/u.test(s.segment);
      if (s.segment === SOFT_HYPHEN) continue;
      if (space) { out.push({ g: s.segment, left: 0, top: 0, space }); continue; }
      range.setStart(n, s.index);
      range.setEnd(n, s.index + s.segment.length);
      const rects = range.getClientRects();
      const r = rects[rects.length - 1];
      if (!r) continue;
      out.push({ g: s.segment, left: r.left, top: r.top, space });
    }
  }
  return out;
}

// freezeBox reads the laid-out text of body back as words. origin is the
// canvas's top-left on screen, scale its px per mm, ascent the font's ascent in
// screen px (the distance from a glyph box's top to its baseline).
export function freezeBox(body: HTMLElement, style: TextStyle, origin: DOMRect, scale: number, ascent: number): FrozenBox {
  const fontPx = style.size * PT_MM * scale;
  const lines: FrozenWord[][] = [];
  let line: FrozenWord[] | null = null;
  let word: FrozenWord | null = null;
  let lineTop = Number.NaN;
  let spaceSince = true;
  for (const gl of glyphsOf(body)) {
    if (gl.space) { spaceSince = true; word = null; continue; }
    const newLine = !line || Math.abs(gl.top - lineTop) > fontPx * LINE_JUMP;
    if (newLine) {
      // A word still open at the break was split by the browser (a space
      // closes it); with hyphenation on, it drew a hyphen the text does not hold.
      if (word && style.hyphens && !HYPHENS.has(word.text.slice(-1))) word.text += "-";
      line = [];
      lines.push(line);
      lineTop = gl.top;
      word = null;
    }
    if (!word || spaceSince) {
      word = { text: "", x: (gl.left - origin.left) / scale, y: (gl.top + ascent - origin.top) / scale };
      line!.push(word);
    }
    word.text += gl.g;
    spaceSince = false;
  }
  return { style, lines };
}

function textOpen(family: string, s: TextStyle): string {
  const noto = !s.font; // an uploaded Face is its own weight and style
  const weight = noto && s.bold ? WEIGHT.bold : WEIGHT.regular;
  const style = noto && s.italic ? "italic" : "normal";
  return `<text font-family="${esc(s.font?.family ?? family)}" font-size="${n(s.size * PT_MM)}" font-weight="${weight}" font-style="${style}" letter-spacing="${n(s.tracking * PT_MM)}" fill="#000">`;
}

function esc(s: string): string {
  return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
}

function n(v: number): string { return String(Math.round(v * PRECISION) / PRECISION); }

export interface OverlayOpts {
  family: string; // the bundled family's name, which both the browser's embedded faces and typst know
  fontFaces?: string; // @font-face rules to embed, for the browser's own raster
}

// A layer of the overlay, in the order the editor stacks them: a black box
// drawn after a text box covers it.
export type Layer = { box: Rect } | { text: FrozenBox };

// overlaySVG is the vector overlay: black boxes and the frozen words, in a
// viewBox of millimetres the size of the canvas.
export function overlaySVG(w: number, h: number, layers: Layer[], o: OverlayOpts): string {
  const parts = [`<svg xmlns="http://www.w3.org/2000/svg" width="${n(w)}mm" height="${n(h)}mm" viewBox="0 0 ${n(w)} ${n(h)}">`];
  if (o.fontFaces) parts.push(`<style>${o.fontFaces}</style>`);
  for (const layer of layers) {
    if ("box" in layer) {
      const b = layer.box;
      parts.push(`<rect x="${n(b.x)}" y="${n(b.y)}" width="${n(b.w)}" height="${n(b.h)}" fill="#000"/>`);
      continue;
    }
    const t = layer.text;
    const s = t.style;
    const words = t.lines.flat();
    if (!words.length) continue;
    parts.push(textOpen(o.family, s));
    for (const wd of words) parts.push(`<tspan x="${n(wd.x)}" y="${n(wd.y)}">${esc(wd.text)}</tspan>`);
    parts.push("</text>");
  }
  parts.push("</svg>");
  return parts.join("");
}
