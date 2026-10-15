---
status: accepted
date: 2026-10-09
---

# Bun replaces deno at the test gates

ADR-0001 gave deno two jobs: fetching the native tsc binary and running the
frontend suites. On 2026-10-09 deno was announced as discontinued, with support
ending a year later. Neither job depends on anything only deno does, so we moved
both to bun before deno stops getting fixes.

## Decision

- **`bun install` fetches tsc**, from the same root `package.json`, and
  `bun.lock` replaces `deno.json` and `deno.lock`. `just typecheck` still execs
  the binary directly.
- **`bun test --parallel ./<dir>/` runs the suites.** `--parallel` gives each
  file a fresh global object, as deno did, and the suites depend on that: many
  files install their own fake `document` and `fetch`. The `./` matters too,
  because without it bun reads the argument as a name filter.
- **Tests use `node:test` and `node:assert/strict` only.** The fifteen files
  that still used `Deno.test` and `jsr:@std/assert` were rewritten to match
  the other ninety-five.
- **Scripts use bun's APIs**: xy's icon generator and its two fixture
  generators.

## Consequences

- Bun runs the same 1109 tests in about 3 s, against deno's 3.7 s on the same
  machine. ADR-0001 measured bun at 6.7 s; that was an older bun.
- Bun is stricter in two places deno let slide, and two tests were fixed rather
  than worked around. `FormData.append` rejects a plain object as a file, so
  the import tests pass a real `File`. A rejection from a module's top-level
  `main()` fails the file, so `join.test.js` gives `main()` a page to run on.
