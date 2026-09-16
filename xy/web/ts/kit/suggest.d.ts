// The kit owns the suggest picker; its source is dopeuikit/assets/ts/suggest.ts.
//
// xy ships native ES modules and bundles nothing, so a kit module cannot be
// inlined the way dope and spliff inline it: `just build-web xy` emits the kit's
// source beside xy's own, at /static/dist/kit/suggest.js, and xy imports it at
// that URL. This file is how tsc finds the types behind the URL — there is no
// code here, and nothing to keep in step: it re-exports the one source.
export * from "../../../../dopeuikit/assets/ts/suggest.js";
