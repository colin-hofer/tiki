<script lang="ts">
  import { tick, untrack } from 'svelte';
  import { flip } from 'svelte/animate';
  import { SvelteSet } from 'svelte/reactivity';
  import { initials, avatarHue, label, statuses } from './api';
  import type { Item, Status } from './api';
  import type { BoardState } from './board-state.svelte';
  import Icon from './Icon.svelte';
  import { arrive, capture, duration } from './motion';

  let {
    data,
    grouped,
    query,
    readonly,
    dirty,
    coarse,
    selectedId = $bindable(''),
    activeColumn = $bindable<Status>('todo'),
    quick = $bindable(),
    creating,
    announcement,
    onopen,
    oncreate,
    onquickcreate,
    onmove,
    onstatus,
    onhelp,
  }: {
    data: BoardState;
    grouped: Record<Status, Item[]>;
    query: string;
    readonly: boolean;
    dirty: boolean;
    coarse: boolean;
    selectedId?: string;
    activeColumn?: Status;
    quick: { status: Status | null; title: string };
    creating: boolean;
    announcement: string;
    onopen: (id: string) => Promise<void>;
    oncreate: (status?: Status) => Promise<void>;
    onquickcreate: (event: SubmitEvent, edit?: boolean) => Promise<void>;
    onmove: (item: Item, anchor: Item, before: boolean) => Promise<void>;
    onstatus: (id: string, status: Status) => Promise<void>;
    onhelp: () => void;
  } = $props();
  const storageKey = 'tiki.list.collapsed';
  const columns = $derived(data.columns);
  const openId = $derived(data.ticket.id);
  const cursors = $derived(data.cursors);
  const busy = $derived(data.busy || Boolean(data.writing));
  const hasLoaded = $derived(data.hasLoaded);
  const moving = $derived(Boolean(data.writing));
  const collapsed = new SvelteSet<Status>(readCollapsed());
  // Keyboard order: every row of every expanded group, top to bottom.
  const rows = $derived(
    columns.filter((status) => !collapsed.has(status)).flatMap((status) => grouped[status]),
  );
  const selected = $derived(rows.find((item) => item.id === selectedId));
  let preferredRow = 0;
  let lastG = 0;
  let dragging = $state('');
  let dropTarget = $state('');
  let dropBefore = $state(true);
  let list = $state<HTMLElement>(null!);

  function readCollapsed(): Status[] {
    try {
      const saved = JSON.parse(localStorage.getItem(storageKey) || '[]');
      return Array.isArray(saved) ? statuses.filter((status) => saved.includes(status)) : [];
    } catch {
      return [];
    }
  }
  function toggle(status: Status) {
    if (collapsed.has(status)) collapsed.delete(status);
    else collapsed.add(status);
    try {
      localStorage.setItem(storageKey, JSON.stringify([...collapsed]));
    } catch {
      // Collapsed groups are a convenience; the list works without storage.
    }
  }

  // Capture the old layout and repair keyboard focus when filtering or live updates change rows.
  $effect.pre(() => {
    const current = rows;
    const availableColumns = columns;
    untrack(() => {
      const focused = document.activeElement;
      const restore = Boolean(focused?.closest('[data-ticket], .group-toggle'));
      const focusedItem = current.find((item) => item.id === selectedId);
      if (focusedItem) activeColumn = focusedItem.status;
      else if (focused?.closest('.group-toggle')) selectedId = '';
      else {
        if (!availableColumns.includes(activeColumn)) activeColumn = availableColumns[0];
        const group = collapsed.has(activeColumn) ? [] : grouped[activeColumn];
        selectedId = group[Math.min(preferredRow, group.length - 1)]?.id || '';
      }
      if (list) capture(list);
      if (restore)
        void tick().then(() => {
          if (document.activeElement !== focused || !focused?.isConnected) focusBoard();
        });
    });
  });

  function focusElement(element: HTMLElement | null | undefined) {
    element?.focus({ preventScroll: true });
    element?.scrollIntoView({ block: 'nearest' });
  }
  function focusItem(item: Item) {
    selectedId = item.id;
    activeColumn = item.status;
    preferredRow = grouped[item.status].indexOf(item);
    focusElement(list.querySelector<HTMLElement>(`#ticket-${item.id}`));
  }
  // Focus a row within a status group, or the group header when it is empty or collapsed.
  export function focusColumn(status = activeColumn, row = preferredRow) {
    activeColumn = columns.includes(status) ? status : columns[0];
    const group = collapsed.has(activeColumn) ? [] : grouped[activeColumn];
    const next = group[Math.min(Math.max(row, 0), group.length - 1)];
    if (next) focusItem(next);
    else {
      selectedId = '';
      focusElement(list.querySelector<HTMLElement>(`#column-${activeColumn}`));
    }
    preferredRow = row;
  }

  export function focusCreate() {
    list.querySelector<HTMLTextAreaElement>('#quick-title')?.focus();
  }

  export function focusBoard() {
    if (selected) focusItem(selected);
    else focusColumn(activeColumn, preferredRow);
  }
  // Alt+↑/↓ reorders within a group; at a group's edge the ticket crosses into the neighbouring status.
  export async function move(direction: number) {
    if (!selected) return;
    const item = selected;
    const group = grouped[item.status];
    const anchor = group[group.indexOf(item) + direction];
    if (anchor) return onmove(item, anchor, direction < 0);
    const status = columns[columns.indexOf(item.status) + direction];
    if (!status) return;
    if (collapsed.has(status)) toggle(status);
    const target = grouped[status];
    const edge = direction > 0 ? target[0] : target.at(-1);
    if (edge) await onmove(item, edge, direction > 0);
    else await onstatus(item.id, status);
  }
  function focusHeader(status: Status) {
    activeColumn = status;
    selectedId = '';
    focusElement(list.querySelector<HTMLElement>(`#column-${status}`));
  }

  export function handleKey(event: KeyboardEvent) {
    const target = event.target as HTMLElement;
    const key = event.key;
    const lower = key.toLowerCase();
    // Directional navigation only takes over on the list (or the unfocused page).
    if (
      !target.closest('.list-view') &&
      target !== document.body &&
      target !== document.documentElement
    )
      return;
    if (!event.altKey && /^[1-7]$/.test(key)) {
      event.preventDefault();
      const status = columns[Number(key) - 1];
      if (status) focusColumn(status, 0);
      return;
    }
    if (!event.altKey && (key === 'Home' || key === 'End' || key === 'G' || key === 'g')) {
      event.preventDefault();
      if (key === 'g') {
        const now = Date.now();
        if (now - lastG > 700) {
          lastG = now;
          return;
        }
        lastG = 0;
      }
      const item = key === 'G' || key === 'End' ? rows.at(-1) : rows[0];
      if (item) focusItem(item);
      return;
    }
    lastG = 0;
    if (!event.altKey && !event.shiftKey && key === 'x') {
      event.preventDefault();
      const status = selected?.status || activeColumn;
      toggle(status);
      void tick().then(() => focusColumn(status, preferredRow));
      return;
    }
    if (['arrowdown', 'arrowup', 'j', 'k'].includes(lower)) {
      event.preventDefault();
      const direction = key === 'ArrowDown' || lower === 'j' ? 1 : -1;
      if (event.altKey || (event.shiftKey && ['J', 'K'].includes(key))) {
        void move(direction);
        return;
      }
      // Walk the list as it reads, top to bottom: each group header, then its rows.
      const stops = columns.flatMap((status) => [
        status,
        ...(collapsed.has(status) ? [] : grouped[status]),
      ]);
      const next = stops[stops.indexOf(selected || activeColumn) + direction];
      if (typeof next === 'string') focusHeader(next);
      else if (next) focusItem(next);
    } else if (['arrowleft', 'arrowright', 'h', 'l'].includes(lower)) {
      event.preventDefault();
      // Statuses are stacked vertically, so sideways keys only leave or enter a group.
      if (event.altKey || event.shiftKey) return;
      const status = selected?.status || activeColumn;
      const inward = key === 'ArrowRight' || lower === 'l';
      if (selected) {
        if (!inward) focusHeader(status);
      } else if (inward) {
        if (collapsed.has(status)) toggle(status);
        else if (grouped[status].length) focusColumn(status, 0);
      } else if (!collapsed.has(status)) toggle(status);
    } else if (key === 'Enter' && selected && !target.closest('button')) {
      event.preventDefault();
      void onopen(selected.id);
    }
  }
