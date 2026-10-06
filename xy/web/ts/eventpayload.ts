// eventpayload.ts — the codec for a Timeline event's encrypted payload. Every
// writer and reader of payload_enc goes through it, so the shape each kind
// stores lives in one place:
//
//   comment      a plain string, or {"xy":1,"t":text,"img":[attachment ids]}
//                once it carries images. The "xy" marker keeps a hand-typed
//                JSON comment from being taken for the envelope.
//   desc_edit    {"before","after"[,"author"]}. An imported (Trello) edit
//                names its author inside, since they are not an xy user.
//   label_*      {"label","label_id"}. Rows written before 2026-07-26 have
//                no label_id.
//   attach_*     {"file"}
//   reaction     the emoji itself, as a raw string
//
// internal/xycli/eventpayload.go is the Go twin. Both read the corpus in
// internal/xycli/testdata/eventpayload.json, so a shape that one side writes
// and the other cannot read fails a test. A payload that will not decrypt, does
// not parse, or belongs to a kind this codec does not know opens as
// {kind: "unreadable"} rather than throwing.
import { xyCrypto } from "./crypto.js";
import type { DataKey } from "./crypto.js";

export interface CommentPayload { kind: "comment"; text: string; images: number[] }
// author is "" for an edit made in xy, where the event row names the author.
export interface DescEditPayload { kind: "desc_edit"; before: string; after: string; author: string }
// labelId is null on rows from before labels were referenced by id.
export interface LabelPayload { kind: "label_add" | "label_remove"; label: string; labelId: number | null }
export interface AttachPayload { kind: "attach_add" | "attach_remove" | "attach_replace"; file: string }
export interface ReactionPayload { kind: "reaction"; emoji: string }
export interface UnreadablePayload { kind: "unreadable" }

export type EventPayload =
  | CommentPayload | DescEditPayload | LabelPayload | AttachPayload | ReactionPayload;
export type OpenedPayload = EventPayload | UnreadablePayload;
export type EventKind = EventPayload["kind"];

type Data<P extends EventPayload> = Omit<P, "kind">;
interface PayloadData {
  comment: Data<CommentPayload>;
  desc_edit: Data<DescEditPayload>;
  label_add: Data<LabelPayload>;
  label_remove: Data<LabelPayload>;
  attach_add: Data<AttachPayload>;
  attach_remove: Data<AttachPayload>;
  attach_replace: Data<AttachPayload>;
  reaction: Data<ReactionPayload>;
}
// PayloadOf is what a writer hands over for one kind, so a label_add cannot be
// given a file.
export type PayloadOf<K extends EventKind> = PayloadData[K];

export const UNREADABLE: UnreadablePayload = Object.freeze({ kind: "unreadable" as const });

// encodePayload writes the plaintext a kind stores. Bundles carry this form
// (ADR-0013), which is why it is separate from the encryption.
export function encodePayload<K extends EventKind>(kind: K, data: PayloadOf<K>): string {
  const p = { ...data, kind } as unknown as EventPayload;
  switch (p.kind) {
    case "comment":
      return p.images.length ? JSON.stringify({ xy: 1, t: p.text, img: [...p.images] }) : p.text;
    case "desc_edit":
      return JSON.stringify(p.author ? { before: p.before, after: p.after, author: p.author } : { before: p.before, after: p.after });
    case "label_add":
    case "label_remove":
      return JSON.stringify(p.labelId == null ? { label: p.label } : { label: p.label, label_id: p.labelId });
    case "attach_add":
    case "attach_remove":
    case "attach_replace":
      return JSON.stringify({ file: p.file });
    case "reaction":
      return p.emoji;
  }
}

function parseObject(raw: string): Record<string, unknown> | null {
  try {
    const v: unknown = JSON.parse(raw);
    return v !== null && typeof v === "object" && !Array.isArray(v) ? v as Record<string, unknown> : null;
  } catch (_) {
    return null;
  }
}

// A field that is absent reads as the fallback; one that is there with the
// wrong type makes the payload unreadable.
function str(o: Record<string, unknown>, key: string): string | null {
  const v = o[key];
  if (v === undefined) return "";
  return typeof v === "string" ? v : null;
}

