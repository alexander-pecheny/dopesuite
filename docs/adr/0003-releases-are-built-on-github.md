---
status: accepted
date: 2026-08-03
---

# Releases are built and hosted on GitHub, and the forge only points at them

code.pecheny.me is the source of truth for this repo, but it has no CI runners,
and xy is not a thing anyone can reasonably build themselves: `typst.wasm` is a
30 MB artifact that is not in git and needs Rust and about five minutes to
compile. Self-hosting is only real if a prebuilt binary exists, so something has
to build one.

We build releases with **GitHub Actions on the existing public mirror**, and the
release assets live there and only there.

- A release is a **CalVer tag**, `xy/2026.08.03` (`.2` for a second release the
  same day). Nothing external consumes xy's API, so semver's break/no-break
  signal would be a judgement we invent per release and nobody reads; a date
  tells an operator the one thing they ask, which is how old their build is. The
  `xy/` prefix leaves room for dope later and keeps these tags away from Go's
  module tagging, which would otherwise notice a bare `v1.2.3` on
  `pecheny.me/dopecore`.
- The workflow runs **on tags only** (`.github/workflows/release.yml`): compile
  `typst.wasm`, run xy's full test suite, cross-build linux amd64 and arm64,
  publish tarballs plus `SHA256SUMS`. Ordinary pushes run nothing — `just
  pre-commit` already gates them locally.
- **No cache for the wasm build.** GitHub evicts caches unused for seven days,
  and releases are rarer than that, so a cache would be cold most times it
  mattered.
- On code.pecheny.me the push mirror carries the tags, and `mirror_sync.py`
  creates a matching Forgejo release whose body links to the GitHub download.
  Metadata, never bytes.

Mirroring the assets themselves was the alternative we rejected: pushing them
back would mean storing a Forgejo write token as a GitHub secret, and pulling
them into Forgejo would double the storage for a link that costs nothing. The
consequence to live with is that downloads leave the forge — a visitor to
code.pecheny.me follows a link to github.com to get a binary.
