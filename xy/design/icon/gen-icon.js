#!/usr/bin/env -S deno run --allow-read --allow-write --allow-run --allow-net
// Render the xy icon, or serve the live tuner. Geometry lives in icon.js,
// shared with lab.html.
//
//   deno task icon                        # writes design/icon/icon.svg + icon.png
//   deno task icon --bgTop '#c08bff'      # any knob from RANGES/COLORS, by name
//   deno task icon --install              # also writes web/assets/static/icon-*.png
//   deno task lab                         # serve lab.html (ES modules need http://)
//
// Needs `rsvg-convert` (brew install librsvg) and `magick` (brew install
// imagemagick) on PATH.
import { buildSVG, COLORS, DEFAULTS, RANGES, SIZE } from "./icon.js";

const HERE = new URL(".", import.meta.url).pathname;
const STATIC = `${HERE}../../web/assets/static/`;

function parseArgs(argv) {
  const numeric = new Set(RANGES.map(([k]) => k));
  const known = new Set([...numeric, ...COLORS.map(([k]) => k), "transparent", "install"]);
  const p = { ...DEFAULTS, install: false };
  for (let i = 0; i < argv.length; i++) {
    const key = argv[i].replace(/^--/, "");
    if (!argv[i].startsWith("--") || !known.has(key)) {
      console.error(`unknown flag: ${argv[i]}\nknown: ${[...known].join(", ")}`);
      Deno.exit(2);
    }
    if (key === "transparent" || key === "install") { p[key] = true; continue; }
    const value = argv[++i];
    p[key] = numeric.has(key) ? Number(value) : value;
    if (numeric.has(key) && Number.isNaN(p[key])) {
      console.error(`--${key} wants a number, got ${value}`);
      Deno.exit(2);
    }
  }
  return p;
}

async function run(cmd, args) {
  const { success, stderr } = await new Deno.Command(cmd, { args }).output();
  if (!success) {
    console.error(`${cmd} failed:\n${new TextDecoder().decode(stderr)}`);
    Deno.exit(1);
  }
}

/** Rasterise `params` at `px`; `opaque` flattens the alpha (iOS rejects it). */
async function png(params, px, out, { opaque = false } = {}) {
  const svg = `${out}.svg`;
  await Deno.writeTextFile(svg, buildSVG(params));
  await run("rsvg-convert", ["-w", `${px}`, "-h", `${px}`, svg, "-o", out]);
  if (opaque) await run("magick", [out, "-background", params.bgBot, "-alpha", "remove", "-alpha", "off", out]);
  await Deno.remove(svg);
}

async function serveLab() {
  const port = 8000;
  const types = { ".html": "text/html", ".js": "text/javascript", ".svg": "image/svg+xml" };
  Deno.serve({ port }, async (req) => {
    let path = new URL(req.url).pathname;
    if (path === "/") path = "/lab.html";
    try {
      const body = await Deno.readFile(HERE + path.slice(1));
      const ext = path.slice(path.lastIndexOf("."));
      return new Response(body, { headers: { "content-type": types[ext] ?? "application/octet-stream" } });
    } catch {
      return new Response("not found", { status: 404 });
    }
  });
  console.log(`icon lab: http://localhost:${port}/lab.html`);
  await new Promise(() => {});
}

if (Deno.args[0] === "--lab") {
  await serveLab();
} else {
  const p = parseArgs(Deno.args);
  await Deno.writeTextFile(`${HERE}icon.svg`, buildSVG(p));
  await png(p, SIZE, `${HERE}icon.png`);
  console.log([...RANGES, ...COLORS].map(([k]) => `${k}=${p[k]}`).join(" "));
  console.log("wrote design/icon/icon.svg, design/icon/icon.png");

  if (p.install) {
    // A square tile for the launcher surfaces that round it themselves, and a
    // maskable one whose art sits inside the 80% safe zone Android crops to.
    const square = { ...p, bgRadius: 0 };
    await png(p, 192, `${STATIC}icon-192.png`);
    await png(p, 512, `${STATIC}icon-512.png`);
    await png(square, 180, `${STATIC}apple-touch-icon.png`, { opaque: true });
    await png({ ...square, artScale: p.artScale * 0.78 }, 512, `${STATIC}icon-maskable.png`, { opaque: true });

    // Favicon: the SVG is what modern browsers use; the .ico (16/32/48) is the
    // fallback every browser asks for at /favicon.ico whether it's linked or not.
    await Deno.writeTextFile(`${STATIC}favicon.svg`, buildSVG(p));
    const tmp = `${STATIC}favicon.tmp.png`;
    await png(p, 128, tmp);
    await run("magick", [tmp, "-define", "icon:auto-resize=48,32,16", `${STATIC}favicon.ico`]);
    await Deno.remove(tmp);
    console.log("installed icon-192, icon-512, apple-touch-icon, icon-maskable, favicon.svg, favicon.ico into web/assets/static/");
  }
}
