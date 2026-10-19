import { Call } from "/wails/runtime.js";

const call = (method, ...args) => Call.ByName(`main.Shell.${method}`, ...args);
const $ = (id) => document.getElementById(id);

// "" is the server list, which the strip's own page draws below it.
let selected = "local";

async function render() {
  const tabs = await call("Tabs");
  const servers = { id: "", title: "Добавить сервер xy…", url: "" };
  $("tabs").replaceChildren(...[...tabs, servers].map((tab) => {
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
  $("servers").hidden = selected !== "";
}

async function select(id) {
  selected = id;
  await call("Select", id);
  render();
}

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
