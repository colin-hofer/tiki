<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { api, initials, avatarHue } from './api';
  import type { User } from './api';
  import Icon from './Icon.svelte';

  type View = 'menu' | 'profile' | 'password';
  let {
    user,
    initialView = 'menu',
    onchange,
    onclose,
    onlogout,
    beforePasswordChange,
    onpasswordchanged,
  }: {
    user: User;
    initialView?: View;
    onchange: (user: User) => void;
    onclose: () => void;
    onlogout: () => void;
    beforePasswordChange: () => Promise<boolean>;
    onpasswordchanged: () => void;
  } = $props();
  let dialog: HTMLDialogElement;
  let view = $state<View>('menu');
  let name = $state('');
  let currentPassword = $state('');
  let newPassword = $state('');
  let confirmation = $state('');
  let busy = $state(false);
  let error = $state('');
  let notice = $state('');
  let heading = $derived(
    view === 'profile' ? 'Edit profile' : view === 'password' ? 'Change password' : 'Your account',
  );

  onMount(() => {
    dialog.showModal();
    void show(initialView);
  });
  async function show(next: View) {
    if (busy) return;
    view = next;
    name = user.name;
    error = '';
    notice = '';
    currentPassword = '';
    newPassword = '';
    confirmation = '';
    await tick();
    dialog
      .querySelector<HTMLElement>('form input:not([type="hidden"]), .menu-items button')
      ?.focus();
  }
  function close() {
    if (!busy) dialog.close();
  }
  function keydown(event: KeyboardEvent) {
    if (view !== 'menu' || event.ctrlKey || event.metaKey || event.altKey || event.isComposing)
      return;
    const controls = [...dialog.querySelectorAll<HTMLButtonElement>('.menu-items button')];
    const index = controls.indexOf(event.target as HTMLButtonElement);
    const down = ['ArrowDown', 'j'].includes(event.key),
      up = ['ArrowUp', 'k'].includes(event.key);
    if (down || up || event.key === 'Home' || event.key === 'End') {
      event.preventDefault();
      const next =
        event.key === 'Home'
          ? 0
          : event.key === 'End'
            ? controls.length - 1
            : (index + (down ? 1 : -1) + controls.length) % controls.length;
      controls[next]?.focus();
    }
  }
  async function saveName(event: SubmitEvent) {
    event.preventDefault();
    if (busy || !name.trim()) return;
    busy = true;
    error = '';
    notice = '';
    try {
      const updated = await api<User>('/auth/me', 'PATCH', { name: name.trim() });
      onchange(updated);
      name = updated.name;
      notice = 'Name updated.';
    } catch (e) {
      error = e instanceof Error ? e.message : 'Could not update your name.';
    } finally {
      busy = false;
    }
  }
  async function changePassword(event: SubmitEvent) {
    event.preventDefault();
    if (busy) return;
    error = '';
    notice = '';
    if (newPassword !== confirmation) {
      error = 'New passwords do not match.';
      return;
    }
    if ([...newPassword].length < 8 || new TextEncoder().encode(newPassword).length > 1024) {
      error = 'Use at least 8 characters and no more than 1024 bytes.';
      return;
    }
    busy = true;
    try {
      if (!(await beforePasswordChange())) {
        error =
          'Your ticket draft could not be saved. Close account settings and finish or resolve the draft first.';
        return;
      }
      await api('/auth/password', 'POST', {
        current_password: currentPassword,
        new_password: newPassword,
      });
      currentPassword = '';
      newPassword = '';
      confirmation = '';
      onpasswordchanged();
    } catch (e) {
      error = e instanceof Error ? e.message : 'Could not change your password.';
    } finally {
      busy = false;
    }
  }
</script>

<!-- Native dialog supplies focus trapping and Escape; the menu also supports arrows and j/k. -->
<!-- svelte-ignore a11y_no_noninteractive_element_interactions, a11y_click_events_have_key_events -->
<dialog
  class={view === 'menu' ? 'dlg menu-dlg' : 'dlg menu-dlg settings'}
  bind:this={dialog}
  aria-labelledby="account-heading"
  onkeydown={keydown}
  {onclose}
  oncancel={(event) => {
    event.preventDefault();
    close();
  }}
  onclick={(event) => {
    if (event.target === dialog) close();
  }}
