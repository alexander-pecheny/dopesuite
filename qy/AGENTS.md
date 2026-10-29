# qy — agent notes

qy is the «Вопрос года» site at qy.pecheny.me. It replaces the scripts and
Google Sheets of code.pecheny.me/pecheny/vopros_goda. Its words are in
`CONTEXT.md`; the monorepo's rules are in the root `AGENTS.md`.

## Layout

```
qy/                     module root (go.mod: module "qy")
  i18nstrings/ru/       the Catalog, one TOML per Surface
  qy/cmd/qy-server      the binary
  qy/server/            the trunk: routes, pages, forms, the background pass
  qy/domain/            pure logic, each package tested on its own
    contest/            stages by date, portions, the 5+3 rule, the deal
    tally/              the shortlist cut and the final count
    question/           parsing a pasted question (chgksuite's textparse)
    anon/               the rule pass, the model prompt, applying a redaction
  qy/platform/          gotquestions (a SQLite copy of its dump: the API is down), rating.chgk.info,
                        OpenRouter, Markdown
  qy/storage/store/     the schema and the queries several handlers share
  qy/web/               route (the dispatcher), ui (the overlay), ts, assets
```

- Every page but the login is built on the server with the kit's typed builder
  (`server/layout.go`). The browser runs one small bundle, `forms.ts`, which
  asks before destructive posts and saves the jury's likes and marks as they
  are clicked. Every form works without it.
- HTML the server rendered itself goes through the overlay's `richtext`
  primitive. Nothing derived from a request goes there unescaped: question
  fields pass through `plain`, and only admin-written Markdown is raw.
- `server/tests/` drives whole contests through the routes; `testapi.go` is
  the seam, and it sets the clock, gotquestions and the model.
- The CMS is studchr's site_v2 ported: pages, revisions and files in SQLite.
  Its visual editor was not ported; pages are edited as Markdown or HTML.

## Commands

`just dev`, `just test`, `just check` as in the other apps. `just
import-archive <vopros_goda checkout>` loads the old static site into the CMS
and turns the last year's rules into the next year's, with the dates as
variables. Deploy targets are `qy-server` and `qytest` on vps-he.
