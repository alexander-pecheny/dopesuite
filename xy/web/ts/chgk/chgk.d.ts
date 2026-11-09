// chgksuite owns the 4s parser and the question renderer; their sources are
// chgksuite/web/ts/*.ts, shared with qy.
//
// xy ships native ES modules and bundles nothing, so `just build-web xy` emits
// them beside xy's own, at /static/dist/chgk/<name>.js, and xy imports them at
// that URL (the kit's modules come in the same way, see kit/suggest.d.ts). This
// file and its siblings are how tsc finds the types behind the URL — there is
// no code here: each re-exports its one source.
export * from "../../../../chgksuite/web/ts/chgk.js";
