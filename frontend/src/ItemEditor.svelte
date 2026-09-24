<script lang="ts">
  import { untrack, tick } from 'svelte';
  import { api, APIError, statuses, label, initials } from './api';
  import type { Item, ItemType, Status, User, Activity, ActivityPage } from './api';
  import Icon from './Icon.svelte';
  import Select from './Select.svelte';
  import { slide } from 'svelte/transition';
  import { duration } from './motion';

  let { item, users, tags, readonly = false, currentUserId, suspended = false, canPrevious = false, canNext = false, onnavigate, onclose, onsave, ondirty }:
    { item: Item; users: User[]; tags: string[]; readonly?: boolean; currentUserId: string; suspended?: boolean; canPrevious?: boolean; canNext?: boolean;
      onnavigate: (direction: number) => void; onclose: () => void; onsave: (item: Item) => void | Promise<void>; ondirty: (dirty: boolean) => void } = $props();
  let panel: HTMLElement;
  let base = $state<Item | null>(null);
  let title = $state('');
  let description = $state('');
  let status = $state<Status>('todo');
  let type = $state<ItemType>('task');
  let itemTags = $state<string[]>([]);
  let assignees = $state<string[]>([]);
  let tagInput = $state('');
  let saving = $state(false);
  let error = $state('');
  let activity = $state<Activity[]>([]);
  let activityCursor = $state('');
  let activityOpen = $state(false);
  let activityBusy = $state(false);
  let activityError = $state('');
  const same = (a: string[], b: string[]) => a.length === b.length && a.every(x => b.includes(x));
  let dirty = $derived(Boolean(tagInput.trim()) || (base ? title !== base.title || description !== (base.description || '') || status !== base.status || type !== base.type || !same(itemTags, base.tags) || !same(assignees, base.assignees) : Boolean(title || description)));
  let conflict = $derived(Boolean(base && item && item.version > base.version && dirty));
  let canWrite = $derived(!readonly && !saving);

  function fill(value: Item) {
    base = value; title = value.title; description = value.description || ''; status = value.status;
    type = value.type; itemTags = [...value.tags]; assignees = [...value.assignees]; tagInput = ''; error = '';
  }
  $effect(() => {
    const incoming = item;
    untrack(() => { if (incoming && (!base || (!dirty && incoming.version > base.version))) fill(incoming); });
  });
  $effect(() => { ondirty(dirty); });
  $effect(() => {
    const version = item?.version;
    if (activityOpen && version) untrack(() => { void loadActivity(); });
  });

  function patch(): Partial<Pick<Item, 'title' | 'description' | 'status' | 'type'>> & { add_tags?: string[]; remove_tags?: string[]; add_assignees?: string[]; remove_assignees?: string[] } {
    if (!base) return {};
    return {
      ...(title !== base.title ? { title } : {}),
      ...(description !== (base.description || '') ? { description } : {}),
      ...(status !== base.status ? { status } : {}),
      ...(type !== base.type ? { type } : {}),
      add_tags: itemTags.filter(t => !base!.tags.includes(t)),
      remove_tags: base.tags.filter(t => !itemTags.includes(t)),
      add_assignees: assignees.filter(id => !base!.assignees.includes(id)),
      remove_assignees: base.assignees.filter(id => !assignees.includes(id)),
    };
  }

  function rebase() {
    if (!item || !base) return;
    if (tagInput.trim()) addTag();
    const changes = patch();
    const latest = item;
    fill(latest);
    if (changes.title !== undefined) title = changes.title;
    if (changes.description !== undefined) description = changes.description;
    if (changes.status !== undefined) status = changes.status;
    if (changes.type !== undefined) type = changes.type;
    itemTags = [...new Set([...latest.tags.filter(t => !changes.remove_tags?.includes(t)), ...(changes.add_tags || [])])];
    assignees = [...new Set([...latest.assignees.filter(id => !changes.remove_assignees?.includes(id)), ...(changes.add_assignees || [])])];
  }

  async function save(event?: SubmitEvent, close = false) {
    event?.preventDefault();
    if (!canWrite || suspended || !base || !title.trim() || conflict) return;
    if (!dirty) { if (close) onclose(); return; }
    const focused = document.activeElement as HTMLElement;
    let closed = false;
    if (tagInput.trim()) addTag();
    saving = true; error = '';
    try {
      const saved = await api<Item>(`/items/${base.id}`, 'PATCH', { version: base.version, ...patch() });
      fill(saved); ondirty(false); await onsave(saved);
      if (close) { closed = true; onclose(); }
    } catch (e) {
      error = e instanceof APIError && e.code === 'conflict'
        ? 'This ticket changed on the server. Your draft is safe. Review the incoming changes before saving again.'
        : e instanceof Error ? e.message : 'Could not save. Your draft is safe.';
    } finally { saving = false; await tick(); if (!closed && focused?.isConnected && (document.activeElement === document.body || panel.contains(document.activeElement))) focused.focus(); }
  }

  export function assignSelf() {
    if (canWrite && !suspended) assignees = assignees.includes(currentUserId) ? assignees.filter(id => id !== currentUserId) : [...assignees, currentUserId];
  }

  function addTag() {
    const values = tagInput.split(',').map(t => t.trim().toLowerCase()).filter(Boolean);
    itemTags = [...new Set([...itemTags, ...values])]; tagInput = '';
  }

  async function loadActivity(more = false) {
    if (!item || activityBusy) return;
    activityBusy = true; activityError = '';
    try {
      const page = await api<ActivityPage>(`/items/${item.id}/activity?limit=50${more && activityCursor ? `&after=${activityCursor}` : ''}`);
      activity = more ? [...activity, ...page.activity] : page.activity;
      activityCursor = page.next_after || '';
    } catch (e) { activityError = e instanceof Error ? e.message : 'Could not load activity.'; }
    finally { activityBusy = false; }
  }

  function editorKey(event: KeyboardEvent) {
    if (suspended || event.defaultPrevented || !panel?.contains(event.target as Node)) return;
    if (event.key === 'Tab' && matchMedia('(max-width: 800px)').matches) {
      const controls = [...panel.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), textarea:not(:disabled), select:not(:disabled), summary')].filter(el => el.getClientRects().length);
      const first = controls[0]; const last = controls.at(-1);
      if (event.shiftKey && (event.target === first || event.target === panel)) { event.preventDefault(); last?.focus(); }
      else if (!event.shiftKey && event.target === last) { event.preventDefault(); first?.focus(); }
    }
    if ((event.ctrlKey || event.metaKey) && event.key === 'Enter' && !event.isComposing) {
      event.preventDefault(); if (panel.querySelector('form')?.reportValidity()) void save(undefined, event.shiftKey);
    }
  }
