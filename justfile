# Monorepo fan-out. xy/ and dope/ keep their own justfiles (run those directly
# when working inside one); dopeuikit/ and dopecore/ have none, so their recipes
# live here.

default:
    @just --list

# Go tests + frontend (deno) tests, all four modules.
test: test-core test-uikit
    cd xy && just test
    cd dope && just test

fmt: fmt-core fmt-uikit
    cd xy && just fmt
    cd dope && just fmt

vet: vet-core vet-uikit
    cd xy && just vet
    cd dope && just vet

# Run before committing anything, anywhere in the repo.
pre-commit: pre-commit-core pre-commit-uikit
    cd xy && just pre-commit
    cd dope && just pre-commit

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

# Nothing else regenerates kit/tags_gen.go, so a kit/vocab.json edit that forgets
# `go generate` ships a typed builder that doesn't match the vocabulary.
# Fail if kit/tags_gen.go is stale w.r.t. kit/vocab.json.
generate-check: build-web
    #!/usr/bin/env bash
    set -euo pipefail
    cd dopeuikit
    go generate ./kit
    if ! git diff --exit-code -- kit/tags_gen.go; then
      echo "kit/tags_gen.go is stale: regenerated from kit/vocab.json, commit the result" >&2
      exit 1
    fi

pre-commit-uikit: fmt-uikit vet-uikit tidy-check-uikit generate-check test-uikit
