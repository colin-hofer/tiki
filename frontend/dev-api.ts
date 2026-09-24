import { spawn, type ChildProcess } from 'node:child_process';
import { mkdir, rename } from 'node:fs/promises';
import { resolve } from 'node:path';
import type { Plugin, ViteDevServer } from 'vite';

// Reuse Vite's watcher and lifecycle instead of requiring a second watch tool.
export function devAPI(port: number): Plugin {
  let api: ChildProcess | undefined;
  let command: ChildProcess | undefined;
  let server: ViteDevServer;
  let closing = false;
  let timer: ReturnType<typeof setTimeout>;
  let work = Promise.resolve();

  async function stop(child?: ChildProcess) {
    if (!child?.pid || child.exitCode !== null || child.signalCode !== null) return;
    const exited = new Promise<void>(done => child.once('exit', () => done()));
    child.kill('SIGTERM');
    const timeout = setTimeout(() => child.kill('SIGKILL'), 6000);
    try { await exited; } finally { clearTimeout(timeout); }
  }

  function run(file: string, args: string[], cwd: string) {
    return new Promise<void>((done, fail) => {
      const child = command = spawn(file, args, { cwd, stdio: 'inherit' });
      child.once('error', fail);
      child.once('exit', code => {
        if (command === child) command = undefined;
        if (code === 0) done();
        else fail(new Error(`${file} exited with code ${code}`));
      });
    });
  }

  // Also cover Vite startup errors, which can exit before its close hooks run.
  function onExit() { api?.kill('SIGTERM'); command?.kill('SIGTERM'); }
  function onInterrupt() { void server.close().then(() => process.exit(130)); }

  return {
    name: 'tiki-api',
    apply: (_, env) => env.command === 'serve' && env.mode !== 'test' && !process.env.TIKI_API_URL,
    async configureServer(vite) {
      server = vite;
      const root = resolve(server.config.root, '..');
      const directory = resolve(root, '.dev');
      const binary = resolve(directory, 'tiki-api');
      const database = resolve(root, process.env.TIKI_DEV_DB || '.dev/tiki.db');
      if (!Number.isInteger(port) || port < 1 || port > 65535) throw new Error('TIKI_DEV_API_PORT must be between 1 and 65535');
      await mkdir(directory, { recursive: true, mode: 0o700 });
      process.once('exit', onExit);
      process.once('SIGINT', onInterrupt);

      async function restart(initialize = false) {
        if (closing) return;
        server.config.logger.info('[tiki] Building Go API…');
        await run('go', ['build', '-tags=dev', '-o', `${binary}.next`, '.'], root);
        if (closing) return;
        // Keep the working API alive when a new build has errors.
        const previous = api;
        api = undefined;
        await stop(previous);
        await rename(`${binary}.next`, binary);
        if (closing) return;
        if (initialize) {
          server.config.logger.info('[tiki] First run: choose a password for dev@tiki.local. Existing accounts are preserved.');
          await run(binary, ['init', '--if-needed', '--db', database, '--name', 'Developer', '--email', 'dev@tiki.local'], root);
        }
        if (closing) return;
        const child = api = spawn(binary, ['serve', '--db', database, '--listen', `127.0.0.1:${port}`], { cwd: root, stdio: ['ignore', 'inherit', 'inherit'] });
        const failed = (message: string) => {
          if (closing || api !== child) return;
          server.config.logger.error(`[tiki] ${message}`);
          void server.close().then(() => process.exit(1));
        };
        child.once('error', err => failed(err.message));
        child.once('exit', code => failed(`API exited (${code}); stopping development servers.`));
      }

      // Direct npm run dev gets the same installer downloads as make dev.
      work = run('sh', ['scripts/build-cli.sh'], root).then(() => restart(true));
      try { await work; } catch (err) { onExit(); throw err; }
      server.watcher.add(root);
      server.watcher.on('all', (event, file) => {
        if (closing || !['add', 'change', 'unlink'].includes(event) || !/(?:\.(?:go|sql|sh)|[/\\]go\.(?:mod|sum)|[/\\]skills[/\\]tiki[/\\]SKILL\.md)$/.test(file)) return;
        clearTimeout(timer);
        timer = setTimeout(() => {
          work = work.then(() => restart()).catch(err => {
            if (!closing) server.config.logger.error(`[tiki] ${err.message}. Fix the error and save to retry.`);
          });
        }, 150);
      });
    },
    async closeServer() {
      closing = true;
      clearTimeout(timer);
      process.off('SIGINT', onInterrupt);
      await Promise.all([stop(api), stop(command)]);
      await work.catch(() => {});
      process.off('exit', onExit);
    },
  };
}
