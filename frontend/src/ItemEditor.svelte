<script lang="ts">
  import { untrack, onDestroy } from 'svelte';
  import { APIError, statuses, label, initials, avatarHue } from './api';
  import type { Item, ItemPatch, User } from './api';
  import Icon from './Icon.svelte';
  import Select from './Select.svelte';
  import ItemActivity from './ItemActivity.svelte';
  import type { EditorField } from './item-edit';
  import { itemFields, itemPatch, rebaseFields } from './item-edit';

  function autosize(node: HTMLTextAreaElement, _value: string) {
    const fit = () => {
      if (CSS.supports('field-sizing', 'content')) return;
      node.style.height = 'auto';
      node.style.height = `${node.scrollHeight}px`;
    };
    fit();
    return { update: fit };
  }

  let {
    item,
    users,
    tags,
    readonly = false,
    deleted = false,
    onmissing,
    ondelete,
    currentUserId,
    suspended = false,
    canPrevious = false,
    canNext = false,
    onnavigate,
    onclose,
    onpersist,
    onreload,
    ondirty,
  }: {
    item: Item;
    users: User[];
    tags: string[];
    readonly?: boolean;
    deleted?: boolean;
    onmissing: () => void;
    ondelete: () => void;
    currentUserId: string;
    suspended?: boolean;
    canPrevious?: boolean;
    canNext?: boolean;
    onnavigate: (direction: number) => void;
    onclose: () => void;
    onpersist: (id: string, version: number, patch: ItemPatch) => Promise<Item>;
    onreload: () => Promise<void>;
    ondirty: (dirty: boolean) => void;
  } = $props();
  let panel: HTMLElement;
  let base = $state<Item>(untrack(() => item));
  let draft = $state(untrack(() => itemFields(base)));
  let tagInput = $state('');
  let saving = $state(false);
  let composing = $state(false);
  let inFlight: Promise<void> | undefined;
  let destroyed = false;
  onDestroy(() => (destroyed = true));
  let error = $state('');
  const changes = $derived(itemPatch(base, draft));
  const changed = $derived(Object.keys(changes).length > 0);
  let dirty = $derived(changed || Boolean(tagInput.trim()));
  let conflict = $derived(Boolean(!saving && item.version > base.version && dirty));
  let canWrite = $derived(!readonly && !deleted);

  function fill(value: Item) {
    base = value;
    draft = itemFields(value);
    tagInput = '';
    error = '';
  }
  $effect(() => {
    const incoming = item;
    const pending = dirty || saving;
    untrack(() => {
      if (incoming && !pending && incoming.version > base.version) fill(incoming);
    });
  });
  $effect(() => {
    ondirty(dirty || saving);
  });
  $effect(() => {
    // Deriving the patch tracks every field, including continued typing.
    void changes;
    if (
      !changed ||
      !canWrite ||
      suspended ||
      saving ||
      composing ||
      conflict ||
      error ||
      !draft.title.trim()
    )
      return;
    const propertyChanged =
      changes.status !== undefined ||
      changes.type !== undefined ||
      changes.add_tags ||
      changes.remove_tags ||
      changes.add_assignees ||
      changes.remove_assignees;
    const timer = setTimeout(() => void save(), propertyChanged ? 0 : 500);
    return () => clearTimeout(timer);
  });
  function rebase() {
    if (tagInput.trim()) addTag();
    draft = rebaseFields(item, base, draft);
    base = item;
    error = '';
  }

  function save(): Promise<void> {
    if (inFlight) return inFlight;
    if (
      destroyed ||
      suspended ||
      !canWrite ||
      !changed ||
      !draft.title.trim() ||
      composing ||
      conflict ||
      error
    )
      return Promise.resolve();
    inFlight = persist().finally(() => (inFlight = undefined));
    return inFlight;
  }

  async function persist() {
    const sent = $state.snapshot(draft);
    saving = true;
    try {
      const saved = await onpersist(base.id, base.version, changes);
      if (destroyed || deleted) return;
      draft = rebaseFields(saved, sent, draft);
      base = saved;
    } catch (e) {
      if (destroyed || deleted) return;
      if (e instanceof APIError && e.status === 404) {
        onmissing();
        return;
      }
      const stale = e instanceof APIError && e.code === 'conflict';
      error = stale
        ? 'This ticket changed on the server. Review the incoming changes to continue saving.'
        : e instanceof Error
          ? e.message
          : 'Could not save. Your changes are kept here.';
      // A conflict response may arrive before its live update. Fetch the version to reconcile.
      if (stale) {
        try {
          await onreload();
        } catch {
          /* Keep the draft and retry control if the server is unavailable. */
        }
      }
    } finally {
      saving = false;
      if (!destroyed) ondirty(dirty);
    }
  }

  export function focus(field?: EditorField) {
    if (!field) {
      panel.focus();
      return;
    }
    const labels: Record<EditorField, string> = {
      title: 'Ticket title',
      description: 'Description',
      status: 'Ticket status',
      type: 'Ticket type',
      assignee: 'Add assignee',
      tags: 'Add tag',
    };
    const control = panel.querySelector<HTMLElement>(
      field === 'description' ? '#description' : `[aria-label="${labels[field]}"]`,
    );
    control?.focus();
    if (
      control?.getAttribute('role') === 'combobox' &&
      control.getAttribute('aria-expanded') !== 'true'
    )
      control.click();
  }

  export async function settle() {
    if (inFlight) await inFlight;
  }

  export async function flush(retry = false): Promise<boolean> {
    if (retry) error = '';
    for (;;) {
      if (inFlight) await inFlight;
      if (destroyed || composing || suspended) return false;
      if (tagInput.trim() && canWrite) addTag();
      if (!changed) {
        ondirty(dirty);
        return !dirty;
      }
      if (
        !canWrite ||
        !draft.title.trim() ||
        conflict ||
        error ||
        !panel.querySelector('form')?.reportValidity()
      )
        return false;
      await save();
    }
  }

  export function assignSelf() {
    if (canWrite && !suspended)
      draft.assignees = draft.assignees.includes(currentUserId)
        ? draft.assignees.filter((id) => id !== currentUserId)
        : [...draft.assignees, currentUserId];
  }

  function addTag() {
    const values = tagInput
      .split(',')
      .map((t) => t.trim().toLowerCase())
      .filter(Boolean);
    draft.tags = [...new Set([...draft.tags, ...values])];
    tagInput = '';
  }

  function editorKey(event: KeyboardEvent) {
    if (suspended || event.defaultPrevented || !panel?.contains(event.target as Node)) return;
    if (event.key === 'Tab' && matchMedia('(max-width: 800px)').matches) {
      const controls = [
        ...panel.querySelectorAll<HTMLElement>(
          'button:not(:disabled), input:not(:disabled), textarea:not(:disabled), select:not(:disabled), summary',
        ),
      ].filter((el) => el.getClientRects().length);
      const first = controls[0];
      const last = controls.at(-1);
      if (event.shiftKey && (event.target === first || event.target === panel)) {
        event.preventDefault();
        last?.focus();
      } else if (!event.shiftKey && event.target === last) {
        event.preventDefault();
        first?.focus();
      }
    }
    if ((event.ctrlKey || event.metaKey) && event.key === 'Enter' && !event.isComposing) {
      event.preventDefault();
      void flush(true).then((saved) => {
        if (saved && event.shiftKey) onclose();
      });
    }
  }
