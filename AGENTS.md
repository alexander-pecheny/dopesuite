# dopesuite — monorepo

Four Go modules, one repo: two apps (xy, dope) on two shared layers
(dopeuikit, dopecore). The apps have their own `AGENTS.md`; start there.

```
dopeuikit/   pecheny.me/dopeuikit — the shared UI system:
             ui/ = generic DSL engine (no design opinions), kit/ = the design
             system (core vocabulary + expansions + core.css + fonts)
dopecore/    pecheny.me/dopecore — the shared platform layer extracted out of
             xy and dope (no AGENTS/CONTEXT of its own): sessions, credentials,
             the SQLite pool conventions and the migration runner (schema),
             webassets, the admin bulk-create,
             the Telegram bot — client, poll lock and the login conversation —
             and the login handshake (tglogin)
xy/          ЧГК question-editing boards (encrypted, Trello-style)
dope/        tournament management (EK/OD/KSI) + realtime web UI
```

- xy and dope consume the shared layers via `replace pecheny.me/dopeuikit =>
  ../dopeuikit` and `replace pecheny.me/dopecore => ../dopecore` — the monorepo
  preserves the sibling layout, so builds need nothing extra. The kit imports
  dopecore the same way; dopecore imports no other module (`docs/adr/0004`).
- xy and dope each keep a `justfile` (`just dev`, `just test`, `just check`);
  dopeuikit and dopecore have none — their recipes live in the root `justfile`,
  which also fans `test`/`fmt`/`vet` out across all four. `just pre-commit` is
  the root gate from anywhere: the module ones delegate up to it, because
  class-check needs both apps' TypeScript and the shared core.css at once.
- **Deploy** is one script for the whole repo: `deploy.py`, a target table
  (`dope-server`, `dopetest`, `xy-server`, `xytest`) naming each unit's module,
  package, binary, systemd unit and **host** — xy is on `vps-he`, dope on
  `vps2day-ee`. Each app's `just deploy` calls it with its own targets.
  If you are already on the target production host, do **not** `ssh` to it —
  run the commands directly.
- **Production deploys come from `main` only, and from a `main` that is
  pushed. NEVER deploy a branch to prod.** Merge, `git push origin main`, then
  deploy from `main`. A branch may go to the staging targets
  (`xytest`, `dopetest`; `just deploy-staging`) to live-test a feature. On
  2026-08-16 a branch deploy overwrote a fix that lived only on another branch
  and put a fixed bug back into xy prod.
- Full pre-merge history is preserved under each subdirectory (git log/blame
  work with subdir paths).
- Plan of record: when the DSL engine matures, `dopeuikit/ui` (engine only,
  NOT the design system) splits into its own repo/module; `kit/` + assets stay
  here.
- Legacy remotes (xy, dope, dopeuikit projects on GitLab) are frozen as of the
  merge; this repo is the source of truth.

## Frontend work is not done until it has been looked at

Behaviour checks pass while a surface looks bolted on: overflow 0, counts right,
elements present, and the spacing still wrong. Run the `design-review` skill
after building any panel, modal, bar or row, and the `verify` skill to drive it
in a browser at both sizes. Both live in `.claude/skills/`.

The two mistakes that keep recurring, and their guards:

- **Re-inventing layout.** The kit ships `.u-col`/`.u-row`/`.u-gap-*`/
  `.u-align-*`/`.u-justify-*`. `scripts/classcheck` refuses a NEW class whose
  body is only those (the ratchet in `layout.go`; what exists is grandfathered
  in `layout-baseline.txt`).
- **Spacing children instead of containers.** Text primitives carry `margin: 0`
  deliberately. Give the container a `gap` from the `--space-*` scale.

xy has `/gallery` and dope has `/gallery`, both dev-only: every primitive on one
page, which is what makes "look at it beside its twin" cheap.

## User-facing strings

Every string a person reads comes from a Catalog, never from the call site
(root `docs/adr/0006`, terms in root `CONTEXT.md`).

- One TOML file per Surface under `<module>/i18nstrings/<lang>/`, `ru` the
  default; `common.toml` is the module's shared words. A `[table]` groups keys.
  Ids are snake_case and name the string's ROLE: `board.delete.confirm`, never
  `board.delete.are_you_sure`. Rewording never renames.
- Templates are `text/template`, restricted to `{{.name}}` (a string) and
  `{{plural .n "one" "few" "many"}}` (an int). Anything else fails generation.
  Write the template as a `'literal string'` so the forms need no escapes.
- Add one: edit the TOML, run `just generate-strings`, commit the `*_gen` files
  beside it. `just generate-check` fails on a stale one. Callers hold a
  `Strings` value (`i18nstrings.Default`) and write `s.Board.Delete.Confirm(n)`.
- An error a person may read is `i18nstrings.User(s.Board.Delete.Locked())`;
  the HTTP edge shows those verbatim and everything else as one generic line.
- `just cyrillic-check` fails on Cyrillic in any `.go`/`.ts`/`.dopeui` outside
  the catalogs, generated files and tests. What still has some is listed in
  `scripts/cyrillic/allowlist.txt` — the migration's burn-down, so a listed
  file with no Cyrillic left fails too. Never add a line to it to land a string.

## Toolchain

- **Go** ≥ 1.26 — all four modules.
- **just** — the task runner (root + per-app justfiles).
- **deno** ≥ 2 — fetches the native tsc binary (`deno install`, root
  `package.json`) and runs the frontend tests (`deno test --parallel`). Bundling
  itself is pure Go (`just build-web [target...]` → `scripts/webbuild/`,
  esbuild-as-library; see `docs/adr/0001`), so no JS runtime is on the build or
  server dev path.
- **Rust** + the `wasm32-wasip1` target — xy only, and only to build typst into
  `xy/internal/chgk/typstwasm/typst.wasm` (`cd xy && just build-wasm`): a 30 MB
  artifact that is `//go:embed`-ed but not in git, so every xy Go recipe fails
  with an instruction until you build it once.
- **Python + uv** — `deploy.py` and the dope scripts. Python only ever through
  `uv` (`uv run python`).

## Git

Plain `git`. Branch, commit and merge with raw git commands — no `gitbutler`, no
`graphite`, no wrapper scripts.

## Agent skills

### Issue tracker

Forgejo issues on code.pecheny.me, via the `fj` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Default five-role vocabulary; label string = role name. See `docs/agents/triage-labels.md`.

### Domain docs

Multi-context: root `CONTEXT-MAP.md` pointing at per-module `CONTEXT.md` files. See `docs/agents/domain.md`.
