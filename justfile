# Monorepo fan-out. xy/ and dope/ keep their own justfiles (run those directly
# when working inside one); dopeuikit/ and dopecore/ have none, so their recipes
# live here.

default:
    @just --list

# Go tests + frontend (deno) tests, all four modules.
test: test-core test-uikit
    cd xy && just test
    cd dope && just test

fmt: fmt-core fmt-uikit fmt-scripts
    cd xy && just fmt
    cd dope && just fmt

vet: vet-core vet-uikit
    cd xy && just vet
    cd dope && just vet

# Run before committing anything, anywhere in the repo.
pre-commit: pre-commit-core pre-commit-uikit class-check
    cd xy && just pre-commit
    cd dope && just pre-commit

# The Vocabulary is closed where Go emits markup, but ~72% of the class names
# are written in TypeScript and core.css is shared by both apps — so no single
# module can check either half. Fail when the two drift: a rule nothing emits,
# or a name nothing styles. (xy's own dead-CSS test covers only the xy layer.)
# Vendor one more Lucide icon and regenerate every consumer. Icons are vendored
# rather than depended on: the build stays offline, and only shapes we actually
# use ship. Names are lucide.dev's — `just icons-add trash-2`.
icons-add name:
    #!/usr/bin/env bash
    set -euo pipefail
    ver=$(curl -sf https://registry.npmjs.org/lucide-static | python3 -c 'import json,sys; print(json.load(sys.stdin)["dist-tags"]["latest"])')
    tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
    curl -sfL "https://registry.npmjs.org/lucide-static/-/lucide-static-$ver.tgz" | tar xz -C "$tmp"
    src="$tmp/package/icons/{{name}}.svg"
    test -f "$src" || { echo "no such Lucide icon: {{name}}" >&2; exit 1; }
    cp "$src" dopeuikit/icons/svg/
    just icons-gen
    echo "vendored {{name}} from lucide-static $ver"

# Regenerate the icon set into Go, the vocabulary and both apps' TypeScript.
icons-gen:
    cd dopeuikit && go generate ./icons && go generate ./kit
    cd xy && go generate ./internal/ui
    cd dope && go generate ./dope/web/ui

class-check: fmt-scripts
    go -C scripts/classcheck vet ./...
    go -C scripts/classcheck test ./...
    go -C scripts/classcheck run .

# scripts/ holds two Go modules (webbuild, classcheck) that no module recipe
# reaches, so they had no fmt or vet until this.
fmt-scripts:
    #!/usr/bin/env bash
    set -euo pipefail
    mapfile -t files < <(find scripts -type f -name '*.go')
    ((${#files[@]} == 0)) || gofmt -w "${files[@]}"

# esbuild the frontend targets (shared toolchain, docs/adr/0001) — pure Go, no
# JS runtime. No args = all targets; `just build-web dope uikit` builds some.
build-web *targets:
    go -C scripts/webbuild run . {{targets}}

# Typecheck every tsconfig project in parallel with the native tsc binary,
# exec'd directly (deno only fetches it — no JS runtime in the loop). A test
# gate, deliberately not part of build-web: esbuild strips types unchecked, so
# the dev loop stays fast and types are enforced where tests run.
typecheck:
    #!/usr/bin/env bash
    set -euo pipefail
    [ -d node_modules ] || deno install --quiet
    tsc=$(find node_modules -path '*@typescript/typescript-*/lib/tsc' -type f | head -1)
    [ -n "$tsc" ] || { echo "native tsc not found — run 'deno install'" >&2; exit 1; }
    pids=()
    for p in dopeuikit dope xy xy/tsconfig.sw.json; do "$tsc" -p "$p" & pids+=($!); done
    rc=0
    for pid in "${pids[@]}"; do wait "$pid" || rc=1; done
    exit $rc

## dopecore ###################################################################

test-core:
    cd dopecore && go test ./...

vet-core:
    cd dopecore && go vet ./...

fmt-core:
    #!/usr/bin/env bash
    set -euo pipefail
    cd dopecore
    mapfile -t files < <(find . -type f -name '*.go')
    ((${#files[@]} == 0)) || gofmt -w "${files[@]}"

tidy-check-core:
    cd dopecore && go mod tidy -diff

pre-commit-core: fmt-core vet-core tidy-check-core test-core

## dopeuikit ##################################################################

# The kit embeds its built assets/dist (root ADR-0001), so every recipe that
# compiles dopeuikit depends on build-web.
test-uikit: build-web typecheck
    cd dopeuikit && go test ./...
    deno test --parallel dopeuikit/jstest/

vet-uikit: build-web
    cd dopeuikit && go vet ./...

fmt-uikit:
    #!/usr/bin/env bash
    set -euo pipefail
    cd dopeuikit
    mapfile -t files < <(find . -type f -name '*.go')
    ((${#files[@]} == 0)) || gofmt -w "${files[@]}"

tidy-check-uikit:
    cd dopeuikit && go mod tidy -diff

# Nothing else regenerates a tags_gen.go, so a vocab.json edit that forgets
# `go generate` ships a typed builder that doesn't match the vocabulary. Both
# app overlays generate from the kit's vocab as well as their own, so a kit edit
# leaves them stale too — check all three, not just the kit.
#
# core.css is in the same list for the same reason: its uchu ramps are generated
# from palette/uchu.json, and a hand-edited rung is exactly the drift the ladder
# exists to prevent.
generate-check: build-web
    #!/usr/bin/env bash
    set -euo pipefail
    # Compare each regenerated file against what was on disk, NOT against HEAD:
    # a git diff also fires when vocab.json and tags_gen.go are both edited
    # correctly but not yet committed, which makes pre-commit unusable mid-change.
    tmp=$(mktemp -d)
    trap 'rm -rf "$tmp"' EXIT
    rc=0
    # "module gen-target file..." — one generate run may write several files
    # (./palette emits into two stylesheets, a Go file and a TypeScript one), and
    # checking only the first would let the others go stale silently.
    for target in "dopeuikit ./kit kit/tags_gen.go" \
                  "dopeuikit ./palette assets/core.css palette/sets_gen.go ../dope/dope/web/assets/static/styles.css ../xy/web/ts/palette_gen.ts" \
                  "dope ./dope/web/ui dope/web/ui/tags_gen.go" \
                  "xy ./internal/ui internal/ui/tags_gen.go" \
                  "xy ./internal/chgk/fsource web/ts/markers_gen.ts"; do
      set -- $target
      module=$1 gentarget=$2
      shift 2
      for f in "$@"; do cp "$module/$f" "$tmp/$(echo "$f" | tr / _)"; done
      (cd "$module" && go generate "$gentarget")
      for f in "$@"; do
        if ! diff -q "$tmp/$(echo "$f" | tr / _)" "$module/$f" >/dev/null; then
          echo "$module/$f is stale w.r.t. its source: run 'go generate $gentarget' in $module/" >&2
          diff "$tmp/$(echo "$f" | tr / _)" "$module/$f" >&2 || true
          rc=1
        fi
      done
    done
    exit $rc

pre-commit-uikit: fmt-uikit vet-uikit tidy-check-uikit generate-check test-uikit
