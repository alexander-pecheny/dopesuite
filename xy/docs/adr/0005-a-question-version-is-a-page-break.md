# A question version is a page break

An editor reworking a question wants the old wording kept beside the new one
until the group picks between them. The obvious homes for that are a
`versions_enc` column on the card, or a `card_versions` table with a row per
version and an active index.

We chose neither. A Version is a run of question text inside the `?` field
itself, separated from its siblings by chgksuite's own `(PAGEBREAK)` directive —
already parsed by `chgk.ts`, already rendered as a rule by the previews, already
emitted by both exporters. So versions cost no column, no migration, no sync
verb, and a versioned card round-trips through chgksuite untouched, which a
marker of ours could not (the same reasoning that made the Alias its own column
rather than a 4s marker).

The price is that a version is not a first-class thing. It has no name, no
author, no timestamp, and nowhere to hang one: a segment of text is all it is.
Only the question can be versioned — never the answer or the comment — because
only the question is one field whose content the directive may split.

## Consequences

- **Every version reaches the export**, page-broken. That is not a compromise
  around the representation, it IS the representation: `(PAGEBREAK)` has always
  meant «break the page here». A pack exported mid-edit contains every candidate
  wording, and pruning before delivery is the editor's job. The card editor
  scopes Просмотр and Поля to one version so the work stays legible, but the
  list preview, the docx, the PDF and the раздатки all show them all.
- **A genuine mid-question page break now reads as two versions.** A long
  question deliberately split across pages will show a two-tab strip. It exports
  exactly as before, so nothing breaks — but the editor calls it something the
  author did not mean. This is the one real cost of overloading the directive,
  and it is why Формат 4s always shows the whole field: that view is the escape
  hatch, and the only place the raw truth is visible.
- Restructuring (add / delete / promote) rewrites the field in a canonical
  spelling — each version on its own lines, the separator alone between them.
  Editing one version deliberately does not: it preserves the whitespace that
  framed it, so retyping version 2 cannot reflow how version 1 breaks.
- The selected version is a view cursor, not state. Nothing about it is
  persisted, and two people editing the same card need not agree on it.
