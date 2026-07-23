// Shared esbuild pipeline (root ADR-0001). Each target maps a module's TS
// sources to its shipped output; `node scripts/webbuild.mjs [target...]
// [--watch]` builds the named targets (default: all).
import esbuild from 'esbuild';

const targets = {
  dope: [
    {
      entryPoints: Object.fromEntries(
        ['od', 'si', 'host', 'viewer'].map((p) => [p, `dope/dope/web/ts/pages/${p}.ts`]),
      ),
      bundle: true,
      format: 'iife',
      outdir: 'dope/dope/web/assets/static/dist',
    },
  ],
  // menu/login ship as classic bundles (menu must run blocking in <head> —
  // theme before first paint); the pure kernels also emit as ESM for node --test.
  uikit: [
    {
      entryPoints: {
        menu: 'dopeuikit/assets/ts/menu.ts',
        login: 'dopeuikit/assets/ts/login.ts',
      },
      bundle: true,
      format: 'iife',
      outdir: 'dopeuikit/assets/dist',
    },
    {
      entryPoints: { 'menu-model': 'dopeuikit/assets/ts/menu-model.ts' },
      format: 'esm',
      outdir: 'dopeuikit/assets/dist/esm',
    },
  ],
};

const watch = process.argv.includes('--watch');
const names = process.argv.slice(2).filter((a) => !a.startsWith('--'));
for (const name of names.length ? names : Object.keys(targets)) {
  if (!targets[name]) throw new Error(`unknown target: ${name}`);
  for (const build of targets[name]) {
    const options = { logLevel: 'info', target: 'es2019', sourcemap: true, ...build };
    if (watch) {
      const ctx = await esbuild.context(options);
      await ctx.watch();
    } else {
      await esbuild.build(options);
    }
  }
}
