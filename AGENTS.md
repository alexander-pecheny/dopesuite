# dopesuite — monorepo

Three Go modules, one repo. Each subdir keeps its own AGENTS.md, justfile,
deploy script, and module; start there.

```
dopeuikit/   pecheny.me/dopeuikit — the shared UI system:
             ui/ = generic DSL engine (no design opinions), kit/ = the design
             system (core vocabulary + expansions + core.css + fonts)
xy/          ЧГК question-editing boards (encrypted, Trello-style)
dope/        tournament management (EK/OD/KSI) + realtime web UI
```

- xy and dope consume dopeuikit via `replace pecheny.me/dopeuikit => ../dopeuikit`
  — the monorepo preserves the sibling layout, so builds need nothing extra.
- Full pre-merge history is preserved under each subdirectory (git log/blame
  work with subdir paths).
- Plan of record: when the DSL engine matures, `dopeuikit/ui` (engine only,
  NOT the design system) splits into its own repo/module; `kit/` + assets stay
  here.
- Legacy remotes (xy, dope, dopeuikit projects on GitLab) are frozen as of the
  merge; this repo is the source of truth.
