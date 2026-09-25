import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, readdirSync, readFileSync, writeFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

process.chdir(resolve(dirname(fileURLToPath(import.meta.url)), '..'));

// Hash paths as well as contents: deletions and restored mtimes must invalidate
// the build. Checking outputs also catches a removed dist or a direct Vite build.
function digest(paths) {
  const hash = createHash('sha256');
  function visit(path) {
    hash.update(path + '\0');
    if (!existsSync(path)) return;
    for (const entry of readdirSync(path, { withFileTypes: true }).sort((a, b) =>
      a.name.localeCompare(b.name),
    )) {
      const name = `${path}/${entry.name}`;
      if (entry.isDirectory()) visit(name);
      else hash.update(name + '\0').update(readFileSync(name));
    }
  }
  for (const path of paths) visit(path);
  return hash;
}

const inputs = digest(['frontend/src', 'frontend/public', 'frontend/tests']);
for (const name of readdirSync('frontend').sort()) {
  if (/\.(?:json|[cm]?js|ts|html|css)$/.test(name) || name === '.env' || name.startsWith('.env.'))
    inputs.update(name + '\0').update(readFileSync(`frontend/${name}`));
}
inputs.update(readFileSync(fileURLToPath(import.meta.url))).update(process.version);
for (const key of Object.keys(process.env).sort()) {
  if (key.startsWith('VITE_') || key === 'NODE_ENV') inputs.update(`${key}=${process.env[key]}\0`);
}
const key = inputs.digest('hex');
const stamp = '.dev/frontend-build.json';
const state = () => JSON.stringify([key, digest(['frontend/dist']).digest('hex')]);
if (
  existsSync('frontend/dist/index.html') &&
  existsSync(stamp) &&
  readFileSync(stamp, 'utf8') === state()
) {
  console.log('Frontend unchanged.');
} else {
  const result = spawnSync('npm', ['--prefix', 'frontend', 'run', 'build'], { stdio: 'inherit' });
  if (result.error) throw result.error;
  if (result.status !== 0) process.exit(result.status ?? 1);
  mkdirSync('.dev', { recursive: true });
  writeFileSync(stamp, state());
}
