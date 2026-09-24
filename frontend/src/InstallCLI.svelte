<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from './api';
  import Icon from './Icon.svelte';

  let { email, onclose }: { email?: string; onclose: () => void } = $props();
  let dialog: HTMLDialogElement;
  let platforms = $state<string[]>([]);
  let loading = $state(true);
  let error = $state('');
  let copied = $state('');
  const quote = (value: string) => `'${value.replaceAll("'", "'\\''")}'`;
  const command = `curl -fsS ${quote(`${location.origin}/api/v1/cli/install.sh`)} | sh -s -- ${quote(location.origin)}`;
  let login = $derived(`tiki auth login --email ${quote(email || 'you@example.com')}`);
  const names: Record<string, string> = { 'linux-amd64': 'Linux Intel/AMD', 'linux-arm64': 'Linux ARM', 'darwin-amd64': 'macOS Intel', 'darwin-arm64': 'macOS Apple Silicon' };
  onMount(() => {
    dialog.showModal();
    void api<{ platforms: string[] }>('/cli').then(value => platforms = value.platforms)
      .catch(e => error = e instanceof Error ? e.message : 'Could not check CLI downloads.')
      .finally(() => loading = false);
  });
  async function copy(value: string, key: string) {
    try { await navigator.clipboard.writeText(value); copied = key; error = ''; }
    catch { error = 'Select the command below and copy it manually.'; }
  }
</script>

<dialog class="install-dialog" bind:this={dialog} aria-labelledby="install-heading" onclose={onclose}>
  <div class="detail-top"><h2 id="install-heading">Install the CLI</h2><button class="icon-button" aria-label="Close CLI setup" onclick={() => dialog.close()}><Icon name="close" size={16} /></button></div>
  <div class="install-body">
    <p>Run this command in your terminal to install Tiki and connect it to this workspace.</p>
    {#if error}<p class="error-banner" role="alert">{error}</p>{/if}
    {#if loading}<p class="hint" role="status">Checking available downloads…</p>
    {:else if platforms.length}
      <label>Install command<textarea readonly value={command} rows="3" onclick={event => event.currentTarget.select()}></textarea></label>
      <button class="primary-button" onclick={() => copy(command, 'install')}>{copied === 'install' ? 'Copied' : 'Copy install command'}</button>
      <p class="hint">Installs to ~/.local/bin without sudo. Follow the terminal's PATH instructions if needed. Supports {platforms.map(p => names[p] || p).join(', ')}.</p>
      <a href="/api/v1/cli/install.sh" target="_blank" rel="noopener noreferrer">View the install script</a>
      <label>Then sign in<input readonly value={login} onclick={event => event.currentTarget.select()} /></label>
      <button class="text-button" onclick={() => copy(login, 'login')}>{copied === 'login' ? 'Copied' : 'Copy sign-in command'}</button>
      <p class="hint">Use your Tiki password when prompted. Your browser session stays private.</p>
    {:else if !error}<p role="status">CLI downloads are not available on this server yet. Ask your administrator to enable them.</p>{/if}
  </div>
</dialog>

<style>
  .install-dialog { width: min(560px, calc(100vw - 24px)); max-height: calc(100dvh - 32px); overflow: auto; }
  .install-body { padding: 20px; display: grid; gap: 14px; }
  p { margin: 0; line-height: 1.5; }
  label { font-size: 12px; font-weight: 500; }
  textarea, input { display: block; width: 100%; margin-top: 6px; padding: 10px; background: var(--inset); border: 1px solid var(--line); border-radius: 6px; color: var(--text); font: 12px/1.5 var(--mono); }
  textarea { resize: vertical; }
  .primary-button { min-height: 36px; }
  a { color: var(--text-2); font-size: 12px; }
  .text-button { justify-self: start; }
</style>
