<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from './api';
  import Icon from './Icon.svelte';

  let { email, onclose }: { email?: string; onclose: () => void } = $props();
  let dialog: HTMLDialogElement;
  let tab = $state<'cli' | 'skill'>('cli');
  let platforms = $state<string[]>([]);
  let loading = $state(true);
  let downloadError = $state('');
  let copyError = $state('');
  let copied = $state('');
  const quote = (value: string) => `'${value.replaceAll("'", "'\\''")}'`;
  const command = `curl -fsS ${quote(`${location.origin}/api/v1/cli/install.sh`)} | sh -s -- ${quote(location.origin)}`;
  const skillCommand = `${command} --skill`;
  let login = $derived(
    `tiki auth login --server ${quote(location.origin)} --email ${quote(email || 'you@example.com')}`,
  );
  const names: Record<string, string> = {
    'linux-amd64': 'Linux Intel/AMD',
    'linux-arm64': 'Linux ARM',
    'darwin-amd64': 'macOS Intel',
    'darwin-arm64': 'macOS Apple Silicon',
  };
  onMount(() => {
    dialog.showModal();
    void api<{ platforms: string[] }>('/cli')
      .then((value) => (platforms = value.platforms))
      .catch(
        (e) => (downloadError = e instanceof Error ? e.message : 'Could not check CLI downloads.'),
      )
      .finally(() => (loading = false));
  });
  function selectTab(value: typeof tab) {
    tab = value;
    copied = '';
    copyError = '';
    dialog.querySelector<HTMLButtonElement>(`#setup-${tab}-tab`)?.focus();
  }
  function tabKey(event: KeyboardEvent) {
    if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
    event.preventDefault();
    selectTab(
      event.key === 'Home'
        ? 'cli'
        : event.key === 'End'
          ? 'skill'
          : tab === 'cli'
            ? 'skill'
            : 'cli',
    );
  }
  async function copy(value: string, key: string) {
    try {
      await navigator.clipboard.writeText(value);
      copied = key;
      copyError = '';
    } catch {
      copied = '';
      copyError = 'Clipboard unavailable. Select the command and copy it manually.';
    }
  }
</script>

