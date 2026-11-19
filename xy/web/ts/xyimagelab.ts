// xyimagelab.ts — /lab/xyimage: the Edited Image editor on a page of its own,
// with nothing to save to. For trying the editor without a board; the board
// opens the same editor over a card (attachments.ts).
import { xyApp } from "./app.js";
import { createEditor } from "./xyimageeditor.js";

async function boot(): Promise<void> {
  const me = await xyApp.requireLogin();
  if (!me) return;
  await createEditor(xyApp.byId("editor"));
}

void boot();
