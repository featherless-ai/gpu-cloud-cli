/**
 * Cross-compiles the release binaries. Bun-only; never run by CI.
 * Layout served relative to FEATHERLESS_RELEASES_BASE_URL:
 *   GET /latest.json
 *   GET /<version>/featherless-cloud-<platform>
 */
import { $ } from 'bun';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const TARGETS = ['darwin-arm64', 'darwin-x64', 'linux-x64', 'linux-arm64'];

const pkg = await Bun.file(path.join(import.meta.dir, '..', 'package.json')).json();
const version = pkg.version as string;

// The version literal in src/version.ts is the single source of truth at
// runtime; keep it synced with the manifest.
const versionFile = path.join(import.meta.dir, '..', 'src', 'version.ts');
const versionSrc = await Bun.file(versionFile).text();

if (!versionSrc.includes(`export const VERSION = '${version}';`)) {
  console.error(`src/version.ts VERSION does not match package.json version ${version}. Update it before releasing.`);
  process.exit(1);
}

const outDir = path.join('dist', 'release', version);
await $`mkdir -p ${outDir}`.cwd(path.join(import.meta.dir, '..'));

const assets: Record<string, string> = {};

for (const target of TARGETS) {
  const asset = `featherless-cloud-${target}`;
  const outfile = path.join(outDir, asset);

  await $`bun build src/index.ts --compile --target=bun-${target} --define __BINARY__=true --outfile ${outfile}`
    .cwd(path.join(import.meta.dir, '..'));

  assets[target] = asset;
}

const latest = { version, assets };
await Bun.write(path.join('dist', 'release', 'latest.json'), JSON.stringify(latest, null, 2) + '\n');

console.log(`Published layout under dist/release/ for ${version}:`);

for (const target of TARGETS) {
  console.log(`  /${version}/${assets[target]}`);
}

console.log('  /latest.json');
