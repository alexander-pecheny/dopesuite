# Monorepo fan-out. xy/ and dope/ keep their own justfiles (run those directly
# when working inside one); dopeuikit/ has none, so its recipes live here.

default:
    @just --list

# Go tests + frontend (node) tests, all three modules.
test: test-uikit
    cd xy && just test
    cd dope && just test

fmt: fmt-uikit
    cd xy && just fmt
    cd dope && just fmt

vet: vet-uikit
    cd xy && just vet
    cd dope && just vet

# Run before committing anything, anywhere in the repo.
pre-commit: pre-commit-uikit
    cd xy && just pre-commit
    cd dope && just pre-commit

## dopeuikit ##################################################################

test-uikit:
    cd dopeuikit && go test ./...

vet-uikit:
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
generate-check:
    #!/usr/bin/env bash
    set -euo pipefail
    cd dopeuikit
    go generate ./kit
    if ! git diff --exit-code -- kit/tags_gen.go; then
      echo "kit/tags_gen.go is stale: regenerated from kit/vocab.json, commit the result" >&2
      exit 1
    fi

pre-commit-uikit: fmt-uikit vet-uikit tidy-check-uikit generate-check test-uikit
