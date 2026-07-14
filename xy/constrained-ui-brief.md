# Constrained, Portable UI System — Design Brief

## Problem statement

AI coding agents produce great native macOS/iOS UI on the first prompt because UIKit/AppKit give them a **constrained, well-designed vocabulary** — you basically can't lay things out wrong. On the web, HTML+CSS give **too many degrees of freedom**, so agents fall back to "slop" even when a bespoke design system exists.

**Goal:** a way to author UI that structurally prevents slop (the compiler/validator catches off-system usage), that is **not tightly coupled to one language**, so the same layouts can drive HTML today and Rust/native later without rewriting the layouts themselves.

## Key insight

The AppKit advantage is *constraint*: a small, closed vocabulary with no escape hatches. The web problem is that CSS is always one prop away. The fix is to remove the escape hatches at the authoring surface — expose only a named set of layout primitives and design tokens, and make anything off-system a compile/validation error.

This is a known pattern: agents nail prototypes but drift when constraints are loose ("constraint decay"), and the mitigation people converge on is exposing only a locked-down design system to the LLM.

## Landscape reviewed

### Purest "no CSS, compiler catches layout errors" — elm-ui
- `mdgriffith/elm-ui`: complete alternative to HTML/CSS; "many layout errors are just not possible to write." Vocabulary: `row`, `column`, `el` + attributes (`spacing`, `padding`, `alignRight`, `centerY`). Elm's compiler is exceptionally friendly.
- Downside: niche language/ecosystem; stable release targets Elm 0.19; long-running rewrite.

### Rust
- **Iced** — Elm-inspired, constrained widget vocabulary. Native-first; web (wasm) backend is less polished.
- **Dioxus / Leptos** — typed `rsx!` compiled to wasm/HTML. BUT primitives are still `div`/`span`+arbitrary CSS, so they check *validity*, not *design-system conformance*. Rust web compile times hurt the agent iteration loop.

### Flutter Web
- AppKit-like constrained layout (constraints down, sizes up), declarative widget tree, agents do well with it.
- Downside: renders to a canvas, not real DOM — weaker a11y/SEO/text-selection; heavy runtime; not "clean HTML."

### Go (user's preferred default web language — simple, fast compile)
No mature web-targeting elm-ui equivalent exists in Go. Tiers found:
- **Type-safe HTML, not a layout language:** `gomponents` (HTML components as pure Go function calls, server-rendered, no build step) and `templ` (`.templ` → type-safe Go, often + HTMX). These check the tree but `Class("...")` is an open string → slop still possible. Good *substrate* to build a constraint layer on.
- **Go→WASM SPA:** `go-app` (declarative PWAs in Go/wasm). Elm-architecture feel, but still HTML-element based + wasm weight.
- **Conceptual bullseye, wrong target:** `Gio` (gioui.org) — real constrained layout system (`layout.Flex`, `Rigid`, `Flexed`), no CSS, pure Go. But renders to GPU canvas, not HTML.
- **Existence proof of the dream architecture:** `grindlemire/go-tui` — `.gsx` templates compile to type-safe Go, with a pure-Go flexbox layout engine (row/column/justify/align/gap/padding/min-max). It's for terminals, not the browser, but proves a compiler-checked constrained layout language in Go is very doable.

### Language-agnostic / portable UI schemas (the direction the user wants)
The decouple-layout-from-renderer idea = **server-driven UI (SDUI)** with a portable schema. Proven at scale:
- **Adaptive Cards (Microsoft)** — declarative JSON UI schema, independent renderers on web/Teams/Windows/iOS/Android. Closest large-scale "one typed spec, many native renderers."
- **Slack Block Kit** — constrained JSON UI vocabulary rendered identically across web/desktop/mobile. Poster child for "constrained vocabulary → no slop": you literally cannot express an off-system layout. `slack-block-builder` even wraps it in a SwiftUI-inspired declarative syntax.
- Gap: these are JSON-schema-first, not a *pleasant typed DSL with great compiler errors*. Nobody has built exactly the desired thing.

## Recommended architecture: neutral typed IR + generated per-language builders

Portability first, without giving up the compiler-catches-slop property.

1. **Single source of truth = a schema.** Define the constrained layout vocabulary once: a closed set of nodes (`Row`, `Column`, `Stack`, `Text`, `Spacer`, `Card`) and typed token enums (spacing / color / alignment / size). Express as JSON Schema or a tiny grammar. This schema is the thing that structurally forbids slop.
2. **Portable artifact = data.** Layouts authored as neutral data (JSON / KDL / TOML) — nothing language-specific in them. Swapping implementation languages never touches the layouts.
3. **Generate typed builder libraries per language from the schema.** Go package today (thin facade over `gomponents`), Rust crate later. Because they're generated from the schema, token enums become real compile-time types in each language → "invalid layout = compile error" AND "swap language = swap renderer, keep layouts."
4. **Renderers are per-target and swappable.** Go→HTML now; Rust→HTML or Rust→native later. Layouts never change.

This is essentially the Adaptive Cards architecture with the user's constrained-design-system opinions baked into the schema, plus a nicer generated typed authoring surface. It's what makes an agent safe to point at: the agent can only ever emit schema-valid nodes / typed builders.

### Design decision recap (the fork)
- **Option A — data format as the IR** (JSON/KDL): maximally language-agnostic; "compiler" is a schema validator (+ optional generated typed bindings). Best for portability-first.
- **Option B — real DSL with its own grammar, codegen per target** (go-tui pattern): best authoring UX and strongest compile guarantees; much more work (you maintain a compiler with multiple backends).
- **Chosen hybrid:** neutral data IR (A) + generated typed builders per language, giving A's portability with B-like compile-time typing.

## Suggested v0 to build first
- A small schema for the core layout nodes + token enums (spacing/color/align/size).
- A Go renderer over `gomponents` that consumes the schema and emits clean HTML.
- One example layout file (neutral data).
- Validate the loop: **author once → render anywhere → agent can't break it.**

## Hard caveats
- No off-the-shelf project delivers all three requirements (Go default + HTML output + compiler-blocks-slop) today; the v0 is a small build (schema + one renderer + codegen step).
- No web tool matches AppKit's "you literally can't do it wrong" as tightly, because the browser's escape hatches are always near. elm-ui gets closest by removing CSS from the authoring surface entirely; the schema approach here reproduces that discipline in a portable, language-agnostic way.

## Key references
- elm-ui: https://github.com/mdgriffith/elm-ui
- Iced: https://github.com/iced-rs/iced
- gomponents: https://github.com/maragudk/gomponents
- templ: https://github.com/a-h/templ
- go-app: https://github.com/maxence-charriere/go-app
- Gio: https://gioui.org/doc/architecture/layout
- go-tui: https://github.com/grindlemire/go-tui
- Adaptive Cards: https://adaptivecards.io/ · https://learn.microsoft.com/en-us/adaptive-cards/
- Slack Block Kit: https://docs.slack.dev/block-kit/
- slack-block-builder (SwiftUI-inspired): https://github.com/raycharius/slack-block-builder
- "Expose Your Design System to LLMs": https://hardik.substack.com/p/expose-your-design-system-to-llms
- "Constraint Decay" (agents drift without constraints): https://7minai.com/constraint-decay-llm-coding-agents/
