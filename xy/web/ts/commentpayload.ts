// commentpayload.ts — the comment payload codec, a leaf shared by the timeline
// (composing/rendering) and the search index (which wants only the words).
// A comment's encrypted payload is a plain string until it carries images; then
// it is {"xy":1,"t":text,"img":[attachment ids]}. The "xy" marker is what keeps
// a hand-typed JSON comment from being mistaken for the envelope.

export function encodeCommentPayload(text: string, images: readonly number[]): string {
  if (!images.length) return text;
  return JSON.stringify({ xy: 1, t: text, img: [...images] });
}

export function decodeCommentPayload(raw: string): { text: string; images: number[] } {
  if (raw.startsWith("{")) {
    try {
      const p = JSON.parse(raw) as { xy?: unknown; t?: unknown; img?: unknown };
      if (p && p.xy === 1 && typeof p.t === "string" && Array.isArray(p.img)) {
        return { text: p.t, images: p.img.filter((n): n is number => typeof n === "number") };
      }
    } catch (_) { /* an ordinary comment that happens to open with { */ }
  }
  return { text: raw, images: [] };
}
