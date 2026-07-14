// xy app icon geometry — the single source of truth, shared by lab.html (live
// tuner) and gen-icon.js (writes icon.svg/png and installs the PWA icons).
//
// The mark is a twist on Trello's: a rounded purple tile holding two white
// cards, but the short one comes first and the tall one second, and each card
// carries a letter of the app's name — 'x' in the short card, 'y' in the tall.
//
// The cards are *derived* from the letters: each is the glyph's tight box grown
// by one `pad` on every side. The two glyphs sit on a shared baseline, so the
// cards align at the top and the y's card is taller by exactly its descender.

export const SIZE = 1024;

// Noto Sans 'x' and 'y' outlines, extracted from the TTFs vendored for the
// handout renderer (internal/chgk/handout/assets). Baked in as paths so the
// browser and rsvg-convert draw exactly the same shape with no font lookup.
export const GLYPHS = {
  regular: {
    upm: 1000,
    x: { path: "M212 274 27 536H127L265 334L402 536H501L316 274L511 0H411L265 214L117 0H18Z", xMin: 18, xMax: 511, yMin: 0, yMax: 536 },
    y: { path: "M1 536H95L211 231Q226 191 238.0 154.5Q250 118 256 85H260Q266 110 279.0 150.5Q292 191 306 232L415 536H510L279 -74Q251 -150 206.5 -195.0Q162 -240 84 -240Q60 -240 42.0 -237.5Q24 -235 11 -232V-162Q22 -164 37.5 -166.0Q53 -168 70 -168Q116 -168 144.5 -142.0Q173 -116 189 -73L217 -2Z", xMin: 1, xMax: 510, yMin: -240, yMax: 536 },
  },
  bold: {
    upm: 1000,
    x: { path: "M190 279 14 546H183L289 372L396 546H565L387 279L573 0H404L289 187L174 0H5Z", xMin: 5, xMax: 573, yMin: 0, yMax: 546 },
    y: { path: "M0 546H159L262 234Q269 212 275.5 187.5Q282 163 285 141H289Q292 162 299.5 187.0Q307 212 314 234L415 546H571L347 -50Q313 -142 260.5 -191.0Q208 -240 111 -240Q85 -240 66.5 -237.5Q48 -235 35 -232V-113Q45 -115 61.0 -117.0Q77 -119 94 -119Q140 -119 165.0 -92.5Q190 -66 205 -27L217 4Z", xMin: 0, xMax: 571, yMin: -240, yMax: 546 },
  },
};

// [key, label, min, max, step, default, decimals] — drives both the lab sliders
// and the CLI flags, so a new knob only has to be added here.
export const RANGES = [
  ["bgRadius", "Tile corner", 0, 512, 2, 184, 0],
  ["letterSize", "Letter size", 100, 900, 2, 100, 0],
  ["pad", "Card padding", 0, 200, 1, 16, 0],
  ["gap", "Gap between cards", 0, 320, 2, 12, 0],
  ["cardRadius", "Card corner", 0, 120, 1, 16, 0],
  ["evenWidth", "Equal card widths (0/1)", 0, 1, 1, 1, 0],
  ["bold", "Bold glyphs (0/1)", 0, 1, 1, 1, 0],
  ["knockout", "Cut letters out (0/1)", 0, 1, 1, 1, 0],
  ["gradAngle", "Gradient angle", 0, 360, 1, 160, 0],
  ["artScale", "Overall size", 0.4, 6, 0.01, 4.65, 2],
  ["artDy", "Vertical nudge", -120, 120, 1, 0, 0],
];

export const COLORS = [
  ["bgTop", "Tile (from)", "#a06bf0"],
  ["bgBot", "Tile (to)", "#6427c0"],
  ["card", "Cards", "#ffffff"],
  ["ink", "Letters (when not cut out)", "#6427c0"],
];

export const DEFAULTS = Object.fromEntries([
  ...RANGES.map(([k, , , , , def]) => [k, def]),
  ...COLORS.map(([k, , def]) => [k, def]),
  ["transparent", false],
]);

/**
 * Card boxes and glyph placement, in user units. `letterSize` is the em size, so
 * the two glyphs keep the type designer's relative proportions; everything else
 * — card size, block size, centring — falls out of the glyph boxes.
 */
