#!/usr/bin/env bun
// Render the xy icon, or serve the live tuner. Geometry lives in icon.js,
// shared with lab.html.
//
//   bun run icon                          # writes design/icon/icon.svg + icon.png
//   bun run icon --bgTop '#c08bff'        # any knob from RANGES/COLORS, by name
//   bun run icon --install                # also writes web/assets/static/icon-*.png
//   bun run lab                           # serve lab.html (ES modules need http://)
//
// Needs `rsvg-convert` (brew install librsvg) and `magick` (brew install
// imagemagick) on PATH.
import { buildSVG, COLORS, DEFAULTS, RANGES, SIZE } from "./icon.js";

const HERE = new URL(".", import.meta.url).pathname;
const STATIC = `${HERE}../../web/assets/static/`;

const LAB_PORT = 8000;
const HTTP_NOT_FOUND = 404;
const ANDROID_ICON_PX = 192;
const LARGE_ICON_PX = 512;
const APPLE_TOUCH_PX = 180;
// Android crops a maskable icon to a circle 80% wide; this keeps the art inside.
const MASKABLE_ART_SCALE = 0.78;
const FAVICON_SOURCE_PX = 128;

function parseArgs(argv) {
  const numeric = new Set(RANGES.map(([k]) => k));
  const known = new Set([...numeric, ...COLORS.map(([k]) => k), "transparent", "install"]);
  const p = { ...DEFAULTS, install: false };
  for (let i = 0; i < argv.length; i++) {
    const key = argv[i].replace(/^--/, "");
    if (!argv[i].startsWith("--") || !known.has(key)) {
      console.error(`unknown flag: ${argv[i]}\nknown: ${[...known].join(", ")}`);
      process.exit(2);
    }
    if (key === "transparent" || key === "install") { p[key] = true; continue; }
    const value = argv[++i];
    p[key] = numeric.has(key) ? Number(value) : value;
    if (numeric.has(key) && Number.isNaN(p[key])) {
      console.error(`--${key} wants a number, got ${value}`);
      process.exit(2);
    }
  }
  return p;
}

async function run(cmd, args) {
  const { success, stderr } = Bun.spawnSync([cmd, ...args]);
  if (!success) {
    console.error(`${cmd} failed:\n${stderr}`);
    process.exit(1);
  }
}

/** Rasterise `params` at `px`; `opaque` flattens the alpha (iOS rejects it). */
async function png(params, px, out, { opaque = false } = {}) {
  const svg = `${out}.svg`;
  await Bun.write(svg, buildSVG(params));
  await run("rsvg-convert", ["-w", `${px}`, "-h", `${px}`, svg, "-o", out]);
  if (opaque) await run("magick", [out, "-background", params.bgBot, "-alpha", "remove", "-alpha", "off", out]);
  await Bun.file(svg).delete();
}

function serveLab() {
  const port = LAB_PORT;
  Bun.serve({
    port,
    async fetch(req) {
      let path = new URL(req.url).pathname;
      if (path === "/") path = "/lab.html";
      const file = Bun.file(HERE + path.slice(1));
      return (await file.exists()) ? new Response(file) : new Response("not found", { status: HTTP_NOT_FOUND });
    },
  });
  console.log(`icon lab: http://localhost:${port}/lab.html`);
}

const argv = Bun.argv.slice(2);
if (argv[0] === "--lab") {
  serveLab();
} else {
  const p = parseArgs(argv);
  await Bun.write(`${HERE}icon.svg`, buildSVG(p));
  await png(p, SIZE, `${HERE}icon.png`);
  console.log([...RANGES, ...COLORS].map(([k]) => `${k}=${p[k]}`).join(" "));
  console.log("wrote design/icon/icon.svg, design/icon/icon.png");

  if (p.install) {
    // A square tile for the launcher surfaces that round it themselves, and a
    // maskable one whose art sits inside the 80% safe zone Android crops to.
    const square = { ...p, bgRadius: 0 };
    await png(p, ANDROID_ICON_PX, `${STATIC}icon-${ANDROID_ICON_PX}.png`);
    await png(p, LARGE_ICON_PX, `${STATIC}icon-${LARGE_ICON_PX}.png`);
    await png(square, APPLE_TOUCH_PX, `${STATIC}apple-touch-icon.png`, { opaque: true });
    await png({ ...square, artScale: p.artScale * MASKABLE_ART_SCALE }, LARGE_ICON_PX, `${STATIC}icon-maskable.png`, { opaque: true });

    // Favicon: the SVG is what modern browsers use; the .ico (16/32/48) is the
    // fallback every browser asks for at /favicon.ico whether it's linked or not.
    await Bun.write(`${STATIC}favicon.svg`, buildSVG(p));
    const tmp = `${STATIC}favicon.tmp.png`;
    await png(p, FAVICON_SOURCE_PX, tmp);
    await run("magick", [tmp, "-define", "icon:auto-resize=48,32,16", `${STATIC}favicon.ico`]);
    await Bun.file(tmp).delete();
    console.log("installed icon-192, icon-512, apple-touch-icon, icon-maskable, favicon.svg, favicon.ico into web/assets/static/");
  }
}
