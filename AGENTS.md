# dopesuite — monorepo

This repo holds six Go modules: three apps (xy, dope, spliff), two shared
layers they are built on (dopeuikit, dopecore), and one desktop GUI. Each app
has its own `AGENTS.md`, so start there.

```
dopeuikit/   pecheny.me/dopeuikit — the shared UI system:
             ui/ = generic DSL engine (no design opinions), kit/ = the design
             system (core vocabulary + expansions + core.css + fonts)
dopecore/    pecheny.me/dopecore — the shared platform layer that was extracted
             out of xy and dope. It has no AGENTS or CONTEXT of its own, and
             contains: sessions, credentials, the SQLite pool conventions and
             the migration runner (schema), webassets, the admin bulk-create,
             the on-disk blob store (blobstore), the Telegram bot (client, poll
             lock and the login conversation) and the login handshake (tglogin)
xy/          ЧГК question-editing boards (encrypted, Trello-style)
dope/        tournament management (EK/OD/KSI) + realtime web UI
spliff/      shared expenses: who paid for whom, in any currency, and who owes
             whom. This is the only module whose UI is in English.
chgksuite-gui/
             a Fyne window over xy's chgksuite CLI, generated from the flags
             that CLI declares (`chgksuite spec`)
```

- xy, dope and spliff use the shared layers through `replace
  pecheny.me/dopeuikit => ../dopeuikit` and `replace pecheny.me/dopecore =>
  ../dopecore`. The monorepo keeps the modules as siblings, which is the layout
  those paths expect, so a build needs no extra setup. The kit imports dopecore
  the same way. dopecore imports no other module of ours (`docs/adr/0004`).
- `chgksuite-gui` is deliberately left out of the cross-module recipes below. It
  needs cgo and a desktop toolchain, and neither server depends on it. Build and
  test it with its own `just check`, which also checks that it still matches the
  CLI it wraps.
- xy, dope and spliff each have a `justfile` with `just dev`, `just test` and
  `just check`. dopeuikit and dopecore have none: their recipes live in the root
  `justfile`, which also runs `test`, `fmt` and `vet` across all five modules at
  once. `just pre-commit` is the gate for the whole repo and you can run it from
  anywhere. The per-module `pre-commit` recipes just call the root one, because
  class-check needs every app's TypeScript and the shared core.css together.
- **Deploying** is done by one script for the whole repo, `deploy.py`. It has a
  table of targets (`dope-server`, `dopetest`, `xy-server`, `xytest`,
  `spliff-server`, `splifftest`), and each row says which module, package,
  binary, systemd unit and **host** that target uses. xy and spliff run on
  `vps-he`, dope runs on `vps2day-ee`. Each app's `just deploy` calls the script
  with its own targets. If you are already logged in to the production host you
  are deploying to, do **not** `ssh` to it again — just run the commands.
- **One staging site per branch.** `just deploy-branch <name>` (in `dope/`)
  gives a branch its own dope at `<name>.dopetest.pecheny.me`: its own systemd
  unit, port, database and Caddy route, all provisioned on the first run. Two
  branches can be up at once, which the single `dopetest` target cannot do.
  `just branches` lists them, `just drop-branch <name>` removes one for good.
  A new instance's database is the fixture fest by default, or a fresh backup
  of production with `--seed prod`; a redeploy never touches the database, so
  what you were testing with survives. The branch sites run on `vps-he`, not on
  dope's 960MB production box, and they carry no Telegram token, so their bot
  cannot poll the one production owns. `dopetest.pecheny.me` itself stays what
  it always was: the rehearsal against prod's own hardware, which is still
  where a schema or migration change goes first.
- **Only deploy to production from `main`, and only when `main` is pushed.
  NEVER deploy a branch to production.** Merge the branch, run `git push origin
  main`, then deploy from `main`. If you want to test a feature live, deploy the
  branch to one of the staging targets instead (`xytest`, `dopetest`,
  `splifftest`, via `just deploy-staging`). On 2026-08-16 someone deployed a
  branch to production. It overwrote a fix that existed only on a different
  branch, and a bug that had already been fixed came back in xy production.
- The full history from before the merge is kept under each subdirectory, so
  `git log` and `git blame` work on paths inside them.
- The plan is that once the DSL engine is mature, `dopeuikit/ui` (the engine
  only, not the design system) moves to its own repo and module. `kit/` and the
  assets stay here.
- The old remotes (the xy, dope and dopeuikit projects on GitLab) were frozen
  when the repos were merged. This repo is now the only source of truth.

## Frontend work is not finished until you have looked at it

Automated checks can all pass while the screen still looks wrong: overflow is 0,
the counts are right, every element is present, and the spacing is still off.
So after you build any panel, modal, bar or row, run the `design-review` skill,
and use the `verify` skill to open it in a browser at both screen sizes. Both
skills are in `.claude/skills/`.

Two mistakes keep happening. Each one has a guard:

