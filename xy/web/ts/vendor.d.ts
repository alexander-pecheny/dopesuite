// The vendored @noble/hashes scrypt build stays plain JS under static/vendor/
// (served + SW-precached there); sources import it by its emitted-relative
// path, which tsc matches against this wildcard declaration (an ambient module
// name may not be relative, but a wildcard pattern still matches "../vendor/…").
declare module "*/vendor/scrypt.js" {
  export function scrypt(
    password: Uint8Array | string,
    salt: Uint8Array | string,
    opts: { N: number; r: number; p: number; dkLen: number },
  ): Uint8Array<ArrayBuffer>;
}
