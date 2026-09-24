<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from './api';
  import Icon from './Icon.svelte';
  import Select from './Select.svelte';

  let { onclose }: { onclose: () => void } = $props();
  let dialog: HTMLDialogElement;
  let role = $state('member');
  let invite = $state<{ id: string; token: string; role: string; expires_at: number }>();
  let busy = $state(false);
  let error = $state('');
  let copied = $state(false);
  let link = $derived(invite ? `${location.origin}/#invite=${encodeURIComponent(invite.token)}` : '');
  onMount(() => { dialog.showModal(); });

  async function create(event: SubmitEvent) {
    event.preventDefault(); busy = true; error = ''; copied = false;
    try { invite = await api('/invites', 'POST', { role }); }
    catch (e) { error = e instanceof Error ? e.message : 'Could not create invite.'; }
    finally { busy = false; }
  }
  async function copy() {
    try { await navigator.clipboard.writeText(link); copied = true; }
    catch { error = 'Select and copy the link from the field below.'; }
  }
  async function revoke() {
    if (!invite) return;
    busy = true; error = '';
    try { await api(`/invites/${invite.id}`, 'DELETE'); invite = undefined; }
    catch (e) { error = e instanceof Error ? e.message : 'Could not revoke invite.'; }
    finally { busy = false; }
  }
</script>

<dialog class="invite-dialog" bind:this={dialog} aria-labelledby="invite-heading" onclose={onclose}>
  <div class="detail-top"><h2 id="invite-heading">Invite people</h2><button class="icon-button" aria-label="Close invite" onclick={() => dialog.close()}><Icon name="close" size={16} /></button></div>
  <div class="invite-body">
    <p>Create a single-use link. They choose their own name, email, and password.</p>
    {#if error}<p class="error-banner" role="alert">{error}</p>{/if}
    {#if invite}
      <label>Invite link<input readonly value={link} onclick={event => event.currentTarget.select()} /></label>
      <p class="hint">{invite.role} access · Expires {new Date(invite.expires_at * 1000).toLocaleString()}. Share privately with the person you want to invite.</p>
      <div class="button-row"><button class="primary-button" onclick={copy} disabled={busy}>{copied ? 'Copied' : 'Copy link'}</button><button class="text-button" onclick={revoke} disabled={busy}>Revoke link</button></div>
    {:else}
      <form onsubmit={create}>
        <div class="field"><span class="field-label">Role</span><Select label="Role" variant="field" bind:value={role} disabled={busy} options={[
          { value: 'member', label: 'Member', icon: 'person', description: 'Create and edit tickets' },
          { value: 'viewer', label: 'Viewer', icon: 'eye', description: 'Read only' },
          { value: 'admin', label: 'Admin', icon: 'shield', description: 'Manage users and invites' },
        ]} /></div>
        <p class="hint">The link expires in 7 days and can be claimed once.</p>
        <button class="primary-button" disabled={busy}>{busy ? 'Creating…' : 'Create invite link'}</button>
      </form>
    {/if}
  </div>
</dialog>

<style>
  .invite-dialog { width: min(440px, calc(100vw - 24px)); max-height: calc(100dvh - 32px); overflow: auto; }
  .invite-body { padding: 20px; display: grid; gap: 16px; }
  p { margin: 0; line-height: 1.5; }
  form { display: grid; gap: 16px; }
  label { font-size: 12px; font-weight: 500; }
  .field { display: grid; gap: 6px; }
  .field-label { font-size: 12px; font-weight: 500; }
  input { display: block; width: 100%; min-height: 36px; margin-top: 6px; padding: 8px; border: 1px solid var(--line); border-radius: 6px; background: var(--inset); color: var(--text); font: inherit; }
  .primary-button { min-height: 36px; padding: 8px 14px; }
  .button-row { display: flex; gap: 16px; }
</style>