</script>

<svelte:window onkeydown={editorKey} />

<aside class="detail" tabindex="-1" bind:this={panel} aria-label={`Item ${item.id}`}>
  <div class="detail-top">
    <span class="detail-id"><span class="item-id">TK-{item.id}</span></span>
    <div class="button-row">
      <button
        class="icon-button"
        aria-label="Previous ticket"
        title="Previous ticket ([ / K)"
        disabled={!canPrevious}
        onclick={() => onnavigate(-1)}><Icon name="up" size={15} /></button
      ><button
        class="icon-button"
        aria-label="Next ticket"
        title="Next ticket (] / J)"
        disabled={!canNext}
        onclick={() => onnavigate(1)}><Icon name="arrow-down" size={15} /></button
      >{#if canWrite}<button
          class="icon-button danger-text"
          aria-label="Delete ticket"
          title="Delete ticket (Delete)"
          onclick={ondelete}><Icon name="trash" size={15} /></button
        >{/if}<button
        class="icon-button detail-close"
        aria-label="Close details"
        title="Close details (Escape)"
        onclick={onclose}
        ><span class="desktop-only"><Icon name="close" size={16} /></span><span class="mobile-only"
          ><Icon name="back" size={22} /></span
        ></button
      >
    </div>
  </div>
  <form
    class="editor-form"
    onsubmit={(event) => {
      event.preventDefault();
      void flush(true);
    }}
    oncompositionstart={() => (composing = true)}
    oncompositionend={() => (composing = false)}
  >
    <div class="detail-content">
      <!-- A wrapping single-line title: Enter saves like a text field, pasted line breaks become spaces. -->
      <textarea
        class="title-editor"
        rows="1"
        aria-label="Ticket title"
        placeholder="Title"
        maxlength="300"
        required
        spellcheck="false"
        bind:value={draft.title}
        use:autosize={draft.title}
        onblur={() => void save()}
        readonly={deleted}
        disabled={readonly && !deleted}
        oninput={(event) => {
          if (/[\r\n]/.test(event.currentTarget.value))
            draft.title = event.currentTarget.value.replace(/\s*[\r\n]+\s*/g, ' ');
        }}
        onkeydown={(event) => {
          if (event.key === 'Enter' && !event.isComposing) {
            event.preventDefault();
            event.currentTarget.form?.requestSubmit();
          }
        }}></textarea>

      {#if conflict && !deleted}
        <div class="conflict" role="status">
          <strong>Updated by someone else</strong>
          <p>Your draft is preserved. Review their changes, then choose which version to keep.</p>
          <details>
            <summary>Review latest version · v{item.version}</summary>
            <div class="remote-copy">
              <strong>{item.title}</strong>
              <p>{item.description || 'No description'}</p>
              <small
                >{label(item.status)} · {label(item.type)}<br />Tags: {item.tags.join(', ') ||
                  'none'}<br />Assignees: {item.assignees
                  .map((id) => users.find((u) => u.id === id)?.name || id)
                  .join(', ') || 'none'}</small
              >
            </div>
          </details>
          <div class="button-row">
            <button type="button" class="small-button" onclick={rebase}
              >Keep my edits on latest</button
            ><button type="button" class="text-button" onclick={() => fill(item)}
              >Discard my draft</button
            >
          </div>
        </div>
      {/if}
      {#if deleted}<div class="conflict" role="status">
          <strong>This ticket was deleted.</strong>
          <p>
            {dirty
              ? 'Your unsaved text is kept here. Copy anything you need before discarding the draft.'
              : 'You can close this panel.'}
          </p>
          {#if dirty}<button
              type="button"
              class="small-button"
              onclick={() => {
                fill(item);
                onclose();
              }}>Discard draft and close</button
            >{/if}
        </div>{/if}
      {#if error && !deleted}<div class="error-banner" role="alert">{error}</div>{/if}

      <div class="properties">
        <div class="property">
          <span>Status</span><Select
            label="Ticket status"
            variant="property"
            bind:value={draft.status}
            disabled={!canWrite}
            options={statuses.map((value) => ({
              value,
              label: label(value),
              icon: value,
              iconClass: `status-icon ${value}`,
            }))}
          />
        </div>
        <div class="property">
          <span>Type</span><Select
            label="Ticket type"
            variant="property"
            bind:value={draft.type}
            disabled={!canWrite}
            options={(['task', 'bug', 'feature'] as const).map((value) => ({
              value,
              label: label(value),
              icon: value,
              iconClass: `item-type ${value}`,
            }))}
          />
        </div>
        <div class="property">
          <span>Assignees</span>
          <div class="property-values">
            {#each draft.assignees as id}<span class="person-chip"
                ><span class="mini-avatar" style:--hue={avatarHue(id)}
                  >{initials(users.find((u) => u.id === id)?.name || id)}</span
                >{users.find((u) => u.id === id)?.name || `User ${id}`}{#if canWrite}<button
                    type="button"
                    aria-label={`Remove assignee ${users.find((u) => u.id === id)?.name || id}`}
                    onclick={() => (draft.assignees = draft.assignees.filter((a) => a !== id))}
                    ><Icon name="close" size={12} /></button
                  >{/if}</span
              >{/each}
            {#if canWrite}<Select
                label="Add assignee"
                variant="add"
                placeholder="Assign"
                placeholderIcon="add-person"
                value=""
                disabled={users.every((u) => u.removed_at || draft.assignees.includes(u.id))}
                onchange={(id) => {
                  if (id) draft.assignees = [...draft.assignees, id];
                }}
                options={users
                  .filter((u) => !u.removed_at && !draft.assignees.includes(u.id))
                  .map((u) => ({
                    value: u.id,
                    label: u.name,
                    avatar: initials(u.name),
                    avatarHue: avatarHue(u.id),
                    hint: u.id === currentUserId ? 'me' : undefined,
                  }))}
              />{:else if !draft.assignees.length}<span class="muted">Unassigned</span>{/if}
          </div>
        </div>
        <div class="property">
          <span>Tags</span>
          <div class="property-values">
            {#each draft.tags as tag}<span class="tag-chip"
                >{tag}{#if canWrite}<button
                    type="button"
                    aria-label={`Remove tag ${tag}`}
                    onclick={() => (draft.tags = draft.tags.filter((t) => t !== tag))}
                    ><Icon name="close" size={12} /></button
                  >{/if}</span
              >{/each}
            {#if canWrite}<div class="tag-entry">
                <input
                  aria-label="Add tag"
                  placeholder="+ Add tag"
                  list="known-tags"
                  maxlength="64"
                  bind:value={tagInput}
                  onblur={() => {
                    if (!composing && tagInput.trim()) addTag();
                  }}
                  onkeydown={(event) => {
                    if (
                      event.key === 'Enter' &&
                      !event.ctrlKey &&
                      !event.metaKey &&
                      !event.isComposing
                    ) {
                      event.preventDefault();
                      addTag();
                    }
                  }}
                /><button
                  type="button"
                  class="icon-button"
                  aria-label="Apply tag"
                  disabled={!tagInput.trim()}
                  onclick={addTag}><Icon name="plus" size={13} /></button
                >
              </div>
              <datalist id="known-tags"
                >{#each tags as tag}<option value={tag}></option>{/each}</datalist
              >{:else if !draft.tags.length}<span class="muted">No tags</span>{/if}
          </div>
        </div>
      </div>
      <div class="section-label"><label for="description">Description</label></div>
      <textarea
        id="description"
        class="description-editor"
        placeholder="Description (Markdown)"
        bind:value={draft.description}
        onblur={() => void save()}
        readonly={deleted}
        disabled={readonly && !deleted}
        spellcheck="false"></textarea>
      <ItemActivity {item} {users} {deleted} />
      <div class="detail-meta">
        Created {new Date(base.created_at).toLocaleDateString(undefined, {
          month: 'short',
          day: 'numeric',
          year: 'numeric',
        })}<span>TK-{base.id}</span>
      </div>
    </div>
    <div class="editor-footer">
      <span class:unsaved={dirty || saving} class="save-state" role="status" aria-live="polite"
        ><span class="tiny-dot" aria-hidden="true"></span>{deleted
          ? 'Ticket deleted'
          : saving
            ? 'Saving…'
            : readonly
              ? 'Read-only access'
              : conflict
                ? 'Resolve changes to save'
                : error
                  ? 'Not saved'
                  : !draft.title.trim()
                    ? 'Add a title to save'
                    : changed
                      ? 'Saving soon…'
                      : tagInput.trim()
                        ? 'Press Enter to add tag'
                        : 'All changes saved'}</span
      >
      {#if canWrite && (error || conflict)}<div class="button-row">
          <button
            type="button"
            class="text-button"
            disabled={saving}
            onclick={() => fill(item.version >= base.version ? item : base)}
            >Discard unsaved changes</button
          >{#if error && !conflict}<button
              type="button"
              class="small-button"
              disabled={saving}
              onclick={() => void flush(true)}>Retry save</button
            >{/if}
        </div>{/if}
    </div>
  </form>
</aside>
