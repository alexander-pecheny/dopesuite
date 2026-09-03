# dopesuite — system-wide terms

Terms that hold across xy, dope and the kit. Each app's own glossary lives beside it (see `CONTEXT-MAP.md`).

## Language

**Catalog**:
One module's complete set of user-facing strings in one language. Every string a person can read — page copy, button labels, error text, CLI help, export headers — comes from a Catalog; code holds no such string of its own.
_Avoid_: translations, messages, resources

**Surface**:
One screen or area of a module, and the one file of its Catalog that holds its strings. A Surface reads top to bottom the way the screen does.

**String Id**:
The name code uses for one string: Surface, then group, then key, each named for the string's role, never for its wording. Rewording a string never renames it.
_Avoid_: message key, translation key

**Common strings**:
The one Surface of a module for words shared across screens («Сохранить», «Отмена»). A screen uses a common string only when it means exactly the common thing.

**Default language**:
The language a module renders in when nothing chose one: Russian. Places without a reader's preference — the CLI, exports, logs — always use it.

**User Error**:
A failure whose message was written for the person who caused it and may be shown verbatim. Every other error is an internal one: the person sees a generic line, the log sees the detail.
_Avoid_: client error, validation error (a User Error may come from anywhere, not only from validation)

**Parity labels**:
The chgksuite label sets embedded in xy for document output. They are not a Catalog: they mirror the upstream tool byte for byte and are never edited here.
