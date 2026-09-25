<script lang="ts">
  import { onDestroy, tick, untrack } from 'svelte';
  import { flip } from 'svelte/animate';
  import { initials, avatarHue, label, statuses } from './api';
  import type { Item, Status } from './api';
  import type { BoardState } from './board.svelte';
  import Icon from './Icon.svelte';
  import { arrive, capture, duration, pin, unpin } from './motion';

  let {
    data,
    visible,
    query,
    readonly,
    dirty,
    mobile,
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
    guardDraft,
    onhelp,
  }: {
    data: BoardState;
    visible: Item[];
    query: string;
    readonly: boolean;
    dirty: boolean;
    mobile: boolean;
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
    guardDraft: () => Promise<boolean>;
    onhelp: () => void;
  } = $props();
  const columns = $derived(data.columns);
  const items = $derived(data.items);
  const users = $derived(data.users);
  const selected = $derived(visible.find((item) => item.id === selectedId));
  const openId = $derived(data.openId);
  const cursors = $derived(data.cursors);
  const busy = $derived(data.busy || Boolean(data.writing));
  const hasLoaded = $derived(data.hasLoaded);
  const moving = $derived(Boolean(data.writing));
  let preferredRow = 0;
  let lastG = 0;
  let dragging = $state('');
  let dropTarget = $state('');
  let dropBefore = $state(true);
  let board = $state<HTMLElement>(null!);
  let tabStrip = $state<HTMLElement>(null!);
  let feedStatus = $state<Status>('todo');
  let feedReady = false;
  let press: { timer: ReturnType<typeof setTimeout>; x: number; y: number } | undefined;
  let suppressClick = false;

  $effect.pre(() => {
    const rows = visible;
    const availableColumns = columns;
    untrack(() => {
      const focused = document.activeElement;
      const restore = Boolean(focused?.closest('[data-ticket], .column-focus'));
      const focusedItem = rows.find((item) => item.id === selectedId);
      if (focusedItem) activeColumn = focusedItem.status;
      else {
        if (!availableColumns.includes(activeColumn)) activeColumn = availableColumns[0];
        const column = rows.filter((item) => item.status === activeColumn);
        selectedId = column[Math.min(preferredRow, column.length - 1)]?.id || '';
      }
      if (board) capture(board);
      if (restore)
        void tick().then(() => {
          if (document.activeElement !== focused || !focused?.isConnected) focusBoard();
        });
    });
  });
  onDestroy(() => {
    pressEnd();
    const drag = stopDrag();
    drag?.ghost.remove();
  });
  $effect(() => {
    if (!mobile || !hasLoaded || !board || feedReady) return;
    feedReady = true;
    void tick().then(() =>
      showStatus(columns.includes(activeColumn) ? activeColumn : columns[0], true),
    );
  });
  $effect(() => {
    if (!columns.includes(feedStatus) && columns[0]) feedStatus = columns[0];
  });
  $effect(() => {
    const tab = tabStrip?.querySelector<HTMLElement>(`[data-status="${feedStatus}"]`);
    if (tab && mobile)
      tabStrip.scrollTo({
        left: tab.offsetLeft - (tabStrip.clientWidth - tab.offsetWidth) / 2,
        behavior: duration(1) ? 'smooth' : 'instant',
      });
  });

  // While a tab tap or drag scrolls the feed, intermediate positions must not reset the chosen status.
  let scrollingTo: { status: Status; until: number } | null = null;
  function feedScrolled() {
    if (!mobile || !board) return;
    const status = columns[Math.round(board.scrollLeft / Math.max(1, board.clientWidth))];
    if (scrollingTo) {
      if (status === scrollingTo.status || Date.now() > scrollingTo.until) scrollingTo = null;
      else return;
    }
    if (status && status !== feedStatus) {
      feedStatus = status;
      activeColumn = status;
    }
  }
  function showStatus(status: Status, instant = false) {
    const index = columns.indexOf(status);
    if (index < 0 || !board) return;
    const smooth = !instant && Boolean(duration(1));
    scrollingTo = smooth ? { status, until: Date.now() + 800 } : null;
    board.scrollTo({ left: index * board.clientWidth, behavior: smooth ? 'smooth' : 'instant' });
    feedStatus = status;
    activeColumn = status;
  }
  // Touch: press and hold lifts a card. Drag up/down to reorder, to a screen edge or status tab to change status, release to drop.
  // Native drag and drop is unavailable on touch screens.
  type TouchDrag = {
    item: Item;
    ghost: HTMLElement;
    dx: number;
    dy: number;
    x: number;
    y: number;
    startX: number;
    startY: number;
    moved: boolean;
    edgeSince: number;
    tabSince: number;
    tab: string;
  };
  let lifted = $state<TouchDrag | null>(null);
  let dragFrame = 0;
  function pressStart(event: PointerEvent, item: Item) {
    suppressClick = false;
    if (event.pointerType !== 'touch' || readonly || moving || lifted) return;
    const card = event.currentTarget as HTMLElement;
    const x = event.clientX,
      y = event.clientY;
    press = {
      x,
      y,
      timer: setTimeout(() => {
        press = undefined;
        void lift(card, item, x, y);
      }, 350),
    };
  }
  function pressMove(event: PointerEvent) {
    if (press && Math.hypot(event.clientX - press.x, event.clientY - press.y) > 8) pressEnd();
  }
  function pressEnd() {
    if (press) {
      clearTimeout(press.timer);
      press = undefined;
    }
  }
  async function lift(card: HTMLElement, item: Item, x: number, y: number) {
    if (!card.isConnected || !(await guardDraft())) return;
    suppressClick = true;
    navigator.vibrate?.(10);
    const rect = card.getBoundingClientRect();
    const ghost = card.cloneNode(true) as HTMLElement;
    for (const element of [ghost, ...ghost.querySelectorAll('[id]')]) element.removeAttribute('id');
    ghost.classList.add('card-ghost');
    ghost.setAttribute('aria-hidden', 'true');
    ghost.inert = true;
    Object.assign(ghost.style, {
      left: `${rect.left}px`,
      top: `${rect.top}px`,
      width: `${rect.width}px`,
      height: `${rect.height}px`,
    });
    ghost.style.transform = 'scale(1.03) rotate(-1deg)';
    document.body.append(ghost);
    ghost.animate([{ transform: 'none', boxShadow: 'none' }, {}], {
      duration: duration(160),
      easing: 'cubic-bezier(.2, .8, .2, 1)',
    });
    selectedId = item.id;
    lifted = {
      item,
      ghost,
      dx: x - rect.left,
      dy: y - rect.top,
      x,
      y,
      startX: x,
      startY: y,
      moved: false,
      edgeSince: 0,
      tabSince: 0,
      tab: '',
    };
    window.addEventListener('pointermove', dragMove, { passive: false });
    window.addEventListener('pointerup', dragEnd);
    window.addEventListener('pointercancel', dragCancel);
    window.addEventListener('touchmove', holdScroll, { passive: false });
    dragFrame = requestAnimationFrame(dragTick);
  }
  function holdScroll(event: TouchEvent) {
    if (lifted) event.preventDefault();
  }
  function dragMove(event: PointerEvent) {
    if (!lifted || event.pointerType !== 'touch') return;
    event.preventDefault();
    lifted.x = event.clientX;
    lifted.y = event.clientY;
    if (Math.hypot(lifted.x - lifted.startX, lifted.y - lifted.startY) > 10) lifted.moved = true;
    lifted.ghost.style.transform = `translate(${lifted.x - lifted.dx - parseFloat(lifted.ghost.style.left)}px, ${lifted.y - lifted.dy - parseFloat(lifted.ghost.style.top)}px) scale(1.03) rotate(-1deg)`;
    placeDrop();
  }
  function feedColumn() {
    return board?.querySelector<HTMLElement>(`[data-column="${feedStatus}"]`);
  }
  function placeDrop() {
    if (!lifted) return;
    const cards = [...(feedColumn()?.querySelectorAll<HTMLElement>('[data-card]') || [])].filter(
      (node) => node.dataset.card !== lifted!.item.id,
    );
    const next = cards.find((node) => {
      const r = node.getBoundingClientRect();
      return lifted!.y < r.top + r.height / 2;
    });
    const anchor = next || cards.at(-1);
    dropTarget = anchor?.dataset.card || '';
    dropBefore = Boolean(next);
  }
  function dragTick(now: number) {
    if (!lifted) return;
    const { x, y } = lifted;
    // Auto-scroll the column near its top and bottom edges.
    const scroller = feedColumn()?.querySelector<HTMLElement>('.column-scroll');
    if (scroller && lifted.moved) {
      const r = scroller.getBoundingClientRect();
      const speed =
        y < r.top + 56 ? -(r.top + 56 - y) / 4 : y > r.bottom - 72 ? (y - (r.bottom - 72)) / 4 : 0;
      if (speed) {
        scroller.scrollTop += Math.max(-14, Math.min(14, speed));
        placeDrop();
      }
    }
    // Hovering a status tab, or holding at a screen edge, switches status.
    const tab =
      (document.elementFromPoint(x, y) as HTMLElement | null)?.closest<HTMLElement>(
        '.status-tabs [data-status]',
      )?.dataset.status || '';
    if (tab !== lifted.tab) {
      lifted.tab = tab;
      lifted.tabSince = now;
    } else if (tab && tab !== feedStatus && now - lifted.tabSince > 260) {
      showStatus(tab as Status);
      lifted.tabSince = now;
      void tick().then(placeDrop);
    }
    const edge = !tab && lifted.moved ? (x < 28 ? -1 : x > innerWidth - 28 ? 1 : 0) : 0;
    if (!edge) lifted.edgeSince = 0;
    else if (!lifted.edgeSince) lifted.edgeSince = now;
    else if (now - lifted.edgeSince > 380) {
      const status = columns[columns.indexOf(feedStatus) + edge];
      if (status) {
        showStatus(status);
        navigator.vibrate?.(6);
        void tick().then(placeDrop);
      }
      lifted.edgeSince = now + 320;
    }
    dragFrame = requestAnimationFrame(dragTick);
  }
  function stopDrag() {
    cancelAnimationFrame(dragFrame);
    window.removeEventListener('pointermove', dragMove);
    window.removeEventListener('pointerup', dragEnd);
    window.removeEventListener('pointercancel', dragCancel);
    window.removeEventListener('touchmove', holdScroll);
    const drag = lifted;
    lifted = null;
    dropTarget = '';
    return drag;
  }
  function settle(ghost: HTMLElement) {
    ghost
      .animate(
        [{ opacity: 1 }, { opacity: 0, transform: `${ghost.style.transform || ''} scale(.98)` }],
        { duration: duration(140), easing: 'ease-out' },
      )
      .finished.then(
        () => ghost.remove(),
        () => ghost.remove(),
      );
  }
  function dragCancel() {
    const drag = stopDrag();
    if (drag) settle(drag.ghost);
  }
  async function dragEnd() {
    const target = dropTarget,
      before = dropBefore;
    const drag = stopDrag();
    if (!drag) return;
    const { item, ghost } = drag;
    const status = feedStatus;
    if (!drag.moved && status === item.status) {
      settle(ghost);
      return;
    }
    const anchor = items.find((i) => i.id === target);
    // Same column and same slot: nothing to save.
    const others = visible.filter((i) => i.status === status && i.id !== item.id);
    const slot = anchor ? others.indexOf(anchor) + (before ? 0 : 1) : others.length;
    if (
      status === item.status &&
      slot === visible.filter((i) => i.status === status).findIndex((i) => i.id === item.id)
    ) {
      settle(ghost);
      return;
    }
    // A card landing in another column arrives from the drop point; the ghost leaves once its replacement is animating.
    if (status !== item.status) pin(item.id, ghost.getBoundingClientRect(), () => ghost.remove());
    try {
      if (anchor) await onmove(item, anchor, before);
      else await onstatus(item.id, status);
    } finally {
      unpin(item.id);
      if (ghost.isConnected) settle(ghost);
      if (mobile) {
        activeColumn = status;
        showStatus(status, true);
      }
    }
  }
  export function focusColumn(status = activeColumn, row = preferredRow) {
    activeColumn = columns.includes(status) ? status : columns[0];
    const column = visible.filter((i) => i.status === activeColumn);
    const next = column[Math.min(Math.max(row, 0), column.length - 1)];
    selectedId = next?.id || '';
    const element = board.querySelector<HTMLElement>(
      next ? `#ticket-${next.id}` : `#column-${activeColumn}`,
    );
    element?.focus({ preventScroll: true });
    element?.scrollIntoView({ block: 'nearest', inline: 'nearest' });
    preferredRow = row;
  }

  export function focusCreate() {
    board.querySelector<HTMLTextAreaElement>('#quick-title')?.focus();
  }

  export function focusBoard() {
    const item = visible.find((i) => i.id === selectedId);
    focusColumn(
      item?.status || activeColumn,
      item ? visible.filter((i) => i.status === item.status).indexOf(item) : preferredRow,
    );
  }
  export async function move(direction: number) {
    const column = visible.filter((i) => i.status === activeColumn);
    const index = column.findIndex((i) => i.id === selectedId);
    const anchor = column[index + direction];
    if (selected && anchor) await onmove(selected, anchor, direction < 0);
  }

  export function handleKey(event: KeyboardEvent) {
    const target = event.target as HTMLElement;
    const key = event.key;
    const lower = key.toLowerCase();
    // Directional navigation only takes over on the board (or the unfocused page).
    if (
      !target.closest('.board') &&
      target !== document.body &&
      target !== document.documentElement
    )
      return;
    if (!event.altKey && /^[1-7]$/.test(key)) {
      event.preventDefault();
      const status = columns[Number(key) - 1];
      if (status) focusColumn(status, preferredRow);
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
      const row =
        key === 'G' || key === 'End'
          ? Math.max(0, visible.filter((i) => i.status === activeColumn).length - 1)
          : 0;
      focusColumn(activeColumn, row);
      return;
    }
    lastG = 0;
    if (['arrowdown', 'arrowup', 'j', 'k'].includes(lower)) {
      event.preventDefault();
      const direction = key === 'ArrowDown' || lower === 'j' ? 1 : -1;
      if (event.altKey || (event.shiftKey && ['J', 'K'].includes(key))) {
        void move(direction);
        return;
      }
      const column = visible.filter((i) => i.status === activeColumn);
      const index = column.findIndex((i) => i.id === selectedId);
      focusColumn(
        activeColumn,
        Math.min(Math.max(index + direction, 0), Math.max(0, column.length - 1)),
      );
    } else if (['arrowleft', 'arrowright', 'h', 'l'].includes(lower)) {
      event.preventDefault();
      const direction = key === 'ArrowRight' || lower === 'l' ? 1 : -1;
      if (event.altKey || (event.shiftKey && ['H', 'L'].includes(key))) {
        const status = selected && statuses[statuses.indexOf(selected.status) + direction];
        if (status) void onstatus(selectedId, status);
      } else {
        const status = columns[columns.indexOf(activeColumn) + direction];
        if (status) focusColumn(status, preferredRow);
      }
    } else if (key === 'Enter' && selected && !target.closest('button')) {
      event.preventDefault();
      void onopen(selected.id);
    }
  }