</script>

<div
  class="list-view"
  bind:this={list}
  aria-label="Items by status"
  aria-describedby="board-keyboard-hint"
>
  {#each columns as status}
    {@const groupItems = grouped[status]}
    {@const open = !collapsed.has(status)}
    <!-- Native drag/drop is an additional input; the same action is available through Alt+arrows and the editor. -->
    <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
    <section
      data-column={status}
      class="list-group"
      class:drop-target={dragging && data.itemsById.get(dragging)?.status !== status}
      aria-label={`${label(status)} group`}
      ondragover={(event) => {
        if (dragging && !readonly) event.preventDefault();
      }}
      ondrop={(event) => {
        event.preventDefault();
        if (dragging) void onstatus(dragging, status);
      }}
    >
      <div class="group-header">
        <button
          id={`column-${status}`}
          class="group-toggle"
          tabindex={activeColumn === status && !selected ? 0 : -1}
          aria-expanded={open}
          onfocus={() => {
            activeColumn = status;
            selectedId = '';
          }}
          onclick={() => toggle(status)}
          ><span class="group-chevron" class:open><Icon name="arrow" size={13} /></span><span
            class={`status-icon ${status}`}><Icon name={status} size={14} /></span
          >
          <h2>{label(status)}</h2>
          <span class="column-count" title="Loaded items"
            >{groupItems.length}{cursors[status] ? '+' : ''}</span
          ></button
        >{#if !readonly}<button
            class="icon-button column-add"
            aria-label={`Add item to ${label(status)}`}
            title="Add item (C)"
            onfocus={() => {
              activeColumn = status;
              selectedId = '';
            }}
            onclick={() => {
              if (!open) toggle(status);
              void oncreate(status);
            }}><Icon name="plus" size={15} /></button
          >{/if}
      </div>
      {#if quick.status === status}<form
          class="quick-create list-create"
          onsubmit={(event) =>
            onquickcreate(event, (event.submitter as HTMLButtonElement)?.value === 'edit')}
        >
          <textarea
            id="quick-title"
            aria-label="New item title"
            placeholder="Item title"
            rows="1"
            maxlength="300"
            required
            bind:value={quick.title}
            disabled={creating || readonly}
            onkeydown={(event) => {
              if (event.key === 'Enter' && !event.shiftKey && !event.isComposing) {
                event.preventDefault();
                const form = event.currentTarget.form;
                form?.requestSubmit(
                  event.ctrlKey || event.metaKey
                    ? form.querySelector<HTMLButtonElement>('[value=edit]')!
                    : undefined,
                );
              }
            }}></textarea>
          <div>
            <span class="hint"><kbd>↵</kbd> add <kbd>esc</kbd> cancel</span><button
              type="button"
              class="text-button mobile-only quick-cancel"
              onclick={() => {
                quick.status = null;
                quick.title = '';
              }}>Cancel</button
            ><button
              type="submit"
              value="edit"
              class="text-button"
              title="Add and edit (Ctrl/Cmd+Enter)"
              disabled={creating || readonly || !quick.title.trim()}>Add & edit</button
            ><button class="small-button" disabled={creating || readonly || !quick.title.trim()}
              >{creating ? 'Adding…' : 'Add'}</button
            >
          </div>
        </form>{/if}
      {#if open}
        <ul class="rows" aria-label={`${label(status)} items`}>
          {#each groupItems as item (item.id)}
            <li data-card={item.id} animate:flip={{ duration: duration(220) }} in:arrive>
              <button
                id={`ticket-${item.id}`}
                data-ticket={item.id}
                tabindex={selectedId === item.id ? 0 : -1}
                class="row"
                class:drop-before={dropTarget === item.id && dropBefore}
                class:drop-after={dropTarget === item.id && !dropBefore}
                class:selected={selectedId === item.id}
                class:opened={openId === item.id}
                draggable={!readonly && !moving && !dirty && !coarse}
                ondragstart={(event) => {
                  dragging = item.id;
                  selectedId = item.id;
                  event.dataTransfer?.setData('text/plain', item.id);
                }}
                ondragend={() => {
                  dragging = '';
                  dropTarget = '';
                }}
                ondragover={(event) => {
                  if (!dragging || dragging === item.id || readonly) return;
                  event.preventDefault();
                  event.stopPropagation();
                  dropTarget = item.id;
                  const rect = event.currentTarget.getBoundingClientRect();
                  dropBefore = event.clientY < rect.top + rect.height / 2;
                }}
                ondragleave={() => {
                  if (dropTarget === item.id) dropTarget = '';
                }}
                ondrop={(event) => {
                  event.preventDefault();
                  event.stopPropagation();
                  const current = data.itemsById.get(dragging);
                  if (current) void onmove(current, item, dropBefore);
                }}
                onfocus={() => {
                  selectedId = item.id;
                  activeColumn = status;
                  preferredRow = groupItems.indexOf(item);
                }}
                onclick={() => void onopen(item.id)}
                aria-label={`TK-${item.id}: ${item.title}`}
                aria-current={openId === item.id ? 'true' : undefined}
              >
                <span class={`item-type ${item.type}`} title={label(item.type)}
                  ><Icon name={item.type} size={13} /></span
                ><span class="item-id">#{item.id}</span><span class="row-text"
                  ><span class="row-title">{item.title}</span>{#if item.preview}<span
                      class="row-preview">{item.preview}</span
                    >{/if}</span
                ><span class="card-tags row-tags"
                  >{#each item.tags.slice(0, 2) as tag}<span>{tag}</span
                    >{/each}{#if item.tags.length > 2}<span>+{item.tags.length - 2}</span
                    >{/if}</span
                ><span class="card-assignees"
                  >{#each item.assignees.slice(0, 2) as id}<span
                      class="mini-avatar"
                      style:--hue={avatarHue(id)}
                      title={data.usersById.get(id)?.name || id}
                      >{initials(data.usersById.get(id)?.name || id)}</span
                    >{/each}{#if item.assignees.length > 2}<span class="muted"
                      >+{item.assignees.length - 2}</span
                    >{/if}</span
                >
              </button>
            </li>
          {/each}
        </ul>
        {#if !hasLoaded}<p class="column-empty">
            Loading…
          </p>{:else if !groupItems.length && quick.status !== status}<p class="column-empty">
            {query ? 'No matches' : 'No tickets'}
          </p>{/if}
        {#if cursors[status]}<button
            class="load-more"
            disabled={busy}
            onfocus={() => {
              activeColumn = status;
              selectedId = '';
              preferredRow = Math.max(0, groupItems.length - 1);
            }}
            onclick={() => data.more(status)}>Load more</button
          >{/if}
      {/if}
    </section>
  {/each}
</div>
<div class="board-footer" id="board-keyboard-hint">
  <span><kbd>↑ ↓</kbd> / <kbd>j k</kbd> navigate</span><span
    ><kbd>← →</kbd> / <kbd>h l</kbd> group header · rows</span
  ><span><kbd>Alt ↑ ↓</kbd> reorder · change status</span><span><kbd>Enter</kbd> open</span
  >{#if !readonly}<span><kbd>C</kbd> comment</span><span><kbd>N</kbd> create</span>{/if}<span
    class="board-feedback"
    role="status"
    aria-live="polite">{moving ? 'Updating…' : announcement}</span
  ><button class="text-button" onclick={onhelp}><kbd>?</kbd> Shortcuts</button>
</div>
{#if !readonly && !openId && !quick.status}<button
    class="fab"
    aria-label="New"
    title="New ticket (N)"
    onclick={() => {
      if (collapsed.has(activeColumn)) toggle(activeColumn);
      void oncreate(activeColumn);
    }}><Icon name="plus" size={22} strokeWidth={2} /></button
  >{/if}
