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
  are clicked. Every form works without it, except the nomination form
  (`server/nominateform.go`, `server/nominateview.go`, `web/ts/nominate.ts`):
  its Просмотр, Поля and Текст tabs need the script. Its load, parse, move and
  clear buttons still work without it, because the post does the same and
  shows the form again unsaved.
- A question card (`server/questionview.go`) is drawn in the browser by
  chgksuite's renderer (`chgksuite/web/ts/preview.ts`, shared with xy) from
  the fields the server puts in `data-question`; `web/ts/questions.ts` does it.
  Without the script the card shows the fields as plain text.
- HTML the server rendered itself goes through the overlay's `richtext`
  primitive. Nothing derived from a request goes there unescaped: question
  fields pass through `plain`, and only admin-written Markdown is raw.
- `server/tests/` drives whole contests through the routes; `testapi.go` is
  the seam, and it sets the clock, gotquestions and the model.
- The CMS is studchr's site_v2 ported: pages, revisions and files in SQLite.
  Its visual editor was not ported; pages are edited as Markdown or HTML.

- Jurors and admins are entered by Telegram handle or numeric Telegram id. The
  first login under a handle writes the person's id onto the entry, and from
  then on only the id matches (`store.JurorIsUser`, `store.AdminIsUser`), so a
  handle that changes hands carries no access. An anonymous juror votes like
  the others and is named nowhere: not in the rules' `{{jury}}`, not by the
  nomination check.
- The nomination check bars an editor only from their own tour: gotquestions'
  tour editors for a question it has, else the rating site's credits by the
  number the pasted text opens with, else every editor of the tournament.

## Backups

`/var/lib/qy/qy.db` is replicated by the box's litestream (`/etc/litestream.yml`
on vps-he, the same R2 bucket as xy and spliff) to `r2:backups/qy/qy.db`.
Restore with `litestream restore -config /etc/litestream.yml -o <file>
/var/lib/qy/qy.db`. `gq.db` is not backed up: `just import-gq` rebuilds it from
the dump.

## Commands

`just dev`, `just test`, `just check` as in the other apps. `just
import-archive <vopros_goda checkout>` loads the old static site into the CMS
and turns the last year's rules into the next year's, with the dates as
variables. Deploy targets are `qy-server` and `qytest` on vps-he.