{#snippet commandBox(value: string, key: string, label: string, rows: number)}
  <div class="command-box">
    <textarea
      aria-label={label}
      readonly
      {rows}
      {value}
      spellcheck="false"
      onclick={(event) => event.currentTarget.select()}></textarea>
    <button
      class="icon-button copy-button"
      aria-label={`Copy ${label}`}
      title={copied === key ? 'Copied' : 'Copy'}
      onclick={() => copy(value, key)}
      ><Icon name={copied === key ? 'check' : 'copy'} size={14} /></button
    >
  </div>
{/snippet}

<dialog class="dlg setup-dialog" bind:this={dialog} aria-labelledby="setup-heading" {onclose}>
  <header class="dlg-head">
    <h2 id="setup-heading">CLI & agents</h2>
    <div class="seg setup-tabs" role="tablist" aria-label="Setup options">
      <button
        id="setup-cli-tab"
        role="tab"
        aria-selected={tab === 'cli'}
        aria-controls="setup-cli-panel"
        tabindex={tab === 'cli' ? 0 : -1}
        onclick={() => selectTab('cli')}
        onkeydown={tabKey}><Icon name="terminal" size={14} />CLI</button
      >
      <button
        id="setup-skill-tab"
        role="tab"
        aria-selected={tab === 'skill'}
        aria-controls="setup-skill-panel"
        tabindex={tab === 'skill' ? 0 : -1}
        onclick={() => selectTab('skill')}
        onkeydown={tabKey}><Icon name="feature" size={14} />Agent skill</button
      >
    </div>
    <button class="icon-button" aria-label="Close setup" onclick={() => dialog.close()}
      ><Icon name="close" size={16} /></button
    >
  </header>
  <div class="dlg-body setup-body">
    {#if copyError}<p class="error-banner" role="alert">{copyError}</p>{/if}
    <span class="sr-only" role="status">{copied ? 'Command copied to clipboard.' : ''}</span>

    <!-- Both panels share one grid cell so switching tabs never resizes the dialog. -->
    <div class="panels">
      <div
        id="setup-cli-panel"
        role="tabpanel"
        aria-labelledby="setup-cli-tab"
        tabindex="0"
        hidden={tab !== 'cli'}
      >
        {#if loading}<p class="note" role="status">Checking downloads…</p>
        {:else if downloadError}<p class="error-banner" role="alert">{downloadError}</p>
        {:else if platforms.length}
          <h3><span class="step">1</span>Install</h3>
          {@render commandBox(command, 'cli', 'CLI install command', 2)}
          <p class="note">
            To <code>~/.local/bin</code> · {platforms.map((p) => names[p] || p).join(', ')}
          </p>
          <h3><span class="step">2</span>Sign in</h3>
          {@render commandBox(login, 'login', 'Sign-in command', 2)}
        {:else}<p class="unavailable" role="status">
            <Icon name="info" size={14} />CLI downloads are not available on this server yet.
          </p>{/if}
      </div>

      <div
        id="setup-skill-panel"
        role="tabpanel"
        aria-labelledby="setup-skill-tab"
        tabindex="0"
        hidden={tab !== 'skill'}
      >
        <h3>Install the <code>$tiki</code> skill for Codex</h3>
        {@render commandBox(skillCommand, 'skill', 'Skill install command', 2)}
        <p class="note">
          To <code>~/.codex/skills/tiki</code> · run again to update · uses your CLI sign-in
        </p>
        <p class="example">Then ask: “Use <code>$tiki</code> to find my todo tickets.”</p>
        <p class="links">
          <button class="text-button" onclick={() => selectTab('cli')}>Set up CLI</button><a
            href="/api/v1/skills/tiki/SKILL.md"
            target="_blank"
            rel="noopener noreferrer">Read SKILL.md</a
          >
        </p>
      </div>
    </div>
  </div>
  <footer class="dlg-foot">
    <span class="meta workspace" title={location.origin}>{location.host}</span><a
      class="actions"
      href="/api/v1/cli/install.sh"
      target="_blank"
      rel="noopener noreferrer">View install script</a
    >
  </footer>
</dialog>

<style>
  .setup-dialog {
    width: min(520px, calc(100vw - 24px));
  }
  .setup-body {
    display: block;
  }
  h2 {
    margin-right: 4px !important;
  }
  .setup-tabs {
    margin-right: auto;
  }
  .panels {
    display: grid;
  }
  .panels > [role='tabpanel'] {
    grid-area: 1 / 1;
    min-width: 0;
  }
  [role='tabpanel'][hidden] {
    display: block;
    visibility: hidden;
  }
  [role='tabpanel']:focus-visible {
    outline-offset: 4px;
    border-radius: 4px;
  }
  h3 {
    display: flex;
    align-items: center;
    gap: 8px;
    margin: 0 0 6px;
    font-size: 12px;
    font-weight: 500;
    color: var(--text-2);
  }
  h3:not(:first-child) {
    margin-top: 14px;
  }
  h3:has(code) {
    display: block;
  }
  .step {
    display: inline-grid;
    place-items: center;
    width: 16px;
    height: 16px;
    border-radius: 50%;
    background: var(--chip);
    color: var(--text-3);
    font: 10px var(--mono);
  }
  .note {
    margin-top: 6px;
  }
  .example {
    margin-top: 12px;
  }
  .unavailable {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .links {
    display: flex;
    align-items: center;
    gap: 4px;
    margin: 10px 0 0 -8px;
  }
  a,
  .links .text-button {
    font-size: 11px;
    color: var(--text-3);
    text-decoration: none;
  }
  .links a {
    padding: 0 8px;
  }
  a:hover {
    color: var(--text);
    text-decoration: underline;
  }
  .workspace {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 10px;
  }
  .error-banner {
    margin: 0 0 12px !important;
  }
  .sr-only {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip-path: inset(50%);
    white-space: nowrap;
  }
  @media (max-width: 480px) {
    .dlg-head {
      flex-wrap: wrap;
    }
    .setup-tabs {
      order: 3;
      flex-basis: 100%;
      margin: 0 6px 0 0;
    }
    .setup-tabs button {
      flex: 1;
      justify-content: center;
      height: 32px;
    }
    h2 {
      flex: 1;
    }
  }
</style>
