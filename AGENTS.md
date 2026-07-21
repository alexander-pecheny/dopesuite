# dopesuite — monorepo

Three Go modules, one repo. Each has its own `AGENTS.md`; start there.

```
dopeuikit/   pecheny.me/dopeuikit — the shared UI system:
             ui/ = generic DSL engine (no design opinions), kit/ = the design
             system (core vocabulary + expansions + core.css + fonts)
xy/          ЧГК question-editing boards (encrypted, Trello-style)
dope/        tournament management (EK/OD/KSI) + realtime web UI
```

- xy and dope consume dopeuikit via `replace pecheny.me/dopeuikit => ../dopeuikit`
  — the monorepo preserves the sibling layout, so builds need nothing extra.
- xy and dope each keep a `justfile` (`just dev`, `just test`, `just pre-commit`);
  dopeuikit has none — its recipes live in the root `justfile`, which also fans
  `test`/`fmt`/`vet`/`pre-commit` out across all three.
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

- **Go** ≥ 1.26 — all three modules.
- **just** — the task runner (root + per-app justfiles).
- **node** ≥ 21 — the frontend tests, both apps (`node --test`, no npm deps).
- **Rust** + the `wasm32-wasip1` target — xy only, and only to build typst into
  `xy/internal/chgk/typstwasm/typst.wasm` (`cd xy && just build-wasm`): a 30 MB
  artifact that is `//go:embed`-ed but not in git, so every xy Go recipe fails
  with an instruction until you build it once.
- **Python + uv** — `deploy.py` and the dope scripts. Python only ever through
  `uv` (`uv run python`).

## Git

Plain `git`. Branch, commit and merge with raw git commands — no `gitbutler`, no
`graphite`, no wrapper scripts.
