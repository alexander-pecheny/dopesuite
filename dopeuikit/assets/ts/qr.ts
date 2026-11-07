// qr.ts — a QR code drawn as one SVG path, one square per dark module. The
// login page draws the bot link with it, and so does xy's /telegram page, which
// loads it as a module of its own (dist/kit/qr.js).
import { qrcodegen } from "./vendor/qrcodegen.js";

// The quiet margin around a QR, in modules.
const QR_BORDER = 2;

export function qrSvg(text: string, label: string): SVGSVGElement {
  const qr = qrcodegen.QrCode.encodeText(text, qrcodegen.QrCode.Ecc.MEDIUM);
  const ns = "http://www.w3.org/2000/svg";
  const side = qr.size + 2 * QR_BORDER;
  let d = "";
  for (let y = 0; y < qr.size; y++) {
    for (let x = 0; x < qr.size; x++) {
      if (qr.getModule(x, y)) d += `M${x + QR_BORDER},${y + QR_BORDER}h1v1h-1z`;
    }
  }
  const svg = document.createElementNS(ns, "svg");
  svg.setAttribute("viewBox", `0 0 ${side} ${side}`);
  svg.setAttribute("shape-rendering", "crispEdges");
  svg.setAttribute("role", "img");
  svg.setAttribute("aria-label", label);
  svg.classList.add("qr");
  const path = document.createElementNS(ns, "path");
  path.setAttribute("d", d);
  svg.append(path);
  return svg;
}
