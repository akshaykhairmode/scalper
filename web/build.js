const esbuild = require('esbuild');
const path = require('path');

const isWatch = process.argv.includes('--watch');

const entryPoints = [
  { in: path.resolve(__dirname, 'src/app.ts'), out: 'app' },
  { in: path.resolve(__dirname, 'src/charts.ts'), out: 'charts' },
  { in: path.resolve(__dirname, 'src/patterns.ts'), out: 'patterns' },
  { in: path.resolve(__dirname, 'src/settings.ts'), out: 'settings' },
  { in: path.resolve(__dirname, 'src/logs.ts'), out: 'logs' },
  { in: path.resolve(__dirname, 'src/utils/qrcode.ts'), out: 'qrcode' },
];

const outdir = path.resolve(__dirname, '../internal/web/static');

/** @type {import('esbuild').BuildOptions} */
const buildOptions = {
  entryPoints,
  outdir,
  bundle: true,
  format: 'esm',
  target: 'es2022',
  sourcemap: true,
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
