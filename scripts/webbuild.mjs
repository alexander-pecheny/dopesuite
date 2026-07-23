// Shared esbuild pipeline (root ADR-0001). Each target maps a module's TS
// sources to its shipped output; `node scripts/webbuild.mjs [target...]
// [--watch]` builds the named targets (default: all).
import esbuild from 'esbuild';
import { readdirSync } from 'node:fs';

// xy ships native ES modules: every source transforms per-file (no bundling)
// so the emitted graph mirrors the source graph.
const xySources = () =>
  readdirSync('xy/web/ts')
    .filter((f) => f.endsWith('.ts') && !f.endsWith('.d.ts'))
    .map((f) => `xy/web/ts/${f}`);

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
    // Builder-page classic scripts: self-contained IIFE bundles, one per script.
    {
      entryPoints: Object.fromEntries(
        ['pageforms', 'menu-config', 'gamecreate', 'numbers', 'profile', 'roster'].map((p) => [
          p,
          `dope/dope/web/ts/${p}.ts`,
        ]),
      ),
      bundle: true,
      format: 'iife',
      outdir: 'dope/dope/web/assets/static/dist',
    },
    // Library modules as ESM for node --test (not embedded, not served).
    {
      entryPoints: Object.fromEntries(
        ['entry-model', 'match-table', 'stage-cache', 'stats-sync', 'fest-grid'].map((p) => [
          p,
          `dope/dope/web/ts/${p}.ts`,
        ]),
      ),
      format: 'esm',
      outdir: 'dope/dope/web/jstest/dist',
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
  xy: [
    {
      entryPoints: xySources(),
      format: 'esm',
      outdir: 'xy/web/assets/static/dist',
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
