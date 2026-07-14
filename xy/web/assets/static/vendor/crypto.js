// Local shim for @noble/hashes' crypto import: expose WebCrypto (globalThis.crypto).
// xy only uses scrypt (deterministic) from noble; randomness comes from WebCrypto
// directly in crypto.js, so this just satisfies the import.
export const crypto = globalThis.crypto;
