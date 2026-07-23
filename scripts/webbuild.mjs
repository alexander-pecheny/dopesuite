// Shared esbuild pipeline (root ADR-0001). Each target maps a module's TS
// sources to its shipped output; `node scripts/webbuild.mjs [target...]
// [--watch]` builds the named targets (default: all).
import esbuild from 'esbuild';

const targets = {
  dope: {
    entryPoints: Object.fromEntries(
      ['od', 'si', 'host', 'viewer'].map((p) => [p, `dope/dope/web/ts/pages/${p}.ts`]),
    ),
    bundle: true,
    format: 'iife',
    target: 'es2019',
    outdir: 'dope/dope/web/assets/static/dist',
    sourcemap: true,
  },
};

const watch = process.argv.includes('--watch');
const names = process.argv.slice(2).filter((a) => !a.startsWith('--'));
for (const name of names.length ? names : Object.keys(targets)) {
  if (!targets[name]) throw new Error(`unknown target: ${name}`);
  const options = { logLevel: 'info', ...targets[name] };
  if (watch) {
    const ctx = await esbuild.context(options);
    await ctx.watch();
  } else {
    await esbuild.build(options);
  }
}
