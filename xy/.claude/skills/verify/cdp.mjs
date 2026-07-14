// Minimal CDP driver over Node's built-in WebSocket — no npm deps.
// Usage: const p = await connect(9333); await p.goto(url); await p.evalJS("…");
export async function connect(port) {
  const r = await fetch(`http://127.0.0.1:${port}/json/new?about:blank`, { method: "PUT" });
  const tab = await r.json();
  const ws = new WebSocket(tab.webSocketDebuggerUrl);
  await new Promise((res, rej) => { ws.onopen = res; ws.onerror = rej; });
  let id = 0;
  const pend = new Map();
  ws.onmessage = (m) => {
    const msg = JSON.parse(m.data);
    if (msg.id && pend.has(msg.id)) { pend.get(msg.id)(msg); pend.delete(msg.id); }
  };
  const send = (method, params = {}) => new Promise((res, rej) => {
    const i = ++id;
    pend.set(i, (msg) => msg.error ? rej(new Error(method + ": " + JSON.stringify(msg.error))) : res(msg.result));
    ws.send(JSON.stringify({ id: i, method, params }));
  });
  await send("Page.enable");
  await send("Runtime.enable");
  const evalJS = async (expr) => {
    const r = await send("Runtime.evaluate", { expression: expr, awaitPromise: true, returnByValue: true });
    if (r.exceptionDetails) throw new Error("JS: " + (r.exceptionDetails.exception?.description || JSON.stringify(r.exceptionDetails)));
    return r.result.value;
  };
  const goto = async (url) => {
    await send("Page.navigate", { url });
    for (let i = 0; i < 100; i++) {
      await new Promise((r) => setTimeout(r, 100));
      try { if (await evalJS("document.readyState") === "complete") break; } catch (_) {}
    }
    await new Promise((r) => setTimeout(r, 400)); // let module scripts run
  };
  const shot = async (path) => {
    const { data } = await send("Page.captureScreenshot", {});
    (await import("node:fs")).writeFileSync(path, Buffer.from(data, "base64"));
  };
  return { send, evalJS, goto, shot, close: () => ws.close() };
}