</script>

<svelte:window onkeydown={editorKey} />

<aside class="detail" tabindex="-1" bind:this={panel} aria-label={`Item ${item.id}`}>
  <div class="detail-top">
    <span class="detail-id"><span class="item-id">TK-{item.id}</span></span>
    <div class="button-row"><button class="icon-button" aria-label="Previous ticket" title="Previous ticket ([ / K)" disabled={!canPrevious || saving} onclick={() => onnavigate(-1)}><Icon name="up" size={15} /></button><button class="icon-button" aria-label="Next ticket" title="Next ticket (] / J)" disabled={!canNext || saving} onclick={() => onnavigate(1)}><Icon name="arrow-down" size={15} /></button><button class="icon-button detail-close" aria-label="Close details" title="Close details (Escape)" disabled={saving} onclick={onclose}><span class="desktop-only"><Icon name="close" size={16} /></span><span class="mobile-only"><Icon name="back" size={22} /></span></button></div>
  </div>
  <form class="editor-form" onsubmit={save}>
    <div class="detail-content">
      <input class="title-editor" aria-label="Ticket title" placeholder="Title" maxlength="300" required bind:value={title} disabled={!canWrite} />

      {#if conflict}
        <div class="conflict" role="status">
          <strong>Updated by someone else</strong>
          <p>Your draft is preserved. Review their changes, then choose which version to keep.</p>
          <details><summary>Review latest version · v{item?.version}</summary><div class="remote-copy"><strong>{item?.title}</strong><p>{item?.description || 'No description'}</p><small>{label(item?.status || '')} · {label(item?.type || '')}<br />Tags: {item?.tags.join(', ') || 'none'}<br />Assignees: {item?.assignees.map(id => users.find(u => u.id === id)?.name || id).join(', ') || 'none'}</small></div></details>
          <div class="button-row"><button type="button" class="small-button" onclick={rebase}>Keep my edits on latest</button><button type="button" class="text-button" onclick={() => item && fill(item)}>Discard my draft</button></div>
        </div>
      {/if}
      {#if error}<div class="error-banner" role="alert">{error}</div>{/if}

      <div class="properties">
        <div class="property"><span>Status</span><Select label="Ticket status" variant="property" bind:value={status} disabled={!canWrite} options={statuses.map(value => ({ value, label: label(value), icon: value, iconClass: `status-icon ${value}` }))} /></div>
        <div class="property"><span>Type</span><Select label="Ticket type" variant="property" bind:value={type} disabled={!canWrite} options={(['task', 'bug', 'feature'] as const).map(value => ({ value, label: label(value), icon: value, iconClass: `item-type ${value}` }))} /></div>
        <div class="property"><span>Assignees</span><div class="property-values">
          {#each assignees as id}<span class="person-chip"><span class="mini-avatar">{initials(users.find(u => u.id === id)?.name || id)}</span>{users.find(u => u.id === id)?.name || `User ${id}`}{#if !readonly}<button type="button" aria-label={`Remove assignee ${users.find(u => u.id === id)?.name || id}`} disabled={saving} onclick={() => assignees = assignees.filter(a => a !== id)}><Icon name="close" size={12} /></button>{/if}</span>{/each}
          {#if !readonly}<Select label="Add assignee" variant="add" placeholder="Assign" placeholderIcon="add-person" value="" disabled={saving || users.every(u => u.removed_at || assignees.includes(u.id))} onchange={id => { if (id) assignees = [...assignees, id]; }} options={users.filter(u => !u.removed_at && !assignees.includes(u.id)).map(u => ({ value: u.id, label: u.name, avatar: initials(u.name), hint: u.id === currentUserId ? 'me' : undefined }))} />{:else if !assignees.length}<span class="muted">Unassigned</span>{/if}
        </div></div>
        <div class="property"><span>Tags</span><div class="property-values">
          {#each itemTags as tag}<span class="tag-chip">{tag}{#if !readonly}<button type="button" aria-label={`Remove tag ${tag}`} disabled={saving} onclick={() => itemTags = itemTags.filter(t => t !== tag)}><Icon name="close" size={12} /></button>{/if}</span>{/each}
          {#if !readonly}<div class="tag-entry"><input aria-label="Add tag" placeholder="+ Add tag" list="known-tags" maxlength="64" bind:value={tagInput} disabled={saving} onkeydown={event => { if (event.key === 'Enter' && !event.ctrlKey && !event.metaKey && !event.isComposing) { event.preventDefault(); addTag(); } }} /><button type="button" class="icon-button" aria-label="Apply tag" disabled={!tagInput.trim() || saving} onclick={addTag}><Icon name="plus" size={13} /></button></div><datalist id="known-tags">{#each tags as tag}<option value={tag}></option>{/each}</datalist>{:else if !itemTags.length}<span class="muted">No tags</span>{/if}
        </div></div>
      </div>
      <div class="section-label"><label for="description">Description</label></div>
      <textarea id="description" class="description-editor" placeholder="Description (Markdown)" bind:value={description} disabled={!canWrite} spellcheck="false"></textarea>
      {#if base}
        <div class="activity-section">
          <button type="button" class="activity-toggle" aria-expanded={activityOpen} onclick={() => activityOpen = !activityOpen}><Icon name={activityOpen ? 'down' : 'arrow'} size={14} />Activity</button>
          {#if activityOpen}<div transition:slide={{ duration: duration(180) }}>
            {#if activityError}<p class="error-banner" role="alert">{activityError}</p>{/if}
            <ol class="activity-list">{#each activity as event (event.id)}<li><span class="activity-node" aria-hidden="true"></span><div><strong>{users.find(u => u.id === event.actor_id)?.name || `User ${event.actor_id}`}</strong> <span class="muted">{event.kind.replaceAll('_', ' ').replaceAll('.', ' ')}</span><time datetime={event.created_at}>{new Date(event.created_at).toLocaleString()}</time></div></li>{/each}</ol>
            {#if activityCursor}<button type="button" class="text-button" disabled={activityBusy} onclick={() => loadActivity(true)}>Load more activity</button>{/if}
            {#if activityBusy}<p class="muted">Loading activity…</p>{/if}
          </div>{/if}
        </div>
        <div class="detail-meta">Created {new Date(base.created_at).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' })}<span>TK-{base.id}</span></div>
      {/if}
    </div>
    <div class="editor-footer">
      <span class:unsaved={dirty} class="save-state"><span class="tiny-dot" aria-hidden="true"></span>{saving ? 'Saving…' : readonly ? 'Read-only access' : dirty ? 'Unsaved changes' : 'Saved'}</span>
      {#if !readonly}<div class="button-row">{#if dirty && base}<button type="button" class="text-button" disabled={saving} onclick={() => fill(item || base!)}>Discard</button>{/if}<button class="primary-button" type="submit" disabled={saving || conflict || !title.trim() || !dirty}>Save<kbd aria-hidden="true">⌘↵</kbd></button></div>{/if}
    </div>
  </form>
</aside>