</script>

{#if mobile}<div class="status-tabs" role="group" bind:this={tabStrip} aria-label="Statuses">
    {#each columns as status}<button
        data-status={status}
        class:active={feedStatus === status}
        aria-current={feedStatus === status ? 'true' : undefined}
        onclick={() => showStatus(status)}
        ><span class={`status-icon ${status}`}><Icon name={status} size={14} /></span>{label(
          status,
        )}<span class="tab-count"
          >{visible.filter((i) => i.status === status).length}{cursors[status] ? '+' : ''}</span
        ></button
      >{/each}
  </div>{/if}
<div
  class="board"
  bind:this={board}
  onscroll={feedScrolled}
  aria-label="Items by status"
  aria-describedby="board-keyboard-hint"
>
  {#each columns as status}
    {@const columnItems = visible.filter((i) => i.status === status)}
    <!-- Native drag/drop is an additional input; the same action is available through Alt+arrows and the editor. -->
    <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
    <section
      data-column={status}
      class="kanban-column"
      class:drop-target={dragging && items.find((i) => i.id === dragging)?.status !== status}
      aria-label={`${label(status)} column`}
      ondragover={(event) => {
        if (dragging && !readonly) event.preventDefault();
      }}
      ondrop={(event) => {
        event.preventDefault();
        if (dragging) void onstatus(dragging, status);
      }}
    >
      <div class="column-header">
        <button
          id={`column-${status}`}
          class="column-focus"
          tabindex={activeColumn === status && !selected ? 0 : -1}
          onfocus={() => {
            activeColumn = status;
            selectedId = '';
          }}
          onclick={() => focusColumn(status, 0)}
          ><span class={`status-icon ${status}`}><Icon name={status} size={14} /></span>
          <h2>{label(status)}</h2>
          <span class="column-count" title="Loaded items"
            >{columnItems.length}{cursors[status] ? '+' : ''}</span
          ></button
        >{#if !readonly}<button
            class="icon-button column-add"
            aria-label={`Add item to ${label(status)}`}
            title="Add item (C)"
            onfocus={() => {
              activeColumn = status;
              selectedId = '';
            }}
            onclick={() => oncreate(status)}><Icon name="plus" size={15} /></button
          >{/if}
      </div>
      <div class="column-scroll">
        {#if quick.status === status}<form
            class="quick-create"
            onsubmit={(event) =>
              onquickcreate(event, (event.submitter as HTMLButtonElement)?.value === 'edit')}
          >
            <textarea
              id="quick-title"
              aria-label="New item title"
              placeholder="Item title"
              rows="2"
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
        <ul class="cards" aria-label={`${label(status)} items`}>
          {#each columnItems as item (item.id)}
            <li data-card={item.id} animate:flip={{ duration: duration(220) }} in:arrive>
              <button
                id={`ticket-${item.id}`}
                data-ticket={item.id}
                tabindex={selectedId === item.id ? 0 : -1}
                class="card"
                class:lifted={lifted?.item.id === item.id}
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
                  const current = items.find((i) => i.id === dragging);
                  if (current) void onmove(current, item, dropBefore);
                }}
                onfocus={() => {
                  selectedId = item.id;
                  activeColumn = status;
                  preferredRow = columnItems.indexOf(item);
                }}
                onclick={() => {
                  if (suppressClick) {
                    suppressClick = false;
                    return;
                  }
                  void onopen(item.id);
                }}
                onpointerdown={(event) => pressStart(event, item)}
                onpointermove={pressMove}
                onpointerup={pressEnd}
                onpointercancel={pressEnd}
                oncontextmenu={(event) => {
                  if (coarse) event.preventDefault();
                }}
                aria-label={`TK-${item.id}: ${item.title}`}
                aria-current={openId === item.id ? 'true' : undefined}
              >
                <span class="card-title">{item.title}</span>
                <div class="card-meta">
                  <span class={`item-type ${item.type}`} title={label(item.type)}
                    ><Icon name={item.type} size={12} /></span
                  ><span class="item-id">#{item.id}</span><span class="card-tags"
                    >{#each item.tags.slice(0, 2) as tag}<span>{tag}</span
                      >{/each}{#if item.tags.length > 2}<span>+{item.tags.length - 2}</span
                      >{/if}</span
                  ><span class="card-assignees"
                    >{#each item.assignees.slice(0, 2) as id}<span
                        class="mini-avatar"
                        style:--hue={avatarHue(id)}
                        title={users.find((u) => u.id === id)?.name || id}
                        >{initials(users.find((u) => u.id === id)?.name || id)}</span
                      >{/each}{#if item.assignees.length > 2}<span class="muted"
                        >+{item.assignees.length - 2}</span
                      >{/if}</span
                  >
                </div>
              </button>
            </li>
          {/each}
        </ul>
        {#if !hasLoaded}<p class="column-empty">
            Loading…
          </p>{:else if !columnItems.length && quick.status !== status}<p class="column-empty">
            {query ? 'No matches' : 'No tickets'}
          </p>{/if}
        {#if cursors[status]}<button
            class="load-more"
            disabled={busy}
            onfocus={() => {
              activeColumn = status;
              selectedId = '';
              preferredRow = Math.max(0, columnItems.length - 1);
            }}
            onclick={() => data.more(status)}>Load more</button
          >{/if}
      </div>
    </section>
  {/each}
</div>
<div class="board-footer" id="board-keyboard-hint">
  <span><kbd>↑ ↓ ← →</kbd> / <kbd>h j k l</kbd> navigate</span><span><kbd>Enter</kbd> open</span
  >{#if !readonly}<span><kbd>C</kbd> create</span>{/if}<span
    class="board-feedback"
    role="status"
    aria-live="polite">{moving ? 'Updating…' : announcement}</span
  ><button class="text-button" onclick={onhelp}><kbd>?</kbd> Shortcuts</button>
</div>
{#if !readonly && !openId && !(mobile && quick.status)}<button
    class="fab"
    aria-label="New"
    title="New ticket (C)"
    onclick={() => oncreate(mobile ? feedStatus : activeColumn)}
    ><Icon name="plus" size={22} strokeWidth={2} /></button
  >{/if}
