// esbuild pipeline for the game-page bundles (ADR-0003). Bundles the TS shell
// plus the page scripts into IIFE bundles under web/assets/static/dist, which
// the .dopeui pages load as their single classic script. --watch rebuilds on
// change for the dev loop.
import esbuild from 'esbuild';

const pages = ['od', 'si', 'host', 'viewer'];
const options = {
  entryPoints: Object.fromEntries(pages.map((p) => [p, `dope/web/ts/pages/${p}.ts`])),
  bundle: true,
  format: 'iife',
  target: 'es2019',
  outdir: 'dope/web/assets/static/dist',
  sourcemap: true,
  logLevel: 'info',
};

if (process.argv.includes('--watch')) {
  const ctx = await esbuild.context(options);
  await ctx.watch();
} else {
  await esbuild.build(options);
}
