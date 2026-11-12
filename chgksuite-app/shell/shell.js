import { Call } from "/wails/runtime.js";

const call = (method, ...args) => Call.ByName(`main.Shell.${method}`, ...args);
const $ = (id) => document.getElementById(id);

// The classic form and the server list are drawn by the strip's own page, below
// it; every other tab is a panel. The last tab chosen is the one that opens.
const pages = { classic: $("classic"), "": $("servers") };
let selected = "classic";
try { selected = localStorage.getItem("chgksuite-tab") ?? selected; } catch {}

async function render() {
  const tabs = await call("Tabs");
  const classic = { id: "classic", title: "Классический UI", url: "" };
  const servers = { id: "", title: "Добавить сервер xy…", url: "" };
  if (!(selected in pages) && !tabs.some((t) => t.id === selected)) selected = "classic";
  $("tabs").replaceChildren(...[classic, ...tabs, servers].map((tab) => {
    const b = document.createElement("button");
    b.type = "button";
    b.className = "seg-btn" + (tab.id === selected ? " active" : "");
    b.textContent = tab.title;
    b.title = tab.url;
    b.onclick = () => select(tab.id);
    return b;
  }));
  $("list").replaceChildren(...tabs.filter((t) => t.id !== "local").map((tab) => {
    const row = document.createElement("div");
    row.className = "u-row u-align-center u-justify-between u-gap-sm";
    const name = document.createElement("span");
    name.textContent = tab.url;
    const remove = document.createElement("button");
    remove.type = "button";
    remove.className = "btn btn-xs";
    remove.textContent = "Убрать";
    remove.onclick = async () => { await call("Remove", tab.id); render(); };
    row.append(name, remove);
    return row;
  }));
  for (const [id, page] of Object.entries(pages)) page.hidden = selected !== id;
}

async function select(id) {
  selected = id;
  try { localStorage.setItem("chgksuite-tab", id); } catch {}
  await call("Select", id in pages ? "" : id);
  render();
}

// A newer release, when the app found one, is a button that installs it and
// restarts into it.
call("AvailableUpdate").then((v) => {
  if (!v) return;
  const b = $("update");
  const label = `Обновиться до версии ${v}`;
  b.textContent = label;
  b.hidden = false;
  b.onclick = async () => {
    b.disabled = true;
    b.textContent = "Обновляю…";
    try {
      await call("Update");
    } catch (err) {
      b.disabled = false;
      b.textContent = label;
      b.title = `Не получилось: ${err.message ?? err}`;
    }
  };
});

$("add").onsubmit = async (e) => {
  e.preventDefault();
  try {
    const tab = await call("Add", $("address").value);
    $("address").value = "";
    $("error").hidden = true;
    select(tab.id);
  } catch (err) {
    $("error").textContent = err.message ?? String(err);
    $("error").hidden = false;
  }
};

// Panels sit over the room, so it is measured and handed to Go on every resize.
new ResizeObserver(() => {
  const r = $("room").getBoundingClientRect();
  call("Place", Math.round(r.left), Math.round(r.top), Math.round(r.width), Math.round(r.height));
}).observe($("room"));

select(selected);
