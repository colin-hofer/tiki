import { spawn, spawnSync } from 'node:child_process';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

// Playwright owns this server. It never reads the developer's database or login.
const directory = mkdtempSync(join(tmpdir(), 'tiki-browser-'));
const binary = join(directory, 'tiki');
const database = join(directory, 'test.db');
const root = resolve('..');
let server;
let timeout;
let stopping = false;
function cleanup() {
  rmSync(directory, { recursive: true, force: true });
}
function stop() {
  stopping = true;
  if (!server) return;
  server.kill('SIGTERM');
  timeout = setTimeout(() => server.kill('SIGKILL'), 6000);
}
process.once('exit', cleanup);
process.once('SIGINT', stop);
process.once('SIGTERM', stop);
function run(file, args, input) {
  const result = spawnSync(file, args, {
    cwd: root,
    input,
    encoding: 'utf8',
    stdio: ['pipe', 'inherit', 'inherit'],
  });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${file} exited with ${result.status}`);
}
run('go', ['build', '-tags=dev', '-o', binary, '.']);
run(
  binary,
  [
    'init',
    '--db',
    database,
    '--name',
    'Browser Test',
    '--email',
    'browser@example.test',
    '--password-stdin',
  ],
  'browser-test-password\n',
);
if (!stopping) {
  server = spawn(
    binary,
    ['serve', '--db', database, '--listen', `127.0.0.1:${process.env.TIKI_TEST_API_PORT}`],
    { cwd: root, stdio: 'inherit' },
  );
  server.once('error', (error) => {
    console.error(error);
    process.exitCode = 1;
  });
  server.once('exit', (code) => {
    clearTimeout(timeout);
    process.exitCode = stopping ? 0 : code || 1;
  });
}
