<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import { api, APIError, statuses, label, initials } from './api';
  import type { Item, ItemType, Status, User, Activity, ActivityPage } from './api';
  import Icon from './Icon.svelte';

  let { item = null, creating = false, users, tags, readonly = false, defaults = [], onclose, onsave, ondirty }:
    { item?: Item | null; creating?: boolean; users: User[]; tags: string[]; readonly?: boolean; defaults?: string[];
      onclose: () => void; onsave: (item: Item) => void; ondirty: (dirty: boolean) => void } = $props();
  let base = $state<Item | null>(null);
  let title = $state('');
  let description = $state('');
  let status = $state<Status>('todo');
  let type = $state<ItemType>('task');
  let itemTags = $state<string[]>(untrack(() => [...defaults]));
  let assignees = $state<string[]>([]);
  let tagInput = $state('');
  let saving = $state(false);
  let error = $state('');
  let activity = $state<Activity[]>([]);
  let activityCursor = $state('');
  let activityOpen = $state(false);
  let activityBusy = $state(false);
  let activityError = $state('');
  let titleInput: HTMLInputElement;
  const same = (a: string[], b: string[]) => a.length === b.length && a.every(x => b.includes(x));
  let dirty = $derived(base ? title !== base.title || description !== (base.description || '') || status !== base.status || type !== base.type || !same(itemTags, base.tags) || !same(assignees, base.assignees) : Boolean(title || description));
  let conflict = $derived(Boolean(base && item && item.version > base.version && dirty));
  let canWrite = $derived(!readonly && !saving);

  function fill(value: Item) {
    base = value; title = value.title; description = value.description || ''; status = value.status;
    type = value.type; itemTags = [...value.tags]; assignees = [...value.assignees]; error = '';
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
  onMount(() => { if (creating) titleInput?.focus(); });

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

  async function save(event?: SubmitEvent) {
    event?.preventDefault();
    if (!canWrite || !title.trim() || conflict || (!creating && !dirty)) return;
    saving = true; error = '';
    try {
      const saved = creating
        ? await api<Item>('/items', 'POST', { title, description, status, type, tags: itemTags, assignees })
        : await api<Item>(`/items/${base!.id}`, 'PATCH', { version: base!.version, ...patch() });
      fill(saved); ondirty(false); onsave(saved);
    } catch (e) {
      error = e instanceof APIError && e.code === 'conflict'
        ? 'This ticket changed on the server. Your draft is safe. Review the incoming changes before saving again.'
        : e instanceof Error ? e.message : 'Could not save. Your draft is safe.';
      // Creates are deliberately not retried: the API has no idempotency keys yet.
      if (creating && !(e instanceof APIError)) error += ' Check the list before creating again; the server may have received it.';
    } finally { saving = false; }
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
    if ((event.ctrlKey || event.metaKey) && event.key === 'Enter' && !event.isComposing) {
      event.preventDefault(); void save();
    }
  }
</script>

<svelte:window onkeydown={editorKey} />

<aside class="detail" aria-label={creating ? 'New ticket' : `Ticket ${item?.id}`}>
  <div class="detail-top">
    <span class="item-id">{creating ? 'New item' : `#${item?.id}`}</span>
    <button class="icon-button" aria-label="Close details" title="Close details (Escape)" onclick={onclose}><Icon name="close" size={17} /></button>
  </div>
  <form class="editor-form" onsubmit={save}>
    <div class="detail-content">
      <input class="title-editor" aria-label="Ticket title" placeholder="Title" maxlength="300" required bind:value={title} bind:this={titleInput} disabled={!canWrite} />

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
        <label class="property"><span>Status</span><select aria-label="Ticket status" bind:value={status} disabled={!canWrite}>{#each statuses as value}<option value={value}>{label(value)}</option>{/each}</select></label>
        <label class="property"><span>Type</span><select aria-label="Ticket type" bind:value={type} disabled={!canWrite}>{#each ['task', 'bug', 'feature'] as value}<option value={value}>{label(value)}</option>{/each}</select></label>
        <div class="property"><span>Assignees</span><div class="property-values">
          {#each assignees as id}<span class="person-chip"><span class="mini-avatar">{initials(users.find(u => u.id === id)?.name || id)}</span>{users.find(u => u.id === id)?.name || `User ${id}`}{#if !readonly}<button type="button" aria-label={`Remove assignee ${users.find(u => u.id === id)?.name || id}`} disabled={saving} onclick={() => assignees = assignees.filter(a => a !== id)}>×</button>{/if}</span>{/each}
          {#if !readonly}<select aria-label="Add assignee" class="add-select" disabled={saving} onchange={event => { if (event.currentTarget.value) assignees = [...assignees, event.currentTarget.value]; event.currentTarget.value = ''; }}><option value="">+ Assign</option>{#each users.filter(u => !assignees.includes(u.id)) as u}<option value={u.id}>{u.name}</option>{/each}</select>{:else if !assignees.length}<span class="muted">Unassigned</span>{/if}
        </div></div>
        <div class="property"><span>Tags</span><div class="property-values">
          {#each itemTags as tag}<span class="tag-chip">{tag}{#if !readonly}<button type="button" aria-label={`Remove tag ${tag}`} disabled={saving} onclick={() => itemTags = itemTags.filter(t => t !== tag)}>×</button>{/if}</span>{/each}
          {#if !readonly}<div class="tag-entry"><input aria-label="Add tag" placeholder="+ Add tag" list="known-tags" maxlength="64" bind:value={tagInput} disabled={saving} onkeydown={event => { if (event.key === 'Enter' && !event.isComposing) { event.preventDefault(); addTag(); } }} /><button type="button" class="icon-button" aria-label="Apply tag" disabled={!tagInput.trim() || saving} onclick={addTag}><Icon name="plus" size={13} /></button></div><datalist id="known-tags">{#each tags as tag}<option value={tag}></option>{/each}</datalist>{:else if !itemTags.length}<span class="muted">No tags</span>{/if}
        </div></div>
      </div>
      <div class="section-label"><label for="description">Description</label></div>
      <textarea id="description" class="description-editor" placeholder="Description (Markdown)" bind:value={description} disabled={!canWrite} spellcheck="false"></textarea>
      {#if base}
        <div class="activity-section">
          <button type="button" class="activity-toggle" aria-expanded={activityOpen} onclick={() => activityOpen = !activityOpen}><Icon name={activityOpen ? 'down' : 'arrow'} size={14} /> Activity</button>
          {#if activityOpen}
            {#if activityError}<p class="error-banner" role="alert">{activityError}</p>{/if}
            <ol class="activity-list">{#each activity as event (event.id)}<li><span class="activity-node"></span><div><strong>{users.find(u => u.id === event.actor_id)?.name || `User ${event.actor_id}`}</strong> <span class="muted">{event.kind.replaceAll('_', ' ').replaceAll('.', ' ')}</span><time datetime={event.created_at}>{new Date(event.created_at).toLocaleString()}</time></div></li>{/each}</ol>
            {#if activityCursor}<button type="button" class="text-button" disabled={activityBusy} onclick={() => loadActivity(true)}>Load more activity</button>{/if}
            {#if activityBusy}<p class="muted">Loading activity…</p>{/if}
          {/if}
        </div>
        <div class="detail-meta">Created {new Date(base.created_at).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' })}<span>TK-{base.id}</span></div>
      {/if}
    </div>
    <div class="editor-footer">
      <span class:unsaved={dirty} class="save-state"><span class="tiny-dot"></span>{saving ? 'Saving…' : readonly ? 'Read-only access' : dirty ? 'Unsaved changes' : creating ? 'New item' : 'Saved'}</span>
      {#if !readonly}<div class="button-row">{#if dirty && base}<button type="button" class="text-button" disabled={saving} onclick={() => fill(item || base!)}>Discard</button>{/if}<button class="primary-button" type="submit" disabled={saving || conflict || !title.trim() || (!creating && !dirty)}>{creating ? 'Create ticket' : 'Save'}<kbd>⌘ ↵</kbd></button></div>{/if}
    </div>
  </form>
</aside>
