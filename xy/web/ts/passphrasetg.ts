// passphrasetg.ts — the owner's copy of a Board Passphrase in their own
// Telegram (internal/server/passphrasetg.go). Called right after the words are
// set (create, import, change) and when the owner types them at the Passphrase
// Check. The server sends them through the bot and keeps only the moment it did.
//
// Never throws: a copy that could not be sent costs the owner nothing they had
// before, so no flow waits on it or fails because of it.

import { xyApp } from "./app.js";
import { xyInstance } from "./instance.js";

export interface PassphraseCopy {
  // A message went out on this call.
  sent: boolean;
  // The owner's Telegram holds the current words, sent now or earlier.
  kept: boolean;
}

const NONE: PassphraseCopy = { sent: false, kept: false };

export async function sendPassphrase(boardId: number, passphrase: string, unsentOnly = false): Promise<PassphraseCopy> {
  // A local xy has no passphrases and no bot.
  if (!xyInstance.offers("passphrase")) return NONE;
  try {
    const res = (await xyApp.jpost(`/api/boards/${boardId}/passphrase/telegram`, {
      passphrase, unsent_only: unsentOnly,
    })) as Partial<PassphraseCopy>;
    return { sent: res.sent === true, kept: res.kept === true };
  } catch (_) {
    return NONE;
  }
}