>
  {#if view === 'menu'}
    <h2 id="account-heading" class="sr-only">{heading}</h2>
    <div class="menu-identity">
      <span class="mini-avatar" style:--hue={avatarHue(user.id)} aria-hidden="true"
        >{initials(user.name)}</span
      >
      <div><strong>{user.name}</strong><span>{user.email}</span></div>
      <span class="meta role">{user.role}</span>
    </div>
    <div class="menu-items">
      <button onclick={() => show('profile')}
        ><Icon name="person" size={15} /><span>Edit profile</span></button
      >
      <button onclick={() => show('password')}
        ><Icon name="key" size={15} /><span>Change password</span></button
      >
      <button onclick={onlogout}><Icon name="logout" size={15} /><span>Sign out</span></button>
    </div>
  {:else}
    <header class="dlg-head">
      <button
        class="icon-button dlg-back"
        aria-label="Back to account menu"
        disabled={busy}
        onclick={() => show('menu')}><Icon name="back" size={16} /></button
      >
      <h2 id="account-heading">{heading}</h2>
      <button class="icon-button" aria-label="Close account" disabled={busy} onclick={close}
        ><Icon name="close" size={16} /></button
      >
    </header>
    {#if view === 'profile'}
      <form onsubmit={saveName}>
        <div class="dlg-body">
          {#if error}<p class="error-banner" role="alert">{error}</p>{/if}
          <label class="field"
            >Name<input
              name="name"
              autocomplete="name"
              required
              maxlength="200"
              bind:value={name}
              disabled={busy}
            /></label
          >
          <div class="field"><span>Email</span><span class="meta">{user.email}</span></div>
        </div>
        <footer class="dlg-foot">
          {#if notice}<span class="dlg-status ok" role="status">{notice}</span>{/if}
          <div class="actions">
            <button
              class="primary-button"
              disabled={busy || !name.trim() || name.trim() === user.name}
              >{busy ? 'Saving…' : 'Save name'}</button
            >
          </div>
        </footer>
      </form>
    {:else}
      <form onsubmit={changePassword}>
        <div class="dlg-body">
          {#if error}<p class="error-banner" role="alert">{error}</p>{/if}
          {#if notice}<p class="dlg-status ok" role="status">{notice}</p>{/if}
          <input type="hidden" name="username" autocomplete="username" value={user.email || ''} />
          <label class="field"
            >Current password<input
              type="password"
              name="current-password"
              autocomplete="current-password"
              required
              maxlength="1024"
              bind:value={currentPassword}
              disabled={busy}
            /></label
          >
          <label class="field"
            >New password<input
              type="password"
              name="new-password"
              autocomplete="new-password"
              required
              minlength="8"
              maxlength="1024"
              aria-describedby="password-hint"
              bind:value={newPassword}
              disabled={busy}
            /></label
          >
          <label class="field"
            >Confirm new password<input
              type="password"
              name="confirm-password"
              autocomplete="new-password"
              required
              minlength="8"
              maxlength="1024"
              bind:value={confirmation}
              disabled={busy}
            /></label
          >
        </div>
        <footer class="dlg-foot">
          <span class="note" id="password-hint">8+ characters · signs out all devices</span>
          <div class="actions">
            <button
              class="primary-button"
              disabled={busy || !currentPassword || !newPassword || !confirmation}
              >{busy ? 'Changing password…' : 'Change password'}</button
            >
          </div>
        </footer>
      </form>
    {/if}
  {/if}
</dialog>

<style>
  .settings {
    width: min(380px, calc(100vw - 24px));
  }
  .role {
    margin-left: auto;
    align-self: start;
    text-transform: lowercase;
  }
  .sr-only {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip-path: inset(50%);
    white-space: nowrap;
  }
</style>
