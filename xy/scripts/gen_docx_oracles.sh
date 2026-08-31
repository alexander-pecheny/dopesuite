#!/usr/bin/env bash
# Regenerates the option oracles in internal/chgk/docx/testdata: for each fixture
# and each `compose docx` switch, chgksuite's own word/document.xml (the whole
# .docx would be 400 KB of embedded fonts per variant, and only the body is
# compared). Needs a chgksuite checkout (CHGKSUITE=~/chgksuite/chgksuite).
set -euo pipefail

CHGKSUITE=${CHGKSUITE:-$HOME/chgksuite/chgksuite}
here=$(cd "$(dirname "$0")/../internal/chgk/docx/testdata" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

variants=(
  "plain:"
  "whiten:--spoilers whiten"
  "pagebreak:--spoilers pagebreak"
  "dots:--spoilers dots"
  "screen:--screen_mode replace_all"
  "versions:--screen_mode add_versions"
  "columns:--screen_mode add_versions_columns"
  "noanswers:--noanswers"
  "noparagraph:--noparagraph"
  "onlynumber:--only_question_number"
  "samesize:--smaller_source_and_author off"
)

for src in "$here"/*.4s; do
  name=$(basename "$src" .4s)
  for v in "${variants[@]}"; do
    tag=${v%%:*}
    flags=${v#*:}
    rm -rf "$work"/* 
    cp "$src" "$work/$name.4s"
    (cd "$CHGKSUITE" && uv run chgksuite compose docx $flags "$work/$name.4s" >/dev/null)
    out=$(ls "$work"/*.docx)
    unzip -p "$out" word/document.xml > "$here/${name}__${tag}.xml"
  done
done
echo "written to $here"
