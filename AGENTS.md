# dopesuite — monorepo

Four Go modules, one repo: two apps (xy, dope) on two shared layers
(dopeuikit, dopecore). The apps have their own `AGENTS.md`; start there.

```
dopeuikit/   pecheny.me/dopeuikit — the shared UI system:
             ui/ = generic DSL engine (no design opinions), kit/ = the design
             system (core vocabulary + expansions + core.css + fonts)
dopecore/    pecheny.me/dopecore — the shared platform layer extracted out of
             xy and dope (no AGENTS/CONTEXT of its own)
xy/          ЧГК question-editing boards (encrypted, Trello-style)
dope/        tournament management (EK/OD/KSI) + realtime web UI
```

- xy and dope consume the shared layers via `replace pecheny.me/dopeuikit =>
  ../dopeuikit` and `replace pecheny.me/dopecore => ../dopecore` — the monorepo
  preserves the sibling layout, so builds need nothing extra.
- xy and dope each keep a `justfile` (`just dev`, `just test`, `just pre-commit`);
  dopeuikit and dopecore have none — their recipes live in the root `justfile`,
  which also fans `test`/`fmt`/`vet`/`pre-commit` out across all four.
- **Deploy** is one script for the whole repo: `deploy.py`, a target table
  (`dope-server`, `dope-bot`, `xy-server`, `xy-bot`) naming each unit's module,
  package, binary, systemd unit and **host** — xy is on `vps-he`, dope on
  `vps2day-ee`. Each app's `just deploy` calls it with its own targets.
  If you are already on the target production host, do **not** `ssh` to it —
  run the commands directly.
- Full pre-merge history is preserved under each subdirectory (git log/blame
  work with subdir paths).
- Plan of record: when the DSL engine matures, `dopeuikit/ui` (engine only,
  NOT the design system) splits into its own repo/module; `kit/` + assets stay
  here.
- Legacy remotes (xy, dope, dopeuikit projects on GitLab) are frozen as of the
  merge; this repo is the source of truth.

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