export function layout(p) {
  const font = GLYPHS[p.bold >= 0.5 ? "bold" : "regular"];
  const s = p.letterSize / font.upm;
  const box = (ch) => {
    const g = font[ch];
    return { g, s, w: (g.xMax - g.xMin) * s, h: (g.yMax - g.yMin) * s, above: g.yMax * s };
  };
  const glyphs = [box("x"), box("y")];

  const widths = glyphs.map((b) => b.w + 2 * p.pad);
  const cardW = p.evenWidth >= 0.5 ? [Math.max(...widths), Math.max(...widths)] : widths;
  const cardH = glyphs.map((b) => b.h + 2 * p.pad);

  const blockW = cardW[0] + p.gap + cardW[1];
  const x0 = (SIZE - blockW) / 2;
  const top = (SIZE - Math.max(...cardH)) / 2 + p.artDy;
  const baseline = top + p.pad + glyphs[0].above; // shared: both glyphs cap at yMax

  return glyphs.map((b, i) => {
    const x = i === 0 ? x0 : x0 + cardW[0] + p.gap;
    return {
      glyph: b.g,
      scale: s,
      card: { x, y: top, w: cardW[i], h: cardH[i] },
      // Tight box centred in the card; the y's descender is inside its own box,
      // so centring the box is what keeps the padding even all round.
      tx: x + cardW[i] / 2 - s * (b.g.xMin + b.g.xMax) / 2,
      ty: baseline,
    };
  });
}

export function derived(p) {
  const [a, b] = layout(p);
  return {
    cards: `${a.card.w.toFixed(0)}×${a.card.h.toFixed(0)} + ${b.card.w.toFixed(0)}×${b.card.h.toFixed(0)}`,
    block: `${(b.card.x + b.card.w - a.card.x).toFixed(0)}×${Math.max(a.card.h, b.card.h).toFixed(0)}`,
    margin: a.card.x.toFixed(0),
  };
}

export function buildSVG(params, { fixedSize = true } = {}) {
  const p = { ...DEFAULTS, ...params };
  const f = (n) => Number(n).toFixed(2);
  const items = layout(p);

  const rect = (it) =>
    `<rect x="${f(it.card.x)}" y="${f(it.card.y)}" width="${f(it.card.w)}" height="${f(it.card.h)}" ` +
    `rx="${f(p.cardRadius)}" ry="${f(p.cardRadius)}"/>`;

  const letter = (it) =>
    `<path d="${it.glyph.path}" transform="translate(${f(it.tx)} ${f(it.ty)}) ` +
    `scale(${it.scale.toFixed(6)} ${(-it.scale).toFixed(6)})"/>`;

  const a = (p.gradAngle * Math.PI) / 180;
  const [gx, gy] = [Math.cos(a), Math.sin(a)];
  const half = SIZE / 2;
  const grad =
    `<linearGradient id="xy-bg" gradientUnits="userSpaceOnUse" ` +
    `x1="${f(half - gx * half)}" y1="${f(half - gy * half)}" ` +
    `x2="${f(half + gx * half)}" y2="${f(half + gy * half)}">` +
    `<stop offset="0" stop-color="${p.bgTop}"/><stop offset="1" stop-color="${p.bgBot}"/>` +
    `</linearGradient>`;

  const knockout = p.knockout >= 0.5;
  const mask = knockout
    ? `<mask id="xy-cards" maskUnits="userSpaceOnUse" x="0" y="0" width="${SIZE}" height="${SIZE}">` +
      `<g fill="#fff">${items.map(rect).join("")}</g>` +
      `<g fill="#000">${items.map(letter).join("")}</g></mask>`
    : "";

  const art = knockout
    ? `<rect x="0" y="0" width="${SIZE}" height="${SIZE}" fill="${p.card}" mask="url(#xy-cards)"/>`
    : `<g fill="${p.card}">${items.map(rect).join("")}</g>` +
      `<g fill="${p.ink}">${items.map(letter).join("")}</g>`;

  const tile = p.transparent
    ? ""
    : `<rect x="0" y="0" width="${SIZE}" height="${SIZE}" rx="${f(p.bgRadius)}" ry="${f(p.bgRadius)}" fill="url(#xy-bg)"/>`;

  const size = fixedSize ? `width="${SIZE}" height="${SIZE}"` : `width="100%" height="100%"`;
  return (
    `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${SIZE} ${SIZE}" ${size}>` +
    `<defs>${grad}${mask}</defs>${tile}` +
    `<g transform="translate(${half} ${half}) scale(${p.artScale}) translate(${-half} ${-half})">${art}</g>` +
    `</svg>`
  );
}