function decodeComment(raw: string): CommentPayload {
  if (raw.startsWith("{")) {
    const p = parseObject(raw);
    if (p && p.xy === 1 && typeof p.t === "string" && Array.isArray(p.img)) {
      return { kind: "comment", text: p.t, images: p.img.filter((n): n is number => typeof n === "number") };
    }
  }
  return { kind: "comment", text: raw, images: [] };
}

function decodeDescEdit(raw: string): OpenedPayload {
  const o = parseObject(raw);
  if (!o) return UNREADABLE;
  const before = str(o, "before"), after = str(o, "after"), author = str(o, "author");
  if (before === null || after === null || author === null) return UNREADABLE;
  return { kind: "desc_edit", before, after, author };
}

function decodeLabel(kind: LabelPayload["kind"], raw: string): OpenedPayload {
  const o = parseObject(raw);
  if (!o) return UNREADABLE;
  const label = str(o, "label");
  const id = o.label_id;
  if (label === null || (id !== undefined && id !== null && typeof id !== "number")) return UNREADABLE;
  return { kind, label, labelId: typeof id === "number" ? id : null };
}

function decodeAttach(kind: AttachPayload["kind"], raw: string): OpenedPayload {
  const o = parseObject(raw);
  const file = o ? str(o, "file") : null;
  return file === null ? UNREADABLE : { kind, file };
}

// remapCommentImages points a comment's images at the copies of its
// attachments: ids maps a source attachment's id to its copy's. An image whose
// attachment was not copied is dropped, since its id would name another card's
// attachment, or nothing. Any other comment comes back as it was.
export function remapCommentImages(raw: string, ids: ReadonlyMap<number, number>): string {
  const p = decodeComment(raw);
  if (!p.images.length) return raw;
  const images = p.images.map((id) => ids.get(id)).filter((id): id is number => id != null);
  return encodePayload("comment", { text: p.text, images });
}

// decodePayload reads the plaintext of an event of the given type.
export function decodePayload(type: string, raw: string): OpenedPayload {
  switch (type) {
    case "comment": return decodeComment(raw);
    case "desc_edit": return decodeDescEdit(raw);
    case "label_add": case "label_remove": return decodeLabel(type, raw);
    case "attach_add": case "attach_remove": case "attach_replace": return decodeAttach(type, raw);
    case "reaction": return raw ? { kind: "reaction", emoji: raw } : UNREADABLE;
    default: return UNREADABLE;
  }
}

// sealPayload encodes and encrypts: what goes into payload_enc,
// desc_event_enc or event_payload_enc.
export function sealPayload<K extends EventKind>(dk: DataKey, kind: K, data: PayloadOf<K>): Promise<string> {
  return xyCrypto.encField(dk, encodePayload(kind, data));
}

// openPayloads decrypts and decodes a run of events in one engine round trip.
// No key, or a field that will not open, gives UNREADABLE for that event.
export async function openPayloads(
  dk: DataKey | null,
  events: readonly { type: string; payload_enc?: string | null }[],
): Promise<OpenedPayload[]> {
  if (!dk) return events.map(() => UNREADABLE);
  let texts: (string | null)[];
  try {
    texts = await xyCrypto.decFields(dk, events.map((e) => e.payload_enc || ""));
  } catch (_) {
    return events.map(() => UNREADABLE);
  }
  return events.map((e, i) => {
    const t = texts[i];
    return t === null ? UNREADABLE : decodePayload(e.type, t);
  });
}

export async function openPayload(dk: DataKey | null, type: string, b64: string | null | undefined): Promise<OpenedPayload> {
  return (await openPayloads(dk, [{ type, payload_enc: b64 }]))[0];
}

// commentText is what search, the 🔔 and a reply's quote want from an event: a
// comment's words, a reaction's emoji, "" for anything else.
export function commentText(p: OpenedPayload): string {
  if (p.kind === "comment") return p.text;
  if (p.kind === "reaction") return p.emoji;
  return "";
}
