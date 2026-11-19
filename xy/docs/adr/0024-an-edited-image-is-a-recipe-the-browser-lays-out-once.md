# An Edited Image is a recipe the browser lays out once

## Status

Proposed, 2026-11-19. Still being settled. The first draft of this ADR made
typst the layout engine for the editor's preview as well; that was dropped the
same day, see "Alternatives considered".

## Context

Editors need a small image editor inside xy: crop a raster image, cover part of
it with a black box, and lay out text on it with alignment inside a box,
vertical alignment, line breaking, leading and tracking. The result goes into
three kinds of output. The package PDF and the раздатки are typeset by typst and
can keep text as vector. The docx export needs a raster, and `imgconv` only
reads raster formats.

The editor has to behave like an ordinary vector editor: you type directly on
the canvas and the text appears at once. That rules out any layout done on the
server, because every keystroke would wait for a round trip. At the same time
the screen and every export must break lines in exactly the same places, or a
раздатка laid out by eye prints differently.

## Decision

**An Edited Image is a new kind of Attachment, `name.xyimage`.** 4s names it
the way it names any picture, `(img plakat.xyimage)`, so the format gains no
new marker. Everything that moves attachments around (Transfer, Bundle, the
Mirror, Tombstones, the quota) treats it as bytes and needs no change.

**The file is a chunk container of our own, framed like PNG.** No base64, no
schema compiler, no new dependency:

```
"XYIMG" 0x00 | u8 format version
chunks: [4-byte ASCII type][u32 big-endian length][bytes]
  RCPE  the Recipe, UTF-8 JSON with its own version (always first): the
        frame, the items as the editor reopens them, and the bake plan
  IMAG  one picture as it was added, numbered in order (repeatable)
  OVER  one vector overlay: black boxes and Frozen Lines (repeatable)
  RAST  the raster Rendition
```

The bake plan is the frame (size in mm, background) and its layers in
stacking order: a picture layer names an `IMAG` and its rectangle relative to
the frame; an overlay layer names an `OVER`, which covers the whole frame. A
run of black boxes and text between two pictures is one overlay, so a black
box can sit over one photo and under another. The reader and writer are
`chgksuite/xyimage` and `web/ts/xyimagefile.ts`, held to
`chgksuite/xyimage/testdata/fixture.*`.

Compatibility follows PNG's rule. A chunk type that starts with an uppercase
letter is critical: a reader that does not know it refuses the file with
"made by a newer xy" rather than dropping something silently. One that starts
with a lowercase letter is optional and skipped (a future thumbnail, say). The
Recipe evolves by optional JSON fields and bumps its own version on a change of
meaning; the format byte changes only if the framing itself does. The reader
and writer exist twice, in Go (chgksuite, for the exports and xy-cli) and in
TS, and both read the same fixtures, as the Envelope's two implementations do.
Nothing outside xy ever sees the container.

**Nothing is destructive.** The source bytes are never rewritten, and the
Recipe can be reopened and changed at any time.

**An infinite canvas with one frame, and pictures are optional.** The editor is
laid out like Figma: an infinite canvas in millimetres with one frame on it,
and only what is inside the frame is baked. The frame is resizable at any time,
with a background colour (white by default), and dragging its edge reframes:
nothing on the canvas moves. Pictures are ordinary layers, as many as wanted,
and a crop is a picture framed by the frame. So one model covers a text-only
раздатка (no picture at all), text over a photo (frame = crop) and a photo with
text beside it (frame larger than the photo). Font sizes are points against
those millimetres, and the raster Rendition takes its pixels from a dpi.

**Editing a picture creates a new attachment and changes no text.**
«Редактировать» in an image attachment's ⋯ menu opens the editor, and saving
adds a *new* `.xyimage` next to the original, which stays as it was. Nothing in
the Card's 4s is rewritten: the editor chooses the Edited Image where the
question or the Handout names a picture, by hand. The price is that a
forgotten reference exports the unedited picture; that is accepted over a
rewrite the editor did not ask for.

**The browser is the only text layout engine.** A text box is the browser's
own editable text in a positioned box, with CSS for horizontal and vertical
alignment, line height and letter spacing. You get the native caret,
selection, IME and phone keyboards, and the card editor's stress-mark tool
works unchanged. Hyphenation is **off by default** and opt-in per text box
(`hyphens: auto`, lang ru).

**Layout is frozen on save.** When editing ends, the editor reads back where
each line actually broke and stores each line with its exact position. No
export ever lays the text out again; it places the Frozen Lines.

**The Renditions are baked in the browser on save**, and stored next to the
Recipe:

