// namedurl.ts — serve a generated file from the service worker at a real, named
// path instead of a blob: URL.
//
// Both built-in PDF viewers take the name they suggest on Save from the
// Content-Disposition header, or failing that from the last segment of the URL.
// A blob: URL's last segment is a UUID, which is why a generated handout saved
// as "5f2c…-….pdf" (issue #43). Handing the bytes to the worker and pointing the
// iframe at /dl/<name> gives the viewer both a real name and a real header.
//
// The bytes never touch Cache Storage — they are decrypted questions, and
// xy does not put plaintext on disk. They live in a Map in the worker, which a
// worker restart drops; that only matters if something re-fetches the URL, since
// the viewer already holds the document it loaded.

const DL_PREFIX = "/dl/";

function controller(): ServiceWorker | null {
  return ("serviceWorker" in navigator && navigator.serviceWorker.controller) || null;
}

// ask posts a message to the worker and resolves with its reply, or null if the
// worker never answers (it may have been killed between the check and the post).
function ask(sw: ServiceWorker, msg: Record<string, unknown>, transferBlob?: Blob): Promise<unknown> {
  return new Promise((resolve) => {
    const ch = new MessageChannel();
    const timer = setTimeout(() => resolve(null), 3000);
    ch.port1.onmessage = (e) => { clearTimeout(timer); resolve(e.data); };
    sw.postMessage({ ...msg, blob: transferBlob }, [ch.port2]);
  });
}

// namedUrl registers `blob` with the worker under `filename` and returns the URL
// to point an iframe or a download link at. Falls back to a blob: URL when there
// is no controlling worker (first load before activation, or no SW support) —
// the old behaviour, uuid name and all.
export async function namedUrl(blob: Blob, filename: string): Promise<string> {
  const sw = controller();
  if (!sw) return URL.createObjectURL(blob);
  const path = DL_PREFIX + encodeURIComponent(filename);
  const ok = await ask(sw, { type: "xy-dl-put", path, filename }, blob);
  return ok ? path : URL.createObjectURL(blob);
}

// revokeNamedUrl releases whichever kind of URL namedUrl handed back.
export function revokeNamedUrl(url: string): void {
  if (!url.startsWith(DL_PREFIX)) { URL.revokeObjectURL(url); return; }
  const sw = controller();
  if (sw) void ask(sw, { type: "xy-dl-del", path: url });
}

export const xyNamedUrl = { namedUrl, revokeNamedUrl };
