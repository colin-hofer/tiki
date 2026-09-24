<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from './api';
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

<dialog class="people-dialog" bind:this={dialog} aria-labelledby="people-heading" onclose={onclose}>
  <div class="detail-top"><h2 id="people-heading">People</h2><button class="icon-button" aria-label="Close people" onclick={() => dialog.close()}><Icon name="close" size={16} /></button></div>
  <div class="people-body">
    <div class="people-tools"><input aria-label="Search people" placeholder="Find by name or email…" bind:value={search} /><button class="primary-button" onclick={() => inviting = !inviting} aria-expanded={inviting}>Invite people</button></div>
    {#if error}<p class="error-banner" role="alert">{error}</p>{/if}
    {#if notice}<p class="hint" role="status">{notice}</p>{/if}
    {#if inviting}
  <div class="invite-body">
    <p>Create a single-use link. They choose their own name, email, and password.</p>
    {#if invite}
      <label>Invite link<input readonly value={link} onclick={event => event.currentTarget.select()} /></label>
      <p class="hint">{invite.role} access · Expires {new Date(invite.expires_at * 1000).toLocaleString()}. Share privately with the person you want to invite.</p>
      <div class="button-row"><button class="primary-button" onclick={copy} disabled={busy}>{copied ? 'Copied' : 'Copy link'}</button><button class="text-button" onclick={revoke} disabled={busy}>Revoke link</button></div>
    {:else}
      <form onsubmit={create}>
        <div class="field"><span class="field-label">Invite role</span><Select label="Invite role" variant="field" bind:value={role} disabled={busy} options={[
          { value: 'member', label: 'Member', icon: 'person', description: 'Create and edit tickets' },
          { value: 'viewer', label: 'Viewer', icon: 'eye', description: 'Read only' },
          { value: 'admin', label: 'Admin', icon: 'shield', description: 'Manage users and invites' },
        ]} /></div>
        <p class="hint">The link expires in 7 days and can be claimed once.</p>
        <button class="primary-button" disabled={busy}>{busy ? 'Creating…' : 'Create invite link'}</button>
      </form>
    {/if}
  </div>
    {/if}
    <label class="removed-toggle"><input type="checkbox" bind:checked={showRemoved} /> Show removed users</label>
    <ul class="people-list" aria-label="Workspace users">
      {#each visible as person (person.id)}
        {@const lastAdmin = !person.removed_at && person.role === 'admin' && admins === 1}
        <li>
          <div class="person-info"><strong>{person.name}{person.id === currentUserId ? ' (you)' : ''}</strong><span>{person.email}</span></div>
          {#if person.removed_at}<span class="hint">Removed · Invite again to restore</span>
          {:else}
            <div class="person-role"><Select label={`Role for ${person.name}`} value={roleDraft[person.id] || person.role} disabled={busy || lastAdmin} onchange={value => change(person, value)} options={[
              { value: 'member', label: 'Member', icon: 'person' }, { value: 'viewer', label: 'Viewer', icon: 'eye' }, { value: 'admin', label: 'Admin', icon: 'shield' },
            ]} /></div>
            <button class="text-button remove-button" aria-label={`Remove ${person.name}`} title={lastAdmin ? 'Add another admin first' : 'Remove access'} disabled={busy || lastAdmin} onclick={() => removing = person}>Remove</button>
            {#if lastAdmin}<span class="hint last-admin">Last admin — promote someone before changing this role.</span>{/if}
          {/if}
          {#if removing?.id === person.id}
            <div class="remove-confirm"><p>Remove {person.name}'s access? They will be signed out. Ticket history and existing assignments remain.</p><div class="button-row"><button class="small-button" disabled={busy} onclick={() => change(person)}>Remove access</button><button class="text-button" disabled={busy} onclick={() => removing = undefined}>Cancel</button></div></div>
          {/if}
        </li>
      {/each}
    </ul>
    {#if !visible.length}<p class="hint">No matching users.</p>{/if}
  </div>
</dialog>

<style>
  .people-dialog { width: min(640px, calc(100vw - 24px)); max-height: calc(100dvh - 32px); overflow: auto; }
  .people-body { padding: 20px; display: grid; gap: 16px; }
  .people-tools { display: flex; align-items: center; gap: 12px; }
  .people-tools input { flex: 1; min-width: 0; margin: 0; }
  .people-tools button { white-space: nowrap; }
  .invite-body { padding: 16px; display: grid; gap: 16px; background: var(--inset); border: 1px solid var(--line); border-radius: 8px; }
  p { margin: 0; line-height: 1.5; }
  form { display: grid; gap: 16px; }
  label { font-size: 12px; font-weight: 500; }
  .field { display: grid; gap: 6px; }
  .field-label { font-size: 12px; font-weight: 500; }
  input { display: block; width: 100%; min-height: 36px; margin-top: 6px; padding: 8px; border: 1px solid var(--line); border-radius: 6px; background: var(--inset); color: var(--text); font: inherit; }
  .primary-button { min-height: 36px; padding: 8px 14px; }
  .button-row { display: flex; gap: 16px; }
  .people-list { list-style: none; margin: 0; padding: 0; }
  .people-list li { display: flex; align-items: center; flex-wrap: wrap; gap: 12px; padding: 16px 0; border-top: 1px solid var(--line); }
  .person-info { flex: 1; min-width: 180px; display: grid; gap: 4px; overflow-wrap: anywhere; }
  .person-info span { color: var(--text-2); font-size: 12px; }
  .person-role { width: 100px; }
  .removed-toggle { display: flex; align-items: center; gap: 8px; }
  .removed-toggle input { width: 16px; height: 16px; min-height: 0; margin: 0; }
  .last-admin, .remove-confirm { flex-basis: 100%; }
  .remove-confirm { display: grid; gap: 12px; padding: 12px; border-radius: 6px; background: var(--inset); }
  .remove-button { color: var(--text-2); }
  .remove-button:disabled { opacity: .4; cursor: default; }
</style>
