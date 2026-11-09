const esbuild = require('esbuild');
const path = require('path');

// Ensure deterministic relative paths in bundles regardless of invocation directory
process.chdir(path.resolve(__dirname, '..'));

const isWatch = process.argv.includes('--watch');

const entryPoints = [
  { in: path.resolve(__dirname, 'src/app.ts'), out: 'app' },
  { in: path.resolve(__dirname, 'src/settings.ts'), out: 'settings' },
  { in: path.resolve(__dirname, 'src/logs.ts'), out: 'logs' },
];

const outdir = path.resolve(__dirname, '../internal/web/static');

/** @type {import('esbuild').BuildOptions} */
const buildOptions = {
  entryPoints,
  outdir,
  bundle: true,
  format: 'esm',
  target: 'es2022',
  // Source maps are a debug aid, not a production asset. Enable with SOURCEMAP=1.
  sourcemap: process.env.SOURCEMAP === '1',
  // Minify by default; use MINIFY=0 for a readable local build.
  minify: process.env.MINIFY !== '0',
  logLevel: 'info',
};

async function run() {
  if (isWatch) {
    const ctx = await esbuild.context(buildOptions);
    await ctx.watch();
    console.log('[esbuild] Watching web assets for changes...');
  } else {
    await esbuild.build(buildOptions);
    console.log('[esbuild] Successfully compiled TypeScript web assets to internal/web/static/');
  }
}

run().catch((err) => {
  console.error('[esbuild] Build failed:', err);
  process.exit(1);
});
