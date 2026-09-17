---
status: accepted
date: 2026-08-03
---

# Releases are built and hosted on GitHub, and the forge only points at them

code.pecheny.me is the source of truth for this repo, but it has no CI runners.
xy is also not something a person can reasonably build for themselves:
`typst.wasm` is a 30 MB artifact, it is not in git, and building it needs Rust
and about five minutes. Self-hosting only works if a prebuilt binary exists, so
something has to build one.

We build releases with **GitHub Actions on the existing public mirror**, and the
release assets live there and only there.

- A release is a **CalVer tag**, such as `xy/2026.08.03`, with `.2` appended
  for a second release on the same day. Nothing outside the repo consumes xy's
  API, so semver's signal about whether something broke would be a judgement we
  had to invent for each release and that nobody would read. A date answers the
  one question an operator actually asks, which is how old their build is. The
  `xy/` prefix leaves room for dope later, and it keeps these tags out of the
  way of Go's module tagging, which would otherwise pick up a bare `v1.2.3` on
  `pecheny.me/dopecore`.
- The workflow runs **only on tags** (`.github/workflows/release.yml`). It
  compiles `typst.wasm`, runs xy's full test suite, cross-builds for linux amd64
  and arm64, and publishes the tarballs together with `SHA256SUMS`. An ordinary
  push runs nothing, because `just pre-commit` already gates those locally.
- **No cache for the wasm build.** GitHub evicts caches unused for seven days,
  and releases are rarer than that, so a cache would be cold most times it
  mattered.
- On code.pecheny.me, the push mirror carries the tags, and `mirror_sync.py`
  creates a matching Forgejo release whose body links to the download on GitHub.
  Only the metadata is mirrored, never the bytes.

The alternative we rejected was mirroring the assets themselves. Pushing them
back would mean storing a Forgejo write token as a GitHub secret, and pulling
them into Forgejo would double the storage for something a link already does for
free. The consequence we accept is that downloads leave the forge: somebody
visiting code.pecheny.me follows a link to github.com in order to get a
binary.