- the **vector Rendition** is the `OVER` chunks laid between the `IMAG`
  pictures by the bake plan. Each overlay is an SVG of black boxes and one
  positioned `<text>` per frozen word, with no raster inside it. At export the
  Go reader composes them into one SVG the size of the frame (the pictures
  embedded, the overlays inline), in memory only, and typst takes it as an
  image: geometry, never layout. typst keeps SVG text as real text, as the
  test in `typstwasm/xyimage_test.go` checks: Noto Sans embedded, with a
  ToUnicode map;
- the **raster Rendition** (`RAST`) is the whole canvas drawn at the Recipe's
  dpi, 300 by default and set per image in the editor. Because it is baked on
  save, changing the dpi is an edit, never an export option. PNG for a
  text-only canvas; for one with a photo, WebP or JPEG rather than a PNG several
  times the source's size (still open).

The exporters gain special handling for `.xyimage`: the PDF and the раздатки
take the vector Rendition and docx takes the raster one. A bare `.4s` export,
and any export of a Board's contents, ships the `.xyimage` files themselves,
with the text naming them as it does: a reader is expected to understand the
container. The Go chgksuite in this repo does, through the same reader the
exports use, so its CLI and xy's own import take such a `.4s` back.

**An Edited Image's native size is its canvas.** Every exporter asks a
`.xyimage` for its size and gets the canvas in millimetres, expressed as the
120 px/in that `inline.Img.SizeInches` expects, so it lands in a question by
chgksuite's usual rule (longest side at most 5 in unless `(img w=… h=…)`
says otherwise) and in a раздатка by its aspect ratio. The dpi changes only
sharpness, never size. pptx, openquiz and Telegram take the raster Rendition,
at that size.

**Outside the editor, xy shows the raster Rendition.** `resolveImages`
recognises an Edited Image by its first bytes (`XYIMG\0`), not its name, and
hands every preview, the list preview, Просмотр and the lightbox an object URL
of its `RAST` chunk. So every place that draws a picture works unchanged and
offline, from the Mirror, and shows what docx gets. A file that merely ends in
`.xyimage` is refused rather than misread. The lightbox offers «Редактировать»,
and the upload's optional WebP recompression skips Edited Images.

**The editor is a full-screen overlay over the open Card**, reached from an
image attachment's ⋯ «Редактировать», from the lightbox, and from «Создать
картинку» for a blank canvas. It sits on the overlay stack like the lightbox,
so back, Escape and ✕ close it, and leaving with unsaved changes asks Save or
Discard, as the Card does. The first version is built for desktop. On a phone
it opens and handles the basics by touch (tap a box to type, drag to move);
precise touch handles and pinch zoom come later.

## Consequences

- Renditions are by-products of the Recipe, rewritten on every save. They are
  baked on save, not at export, because only a browser can lay the text out,
  and xy-cli and the server's exports have none.
- Hyphenation and line breaking can differ slightly between browsers. Freezing
  means the browser that saved decides, and every output follows it. Opening
  the image in another browser reflows a box only once it is edited.
- The editor works offline; only the upload of the saved attachment needs the
  network, like any other attachment.
- A text box may only use a font both the browser and typst can load from the
  same file: Noto Sans, which both already have, or a Face uploaded to the
  Board (ADR-0025).
- If the browser's own text editing proves not good enough, the fallback is an
  engine of our own (a JS line breaker, hyphenation patterns and our own
  caret). The Recipe and the Frozen Lines stay the same either way.

## Known limitations

- Upstream (Python) chgksuite cannot read a `.xyimage`. A `.4s` laid out with it
  is missing those pictures.

## Alternatives considered

- **typst for the live preview as well as the export.** It gives the same line
  breaks everywhere by construction, and was measured: a text box renders in
  about 6 ms, a whole edit with the photo in about 82 ms. But even 6 ms plus a
  round trip is lag on every keystroke, and typing directly on the canvas is
  impossible. Dropped.
- **Bake on save into a flat raster only**: no vector text, and nothing to
  reopen.
- **A canvas editing library (Fabric.js)**: on-canvas editing and SVG and PNG
  export built in, but no hyphenation, no vertical alignment, and its own caret
  and phone-keyboard handling, which is where canvas editors tend to break.
- **JSON with the parts as base64**: a third larger, all of it re-encrypted on
  every save. **Zip**: no base64 either, and `zip.ts` exists, but a general
  archive for four fixed parts invites treating it as one.
  **CBOR**: standard and self-describing, but a Go dependency, a JS decoder,
  and no way to mark a part as must-understand. **Cap'n Proto, protobuf,
  FlatBuffers**: a schema compiler and codegen in two languages for one small
  JSON document and three blobs.
- **The Edited Image referring to its source by attachment id or name**: ids
  are fresh after every Transfer, and names are ambiguous by design.
