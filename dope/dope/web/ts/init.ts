// Reads the per-request init payload the server splices into a non-executable
// <script type="application/json" data-dope-init id="__NAME__"> block and exposes
// it as window.__NAME__ (e.g. window.__HOST_INIT__), the shape host.js/viewer.js/
// od.js/si.js/match-table.js already consume. Delivering it as a JSON data block
// instead of an inline <script>window.…=…</script> keeps the pages free of inline
// script so a strict `script-src 'self'` CSP needs no nonce. Must load before the
// page scripts that read the globals — it is the first classicscript on each page.
for (const el of document.querySelectorAll('script[type="application/json"][data-dope-init]')) {
  try {
    (window as unknown as Record<string, unknown>)[el.id] = JSON.parse(el.textContent!);
  } catch (_) {
    (window as unknown as Record<string, unknown>)[el.id] = null;
  }
}

export {};
