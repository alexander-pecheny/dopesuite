# A Board holds Font Families as zips, and a Recipe names them by hash

## Status

Proposed, 2026-11-19. Belongs with [ADR-0024](0024-an-edited-image-is-a-recipe-the-browser-lays-out-once.md).

## Context

An Edited Image's text is set in Noto Sans by default, and users may upload
fonts of their own; licensing is the user's responsibility. The same font is
typically used across many images and раздатки, and a family is four files or
more, so storing the font inside each `.xyimage` multiplies the cost by the
number of images. Storing it once per Board is the obvious fix, but the
obvious ways of referring to it from an image break: attachment and row ids are
renumbered by every Transfer, and names are ambiguous.

## Decision

**A Font Family is a Board entity stored as one zip of its Faces.** The Family
is what the editor shows and picks («Inter»); a Face is one TTF or OTF file in
the zip, at one weight and style. The zip is identified by the SHA-256 of its
bytes. typst reads neither WOFF nor WOFF2, so a Face must be TTF or OTF.
Variable fonts are refused for now, because typst sets only their default
instance.

**A Recipe refers to a Family by the zip's hash plus the Face's path inside
it**, e.g. `(a1b2…, Inter-Bold.otf)`. The Family's name is kept only for
display.

**Only the newest zip of a name is live.** Changing a Family (adding bold,
replacing the italic) is uploading a new zip under the same name. The upload
warns that images using the Family may be laid out differently, then rewrites
every `.xyimage` on the Board that refers to the old zip: the reference moves to
the new hash, the text is laid out again in the new font and both Renditions are
re-baked, so docx and PDF never disagree about which font an image is in. A
different font file laying the text out differently is accepted as the natural
result of changing the font. Each image is put back by the existing
replace-in-place (same id, rev+1). This needs the network; if it stops part way,
the old zip stays, hidden from the picker, until no image refers to it, and is
then deleted through the ordinary check.

**The hash is never visible to the server.** A plaintext hash of a font file
would let the server confirm which known font a Board uses. The hash lives in
the encrypted Family metadata and inside the encrypted `.xyimage`; the server
stores opaque blobs and sees only how many there are and how big. The client
deduplicates, after decrypting the Board's font list, which is short.

**Fonts travel through the one Transfer path (ADR-0014).** A Bundle gains a
`fonts/` section, and `sliceBundle` adds the Family zips that the selected
Cards' `.xyimage` attachments reach, the same way it adds their Labels and
Sessions. On the target a zip whose hash is already there is reused. When the
target has a different zip under the same name, the incoming one arrives as an
upload would: it becomes the newest of that name, with the same rewrite. A live copy, «Переместить
список» and a Bundle file all go through this.

**A `.4s` export carries its fonts the same way.** Since a bare `.4s` ships
the `.xyimage` files themselves (ADR-0024), the export zip gets a `fonts/`
folder holding `<hash>.zip` for every Family those images reach, the same
layout as a Bundle's `fonts/`. The Go chgksuite looks a Family up there by hash.
xy's import of such a `.4s` brings the Families into the target Board: a hash
already there is reused, and anything else arrives as an upload under its name
would, newest wins, with the same warning and rewrite.

**A missing Face fails the export loudly.** Substituting Noto Sans would break
the Frozen Lines, so a vector export of an image whose Face is missing is a
User Error naming the image and the font. The raster Rendition was baked
already, so docx and a bare `.4s` still export.

**Deleting a Family is checked in the client.** Only the client can read which
images use which Family. Deleting a Family opens the Board's `.xyimage`
attachments (the Mirror usually holds them), and refuses while any image uses
it, listing those images. A Family nothing uses becomes an
ordinary Tombstone.

Font bytes count towards the Board's quota, with a cap of about 20 MB per Face.

## Consequences

- The typst pool loads its fonts once at start. A vector export now needs the
  Board's Faces added per request, which is a new guest call next to
  `add_image`.
- The browser loads a Face from bytes with the `FontFace` API, under a name of
  our own derived from the zip's hash and the Face's path, so an uploaded font can never shadow Noto Sans
  or another upload with the same internal name.
- Deleting a Family costs a scan of the Board's Edited Images. That is
  acceptable because it is rare.

## Alternatives considered

- **One row and one hash per Face**, with images pinned to the exact bytes they
  were laid out with: finer deduplication and no reflow on re-upload, but more
  entities, and old images keep a font the editors meant to replace.
- **Showing every uploaded zip of a name**, or refusing a re-upload that changes
  a Face in use: both keep stale fonts alive that nobody asked for.
- **The font inside each `.xyimage`**: self-contained, but a family used by ten
  images is stored ten times.
- **The Recipe naming a Family by id or by name**: ids are fresh after every
  Transfer, names collide, and a Family changed later would reflow old images.
- **Deleting freely**, failing exports later; **never deleting a Face**, letting
  storage only grow.

## As built

- A Face goes by `xyf-<first 16 hex of the zip's hash>-<fnv1a of its path>`
  everywhere: the browser's FontFace, the overlay SVG and typst. The typst
  guest's `add_named_font` registers it under that family for one generation;
  `reset_files` drops it. So an upload calling itself Noto Sans can never
  stand in for the bundled one.
- The Recipe's `bake.fonts` lists `{zip, face, family, name}` per Face used.
  Exports receive Family zips as `font` parts keyed by the hash the server
  computes; a `.4s` in a pack writes them to `fonts/<hash>.zip`.
- A text box's font is a Family and a Face picker; Noto Sans keeps its
  bold/italic as four Faces.
- The replacement re-bakes each image through an editor laid out off-screen
  (`attachments.replaceFamily`); the old Family is deleted once nothing names it.
- The ☰ menu's «Шрифты» panel lists, uploads and deletes Families; deleting
  is refused while a picture is set in one.
- Not done: a Family's zip is not kept in the offline Mirror, so editing a
  picture in an uploaded font needs the network.
