<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { api } from './api';
  import type { User } from './api';
  import Icon from './Icon.svelte';

  type View = 'menu' | 'profile' | 'password';
  let { user, initialView = 'menu', onchange, onclose, onlogout, beforePasswordChange, onpasswordchanged }:
    { user: User; initialView?: View; onchange: (user: User) => void; onclose: () => void;
      onlogout: () => void; beforePasswordChange: () => Promise<boolean>; onpasswordchanged: () => void } = $props();
  let dialog: HTMLDialogElement;
  let view = $state<View>('menu');
  let name = $state('');
  let currentPassword = $state('');
  let newPassword = $state('');
  let confirmation = $state('');
  let busy = $state(false);
  let error = $state('');
  let notice = $state('');
  let heading = $derived(view === 'profile' ? 'Edit profile' : view === 'password' ? 'Change password' : 'Your account');

  onMount(() => { dialog.showModal(); void show(initialView); });
  async function show(next: View) {
    if (busy) return;
    view = next; name = user.name; error = ''; notice = '';
    currentPassword = ''; newPassword = ''; confirmation = '';
    await tick();
    dialog.querySelector<HTMLElement>('form input:not([type="hidden"]), .account-actions button')?.focus();
  }
  function close() { if (!busy) dialog.close(); }
  function keydown(event: KeyboardEvent) {
    if (view !== 'menu' || event.ctrlKey || event.metaKey || event.altKey || event.isComposing) return;
    const controls = [...dialog.querySelectorAll<HTMLButtonElement>('.account-actions button')];
    const index = controls.indexOf(event.target as HTMLButtonElement);
    const down = ['ArrowDown', 'j'].includes(event.key), up = ['ArrowUp', 'k'].includes(event.key);
    if (down || up || event.key === 'Home' || event.key === 'End') {
      event.preventDefault();
      const next = event.key === 'Home' ? 0 : event.key === 'End' ? controls.length - 1 : (index + (down ? 1 : -1) + controls.length) % controls.length;
      controls[next]?.focus();
    }
  }
  async function saveName(event: SubmitEvent) {
    event.preventDefault();
    if (busy || !name.trim()) return;
    busy = true; error = ''; notice = '';
    try {
      const updated = await api<User>('/auth/me', 'PATCH', { name: name.trim() });
      onchange(updated); name = updated.name; notice = 'Name updated.';
    } catch (e) { error = e instanceof Error ? e.message : 'Could not update your name.'; }
    finally { busy = false; }
  }
  async function changePassword(event: SubmitEvent) {
    event.preventDefault();
    if (busy) return;
    error = ''; notice = '';
    if (newPassword !== confirmation) { error = 'New passwords do not match.'; return; }
    if ([...newPassword].length < 8 || new TextEncoder().encode(newPassword).length > 1024) {
      error = 'Use at least 8 characters and no more than 1024 bytes.'; return;
    }
    busy = true;
    try {
      if (!(await beforePasswordChange())) {
        error = 'Your ticket draft could not be saved. Close account settings and finish or resolve the draft first.'; return;
      }
      await api('/auth/password', 'POST', { current_password: currentPassword, new_password: newPassword });
      currentPassword = ''; newPassword = ''; confirmation = '';
      onpasswordchanged();
    } catch (e) { error = e instanceof Error ? e.message : 'Could not change your password.'; }
    finally { busy = false; }
  }
</script>

