<script lang="ts">
  import { onMount } from 'svelte';
  import { api, initials, avatarHue } from './api';
  import type { User } from './api';
  import Icon from './Icon.svelte';
  import Select from './Select.svelte';

  let { users, currentUserId, onchange, onclose }: { users: User[]; currentUserId: string; onchange: (user: User) => void; onclose: () => void } = $props();
  let dialog: HTMLDialogElement;
  let role = $state('member');
  let invite = $state<{ id: string; token: string; role: string; expires_at: number }>();
  let busy = $state(false);
  let error = $state('');
  let copied = $state(false);
  let inviting = $state(false);
  let search = $state('');
  let showRemoved = $state(false);
  let removing = $state<User>();
  let notice = $state('');
  let roleDraft = $state<Record<string, string>>({});
  let admins = $derived(users.filter(u => !u.removed_at && u.role === 'admin').length);
  let visible = $derived(users.filter(u => (showRemoved || !u.removed_at) && `${u.name} ${u.email || ''}`.toLowerCase().includes(search.trim().toLowerCase())));
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
  async function change(user: User, role?: string) {
    busy = true; error = ''; notice = '';
    if (role) roleDraft[user.id] = role;
    try {
      const updated = await api<User>(`/users/${user.id}`, role ? 'PATCH' : 'DELETE', role ? { role } : undefined);
      removing = undefined; onchange(updated);
      notice = role ? `${user.name} now has ${role} access.` : `${user.name}'s access was removed.`;
    } catch (e) { error = e instanceof Error ? e.message : 'Could not update this user.'; }
    finally { delete roleDraft[user.id]; busy = false; }
  }
</script>

<dialog class="dlg wide" bind:this={dialog} aria-labelledby="people-heading" onclose={onclose}>
  <header class="dlg-head">
    <h2 id="people-heading">People</h2>
    <button class={inviting ? 'small-button' : 'primary-button'} onclick={() => inviting = !inviting} aria-expanded={inviting}><Icon name="add-person" size={14} />Invite people</button>
    <button class="icon-button" aria-label="Close people" onclick={() => dialog.close()}><Icon name="close" size={16} /></button>
  </header>
  <div class="dlg-body">
    {#if inviting}
      <div class="dlg-callout">
        {#if invite}
          <div class="command-box"><input aria-label="Invite link" readonly value={link} onclick={event => event.currentTarget.select()} /><button class="icon-button copy-button" aria-label={copied ? 'Copied' : 'Copy link'} title={copied ? 'Copied' : 'Copy link'} onclick={copy} disabled={busy}><Icon name={copied ? 'check' : 'copy'} size={14} /></button></div>
          <div class="invite-row"><span class="note">{invite.role} · single-use · expires {new Date(invite.expires_at * 1000).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })}</span><button class="text-button danger" onclick={revoke} disabled={busy}>Revoke link</button></div>
        {:else}
          <form class="invite-row" onsubmit={create}>
            <Select label="Invite role" variant="field" bind:value={role} disabled={busy} options={[
              { value: 'member', label: 'Member', icon: 'person', description: 'Create and edit tickets' },
              { value: 'viewer', label: 'Viewer', icon: 'eye', description: 'Read only' },
              { value: 'admin', label: 'Admin', icon: 'shield', description: 'Manage users and invites' },
            ]} />
            <button class="primary-button" disabled={busy}>{busy ? 'Creating…' : 'Create invite link'}</button>
          </form>
          <span class="note">Single-use link, valid 7 days. They set their own name and password.</span>
        {/if}
      </div>
    {/if}
    <label class="dlg-search"><Icon name="search" size={14} /><input aria-label="Search people" placeholder="Filter by name or email…" bind:value={search} /></label>
    {#if error}<p class="error-banner" role="alert">{error}</p>{/if}
    {#if notice}<p class="dlg-status" role="status">{notice}</p>{/if}
    <ul class="dlg-list" aria-label="Workspace users">
      {#each visible as person (person.id)}
        {@const lastAdmin = !person.removed_at && person.role === 'admin' && admins === 1}
        <li class:removed={Boolean(person.removed_at)}>
          <span class="mini-avatar" style:--hue={avatarHue(person.id)} aria-hidden="true">{initials(person.name)}</span>
          <div class="row-main"><strong>{person.name}{person.id === currentUserId ? ' (you)' : ''}</strong><span class="meta">{person.email}</span></div>
          {#if person.removed_at}<span class="note">Removed · Invite again to restore</span>
          {:else}
            <Select label={`Role for ${person.name}`} title={lastAdmin ? 'Last admin — promote someone else first' : ''} value={roleDraft[person.id] || person.role} disabled={busy || lastAdmin} onchange={value => change(person, value)} options={[
              { value: 'member', label: 'Member', icon: 'person' }, { value: 'viewer', label: 'Viewer', icon: 'eye' }, { value: 'admin', label: 'Admin', icon: 'shield' },
            ]} />
            <button class="icon-button danger" aria-label={`Remove ${person.name}`} title={lastAdmin ? 'Add another admin first' : 'Remove access'} disabled={busy || lastAdmin} onclick={() => removing = person}><Icon name="trash" size={14} /></button>
          {/if}
          {#if removing?.id === person.id}
            <div class="dlg-callout danger row-full"><p>Remove {person.name}? They will be signed out. History and assignments stay.</p><div class="button-row"><button class="small-button danger-button" disabled={busy} onclick={() => change(person)}>Remove access</button><button class="text-button" disabled={busy} onclick={() => removing = undefined}>Cancel</button></div></div>
          {/if}
        </li>
      {/each}
    </ul>
    {#if !visible.length}<p class="dlg-empty">No matching users.</p>{/if}
  </div>
  <footer class="dlg-foot"><label class="dlg-check"><input type="checkbox" bind:checked={showRemoved} />Show removed users</label><span class="meta actions">{users.filter(u => !u.removed_at).length} active · {admins} admin{admins === 1 ? '' : 's'}</span></footer>
</dialog>

<style>
  .invite-row { display: flex; align-items: center; gap: 8px; }
  .invite-row > :global(.select-trigger) { flex: 1; }
  .invite-row .note { flex: 1; }
  li.removed .row-main { opacity: .55; }
</style>