- **Re-inventing layout.** The kit already provides `.u-col`, `.u-row`,
  `.u-gap-*`, `.u-align-*` and `.u-justify-*`. If you write a new class whose
  body contains nothing but those rules, `scripts/classcheck` will reject it.
  The check is in `layout.go`, and the classes that already existed when it was
  added are listed in `layout-baseline.txt`.
- **Spacing the children instead of the container.** Text primitives are given
  `margin: 0` on purpose. To space them out, put a `gap` on the container and
  take the value from the `--space-*` scale.

xy and dope both have a `/gallery` page that only works in dev mode. It shows
every primitive on one page, which makes it easy to compare a new thing with
the one it should look like.

## User-facing strings

Every string a person reads comes from a Catalog, never from the place in the
code that shows it (see root `docs/adr/0006`, and the terms in root
`CONTEXT.md`).

- Each Surface has one TOML file, under `<module>/i18nstrings/<lang>/`. The
  default language is `ru`. If a module's UI is in another language it says so
  with `-default-lang` on its `go:generate` line, and then it needs no `ru/`
  directory at all. `common.toml` holds the words that module shares between
  screens, and a `[table]` inside a file groups related keys.
  Ids are snake_case and describe what the string is FOR: `board.delete.confirm`,
  not `board.delete.are_you_sure`. That way rewording a string never renames it.
- Templates use `text/template`, but only two things are allowed in them:
  `{{.name}}` for a string and `{{plural .n "one" "few" "many"}}` for a number.
  Anything else fails generation. Write the template inside `'single quotes'` so
  the plural forms don't need escaping.
- To add a string: edit the TOML file, run `just generate-strings`, and commit
  the `*_gen` files next to it. `just generate-check` fails if one of them is
  out of date. To read a string, take a `Strings` value (`i18nstrings.Default`)
  and write the full path, like `s.Board.Delete.Confirm(n)`.
- Always write that full path at the call site. Never shorten it with something
  like `c := s.Board.Delete`. The generator fails if no `.go`, `.ts` or
  `.dopeui` file mentions an id in full, and that is what stops unused strings
  from piling up.
- In a `.dopeui` page, write an untemplated string as `label=@board.delete.title`,
  or as a bare `@board.delete.hint` item. A value in quotes is always taken
  literally. If the app's Catalog has no such id, the kit's Catalog is asked.
- If a person may need to read an error, build it with
  `i18nstrings.User(s.Board.Delete.Locked())`. The HTTP layer shows those
  errors as they are, and replaces every other error with one generic line.
- `just cyrillic-check` fails if it finds Cyrillic in any `.go`, `.ts` or
  `.dopeui` file outside the catalogs, the generated files and the tests. The
  files still listed in `scripts/cyrillic/allowlist.txt` are ones where the
  Cyrillic does a job: regexes that match Russian input, data tables, and the
  format strings that keep chgksuite parity. The list also has `xy/web/ts/sw.ts`,
  which is a classic worker and has no import graph. The check also fails if a
  listed file no longer has any Cyrillic in it, so the list only shrinks. Never
  add a line to it just to get a string committed.
- `just strings-check` regenerates every module's Catalog and fails if any
  `*_gen` file is out of date. It exists because the apps' own `generate-check`
  only knows about their `tags_gen.go`, and because no module can regenerate the
  kit's Catalog for itself.

### Write the strings plainly

The Russian in the catalogs is interface copy, not literature. Use normal word
order and ordinary words. Don't invert a sentence for effect, don't write
aphorisms, and use a full stop where you were about to use a dash. A hint should
read like one person explaining something to another. The same goes for commit
messages, code comments and test names, in Russian and in English.

## Toolchain

- **Go** 1.26 or newer, for all five modules.
- **just**, the task runner. There is a root justfile and one per app.
- **deno** 2 or newer. It downloads the native tsc binary (`deno install`, see
  the root `package.json`) and runs the frontend tests (`deno test --parallel`).
  Bundling itself is written in Go (`just build-web [target...]`, in
  `scripts/webbuild/`, using esbuild as a library — see `docs/adr/0001`), so
  neither building nor running the server needs a JS runtime.
- **Rust** with the `wasm32-wasip1` target. Only xy needs it, and only to build
  typst into `xy/internal/chgk/typstwasm/typst.wasm` (`cd xy && just
  build-wasm`). That file is 30 MB, it is embedded with `//go:embed`, and it is
  not in git, so every Go recipe in xy fails with an instruction until you have
  built it once.
- **Python with uv**, for `deploy.py` and the dope scripts. Only ever run
  Python through `uv` (`uv run python`).

## Git

Use plain `git`. Branch, commit and merge with ordinary git commands. Don't use
`gitbutler`, `graphite` or any wrapper script.

## Agent skills

### Issue tracker

Forgejo issues on code.pecheny.me, via the `fj` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Default five-role vocabulary; the label string is the role name. See `docs/agents/triage-labels.md`.

### Domain docs

There are several contexts: the root `CONTEXT-MAP.md` points at a `CONTEXT.md`
per module. See `docs/agents/domain.md`.
