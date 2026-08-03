---
status: accepted
date: 2026-08-03
---

# AGPL-3.0, because an encrypted app has to be inspectable

The repo had no licence at all, which meant the ЧГК organizers asking to run
their own xy had no right to run, modify or redistribute anything we handed
them. Picking one became unavoidable the moment self-hosting was on the table.

We license the whole monorepo under **AGPL-3.0**.

The deciding argument is xy's own trust model rather than any general preference
for copyleft. xy encrypts user content in the browser, so the security of every
board rests on the client-side code the server chooses to serve. AGPL's network
clause is what makes that checkable: anyone running a modified public instance
must offer its source, so users of an instance can verify the crypto they are
handed instead of trusting its operator's word. A permissive licence would let a
modified instance ship a backdoored `crypto.ts` with no obligation to say so.

Consequences we accept:

- Redistribution now carries obligations we have to honour ourselves: the
  release tarballs ship `LICENSE` and `NOTICE`, and `licenses/` holds the texts
  of the embedded third-party work (typst under Apache-2.0, Noto Sans and
  JetBrains Mono under OFL-1.1, @noble/hashes under MIT).
- The choice is effectively one-way. Relicensing later needs every contributor's
  agreement, which today is one person but will not stay that way.
- Nothing in the dependency tree forces copyleft — this is a deliberate choice,
  not an inherited one.

We rejected MIT and Apache-2.0 (simplest and best understood, but they permit
exactly the closed modified instance that the encryption story cannot tolerate)
and a bare "you may run this" note in the README (no right to modify or
redistribute, and legally thin next to GitHub's own terms).