<!-- Native dialog supplies focus trapping and Escape; the menu also supports arrows and j/k. -->
<!-- svelte-ignore a11y_no_noninteractive_element_interactions, a11y_click_events_have_key_events -->
<dialog class="account-dialog" class:settings={view !== 'menu'} bind:this={dialog} aria-labelledby="account-heading" onkeydown={keydown} onclose={onclose} oncancel={event => { event.preventDefault(); close(); }} onclick={event => { if (event.target === dialog) close(); }}>
  <div class="detail-top">
    <div class="button-row">{#if view !== 'menu'}<button class="icon-button" aria-label="Back to account menu" disabled={busy} onclick={() => show('menu')}><Icon name="back" /></button>{/if}<h2 id="account-heading">{heading}</h2></div>
    <button class="icon-button" aria-label="Close account" disabled={busy} onclick={close}><Icon name="close" /></button>
  </div>
  {#if view === 'menu'}
    <div class="account-identity"><strong>{user.name}</strong><span>{user.email}</span></div>
    <div class="account-actions">
      <button onclick={() => show('profile')}><Icon name="person" /><span>Edit profile</span><Icon name="arrow" size={14} /></button>
      <button onclick={() => show('password')}><Icon name="key" /><span>Change password</span><Icon name="arrow" size={14} /></button>
      <button onclick={onlogout}><Icon name="logout" /><span>Sign out</span></button>
    </div>
  {:else}
    <div class="account-body">
      {#if error}<p class="error-banner" role="alert">{error}</p>{/if}
      {#if notice}<p class="account-notice" role="status">{notice}</p>{/if}
      {#if view === 'profile'}
        <form onsubmit={saveName}>
          <label>Name<input name="name" autocomplete="name" required maxlength="200" bind:value={name} disabled={busy} /></label>
          <div class="account-email"><span>Email</span><span>{user.email}</span></div>
          <button class="primary-button" disabled={busy || !name.trim() || name.trim() === user.name}>{busy ? 'Saving…' : 'Save name'}</button>
        </form>
      {:else}
        <p>Changing your password signs you out on all devices. Sign in again with your new password.</p>
        <form onsubmit={changePassword}>
          <input type="hidden" name="username" autocomplete="username" value={user.email || ''} />
          <label>Current password<input type="password" name="current-password" autocomplete="current-password" required maxlength="1024" bind:value={currentPassword} disabled={busy} /></label>
          <label>New password<input type="password" name="new-password" autocomplete="new-password" required minlength="8" maxlength="1024" aria-describedby="password-hint" bind:value={newPassword} disabled={busy} /></label>
          <p class="hint" id="password-hint">At least 8 characters.</p>
          <label>Confirm new password<input type="password" name="confirm-password" autocomplete="new-password" required minlength="8" maxlength="1024" bind:value={confirmation} disabled={busy} /></label>
          <button class="primary-button" disabled={busy || !currentPassword || !newPassword || !confirmation}>{busy ? 'Changing password…' : 'Change password'}</button>
        </form>
      {/if}
    </div>
  {/if}
</dialog>

<style>
  .account-dialog { width: min(300px, calc(100vw - 24px)); max-height: calc(100dvh - 76px); margin: 56px 12px 0 auto; overflow: auto; }
  .account-dialog::backdrop { background: #00000022; }
  .settings { width: min(400px, calc(100vw - 24px)); }
  .account-identity { display: grid; gap: 4px; padding: 12px 20px 16px; overflow-wrap: anywhere; }
  .account-identity span, .account-email { color: var(--text-2); font-size: 12px; }
  .account-actions { padding: 8px; border-top: 1px solid var(--line); }
  .account-actions button { width: 100%; display: flex; align-items: center; gap: 12px; padding: 12px; border: 0; border-radius: 6px; background: transparent; text-align: left; }
  .account-actions button:hover, .account-actions button:focus-visible { background: var(--hover); }
  .account-actions span { flex: 1; }
  .account-body { padding: 12px 20px 20px; display: grid; gap: 16px; }
  form { display: grid; gap: 16px; }
  label, .account-email { display: grid; gap: 6px; font-size: 12px; }
  input:not([type="hidden"]) { display: block; width: 100%; min-height: 38px; padding: 8px 10px; background: var(--inset); border: 1px solid var(--line-strong); border-radius: 6px; }
  .account-email { overflow-wrap: anywhere; }
  .primary-button { min-height: 38px; }
  p { font-size: 12px; color: var(--text-2); line-height: 1.6; }
  .account-notice { color: var(--green); }
</style>
