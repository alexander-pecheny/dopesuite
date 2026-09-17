# dopesuite — system-wide terms

These are the terms that mean the same thing in xy, dope and the kit. Each app also has its own glossary next to it (see `CONTEXT-MAP.md`).

## Language

**Catalog**:
A Catalog holds all the user-facing strings of one module in a single language. Every string a person can read comes from a Catalog: page copy, button labels, error text, CLI help, export headers. Code never contains such a string directly.
_Avoid_: translations, messages, resources

**Surface**:
One screen or area of a module, together with the file in the Catalog that holds that screen's strings. The strings inside the file are ordered the way they appear on the screen, from top to bottom.

**String Id**:
The name that code uses to refer to a string. It has three parts: the Surface, then the group, then the key. Each part says what the string is for, not what it says, so you can reword a string without renaming it.
_Avoid_: message key, translation key

**Common strings**:
Every module has one Surface for words that several screens share («Сохранить», «Отмена»). Use a common string only when the screen means exactly what the common word means.

**Default language**:
The language the UI picks when the reader hasn't chosen a different one in the settings. By default it is Russian, unless the module says otherwise (Spliff is English-only). It is also used everywhere there is no reader to have a preference: the CLI, exports and logs.

**User Error**:
An error whose message was written for the person who caused it, so the app can show that message to them as it is. Every other error is internal: the person sees one generic line, and the details go to the log.
_Avoid_: client error, validation error (a User Error can come from anywhere, not only from validation)

**Parity labels**:
The chgksuite label sets that xy embeds for document output. They are not a Catalog. They copy the upstream tool exactly, and we never edit them here.
