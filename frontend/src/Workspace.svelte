<script lang="ts">
  import { onMount, tick, untrack } from 'svelte';
  import { MediaQuery } from 'svelte/reactivity';
  import { APIError, message, statuses, label } from './api';
  import type { Item, ItemPatch, ItemType, User, Status } from './api';
  import type { EditorField } from './item-edit';
  import { BoardState } from './board-state.svelte';
  import { groupItems } from './items';
  import Board from './Board.svelte';
  import List from './List.svelte';
  import Toolbar from './Toolbar.svelte';
  import Icon from './Icon.svelte';
  import ItemEditor from './ItemEditor.svelte';
  import CommandMenu from './CommandMenu.svelte';
  import PeopleDialog from './PeopleDialog.svelte';
  import SetupDialog from './SetupDialog.svelte';
  import AccountDialog from './AccountDialog.svelte';
  import TagsDialog from './TagsDialog.svelte';
  import ShortcutsDialog from './ShortcutsDialog.svelte';
  import DeleteDialog from './DeleteDialog.svelte';
  import { panel } from './motion';

  let {
    user,
    expired,
    onuser,
    onlogout,
    onpasswordchanged,
  }: {
    user: User;
    expired: boolean;
    onuser: (user: User) => void;
    onlogout: () => Promise<void>;
    onpasswordchanged: () => void;
  } = $props();
  const initialURL = new URL(location.href);
  const data = new BoardState(
    untrack(() => user.id),
    (current) => onuser(current),
  );
  const ticket = data.ticket;
  data.filters = {
    status: statuses.find((status) => status === initialURL.searchParams.get('status')) || '',
    tag: initialURL.searchParams.get('tag') || '',
    assignee: initialURL.searchParams.get('assignee') || '',
  };
  ticket.id = initialURL.searchParams.get('item') || '';
  let selectedId = $state(ticket.id);
  let activeColumn = $state<Status>('todo');
  let query = $state(initialURL.searchParams.get('q') || '');
  let quick = $state<{ status: Status | null; title: string }>({ status: null, title: '' });
  type View = 'board' | 'list';
  const viewKey = 'tiki.view';
  const isView = (value: unknown): value is View => value === 'board' || value === 'list';
  let layout = $state<View>(readView());
  let creating = $state(false);
  let announcement = $state('');
  const narrow = new MediaQuery('(max-width: 700px)');
  const touch = new MediaQuery('(pointer: coarse)');
  const mobile = $derived(narrow.current);
  const coarse = $derived(touch.current);
  let modal = $state<'people' | 'setup' | 'tags' | 'shortcuts' | 'account' | 'delete' | null>(null);
  let accountView = $state<'menu' | 'profile' | 'password'>('menu');
  let dialogReturn: HTMLElement | null = null;
  let deleteTarget = $state<Item | null>(null);
  let preparingDelete = $state(false);
  type Menu = 'commands' | 'status' | 'assignee' | 'tags' | 'type';
  let palette = $state<Menu | null>(null);
  let menuItem = $state<Item | null>(null);
  let paletteReturn: HTMLElement | null = null;
  let board = $state<Board | List>(null!);
  let toolbar: Toolbar;
  let editor = $state<ItemEditor>();
  const dirty = $derived(Boolean(ticket.id && editor?.hasUnsavedChanges()));
  const readonly = $derived(user.role === 'viewer');
  const moving = $derived(Boolean(data.writing));
  const columns = $derived(data.columns);
  const visible = $derived.by(() => {
    const text = query.toLowerCase().trim().replace(/^#/, '').replace(/^tk-/, '');
    return text
      ? data.items.filter((item) =>
          `${item.id} ${item.title} ${item.tags.join(' ')}`.toLowerCase().includes(text),
        )
      : data.items;
  });
  const selected = $derived(visible.find((item) => item.id === selectedId));
  const grouped = $derived(groupItems(visible));
  const boardItems = $derived(columns.flatMap((status) => grouped[status]));
  const openedIndex = $derived(boardItems.findIndex((item) => item.id === ticket.id));

  $effect(() => {
    if (expired) {
      modal = null;
      palette = null;
      return;
    }
    untrack(() => data.start());
    return () => data.stop();
  });
  $effect(() => {
    if (user.role !== 'admin' && modal === 'people') modal = null;
  });
  // Server-side tag deletion can remove a filter without a toolbar event.
  $effect(() => {
    void data.filters.tag;
    untrack(() => updateURL());
  });
  function readView(): View {
    const requested = initialURL.searchParams.get('view');
    if (isView(requested)) return requested;
    try {
      const saved = localStorage.getItem(viewKey);
      return isView(saved) ? saved : 'board';
    } catch {
      return 'board';
    }
  }
  async function setLayout(next: View) {
    if (layout === next) return;
    layout = next;
    try {
      localStorage.setItem(viewKey, next);
    } catch {
      // The layout choice still applies to this tab and its URL.
    }
    updateURL();
    announcement = next === 'list' ? 'List view' : 'Board view';
    await tick();
    if (!document.activeElement?.closest('.detail, .toolbar')) board.focusBoard();
  }
  let actions = $derived([
    ...(!readonly ? [{ id: 'new', label: 'Create a ticket', hint: 'N', run: () => create() }] : []),
    { id: 'search', label: 'Search loaded tickets', hint: '/', run: () => toolbar.focusSearch() },
    {
      id: 'view',
      label: layout === 'list' ? 'Switch to board view' : 'Switch to list view',
      hint: 'V',
      run: () => void setLayout(layout === 'list' ? 'board' : 'list'),
    },
    { id: 'all', label: 'Clear all filters', run: clearFilters },
    { id: 'mine', label: 'Filter: assigned to me', run: () => setView('', user?.id || '') },
    {
      id: 'refresh',
      label: 'Refresh tickets and apply current order',
      hint: 'R',
      run: () => void data.refresh(),
    },
    ...(selected
      ? [
          {
            id: 'open',
            label: `Open TK-${selectedId}`,
            hint: '↵',
            run: () => void openItem(selectedId),
          },
        ]
      : []),
    ...(!readonly && (selected || (ticket.item && !ticket.deleted))
      ? [
          {
            id: 'delete',
            label: `Delete TK-${ticket.id && !ticket.deleted ? ticket.id : selectedId}…`,
            hint: 'Delete',
            run: () => void requestDelete(ticket.id && !ticket.deleted ? ticket.item : selected),
          },
        ]
      : []),
    ...(!readonly && selected
      ? [
          {
            id: 'comment',
            label: 'Comment on ticket',
            hint: 'C',
            run: () => void editSelected('comment'),
          },
          {
            id: 'edit',
            label: 'Edit ticket title',
            hint: 'E',
            run: () => void editSelected('title'),
          },
          {
            id: 'description',
            label: 'Edit description',
            hint: 'D',
            run: () => void editSelected('description'),
          },
          { id: 'assign', label: 'Assign ticket', hint: 'A', run: () => propertyMenu('assignee') },
          { id: 'tags', label: 'Edit tags', hint: 'T', run: () => propertyMenu('tags') },
          { id: 'type', label: 'Change ticket type', hint: 'Y', run: () => propertyMenu('type') },
          {
            id: 'me',
            label: selected.assignees.includes(user?.id || '') ? 'Unassign me' : 'Assign to me',
            hint: 'M',
            run: assignMe,
          },
          ...statuses.map((status) => ({
            id: `status-${status}`,
            label: `Set status: ${label(status)}`,
            run: () => void changeStatus(selectedId, status),
          })),
          {
            id: 'up',
            label: 'Move selected ticket up',
            hint: 'Alt ↑',
            run: () => void board.move(-1),
          },
          {
            id: 'down',
            label: 'Move selected ticket down',
            hint: 'Alt ↓',
            run: () => void board.move(1),
          },
        ]
      : []),
    ...columns.map((status, index) => ({
      id: `column-${status}`,
      label: `Go to ${label(status)} ${layout === 'list' ? 'group' : 'column'}`,
      hint: String(index + 1),
      run: () => board.focusColumn(status),
    })),
    { id: 'help', label: 'Keyboard shortcuts', hint: '?', run: showHelp },
    ...(user?.role === 'admin'
      ? [{ id: 'people', label: 'Manage people', run: () => showDialog('people') }]
      : []),
    { id: 'install', label: 'Install CLI & agent skill', run: () => showDialog('setup') },
    { id: 'profile', label: 'Edit my profile', run: () => showAccount('profile') },
    ...(!readonly ? [{ id: 'manage-tags', label: 'Manage tags', run: showTags }] : []),
    { id: 'password', label: 'Change my password', run: () => showAccount('password') },
    { id: 'logout', label: 'Sign out', run: () => void logout() },
  ]);

  let menuTitle = $derived(
    palette === 'commands'
      ? 'Commands'
      : `${palette === 'assignee' ? 'Assign' : palette === 'tags' ? 'Tags' : palette === 'type' ? 'Type' : 'Status'} · TK-${menuItem?.id}`,
  );
  let menuActions = $derived.by(() => {
    if (palette === 'commands' || !menuItem) return actions;
    const item = menuItem;
    if (palette === 'status')
      return statuses.map((status) => ({
        id: status,
        label: label(status),
        hint: item.status === status ? 'Current' : '',
        run: () => void changeStatus(item.id, status),
      }));
    if (palette === 'type')
      return (['task', 'bug', 'feature'] as ItemType[]).map((type) => ({
        id: type,
        label: label(type),
        hint: item.type === type ? 'Current' : '',
        run: () => void updateItem(item, { type }, `Type: ${label(type)}`),
      }));
    if (palette === 'assignee')
      return [
        {
          id: 'none',
          label: 'Unassign everyone',
          run: () => void updateItem(item, { remove_assignees: item.assignees }, 'Unassigned'),
        },
        ...data.users
          .filter((u) => !u.removed_at || item.assignees.includes(u.id))
          .map((u) => ({
            id: u.id,
            label: `${item.assignees.includes(u.id) ? 'Remove' : 'Assign'} ${u.name}${u.removed_at ? ' (removed)' : ''}${u.id === user?.id ? ' (me)' : ''}`,
            run: () =>
              void updateItem(
                item,
                { [item.assignees.includes(u.id) ? 'remove_assignees' : 'add_assignees']: [u.id] },
                'Assignment updated',
              ),
          })),
      ];
    return [
      { id: 'new-tag', label: 'Add a new tag…', run: () => void editSelected('tags') },
      ...[...new Set([...data.tags, ...item.tags])].map((tag) => ({
        id: `tag-${tag}`,
        label: `${item.tags.includes(tag) ? 'Remove' : 'Add'} #${tag}`,
        run: () =>
          void updateItem(
            item,
            { [item.tags.includes(tag) ? 'remove_tags' : 'add_tags']: [tag] },
            'Tags updated',
          ),
      })),
    ];
  });
  async function editSelected(field: EditorField = 'title') {
    const id = document.activeElement?.closest('.detail') ? ticket.id : selected?.id || ticket.id;
    if (!id) return;
    if (ticket.id !== id) await openItem(id, false);
    if (ticket.id !== id) return;
    await tick();
    editor?.focus(field);
  }

  async function propertyMenu(mode: Exclude<Menu, 'commands'>) {
    if (readonly) return;
    if (ticket.id && (document.activeElement?.closest('.detail') || selectedId === ticket.id)) {
      void editSelected(mode);
      return;
    }
    if (moving || !selected || !(await guardDraft())) return;
    menuItem = selected;
    showPalette(mode);
  }

  function assignMe() {
    if (!user || readonly) return;
    if (ticket.id && (document.activeElement?.closest('.detail') || selectedId === ticket.id)) {
      editor?.assignSelf();
      return;
    }
    if (selected)
      void updateItem(
        selected,
        {
          [selected.assignees.includes(user.id) ? 'remove_assignees' : 'add_assignees']: [user.id],
        },
        selected.assignees.includes(user.id) ? 'Unassigned from you' : 'Assigned to you',
      );
  }

  async function adjacentItem(direction: number) {
    const next = boardItems[openedIndex + direction];
    if (openedIndex >= 0 && next) {
      await openItem(next.id, false);
      await tick();
      editor?.focus();
    }
  }

  function updateURL(push = false) {
    const url = new URL(location.href);
    for (const [key, value] of Object.entries({
      q: query,
      status: data.filters.status,
      tag: data.filters.tag,
      assignee: data.filters.assignee,
      item: ticket.id,
      view: layout === 'list' ? 'list' : '',
    })) {
      if (value) url.searchParams.set(key, value);
      else url.searchParams.delete(key);
    }
    if (url.href !== location.href) history[push ? 'pushState' : 'replaceState']({}, '', url);
  }

  export async function guardDraft() {
    if (quick.title.trim()) {
      data.notice = 'Finish or cancel the new ticket first.';
      return false;
    }
    if (editor && !(await editor.flush())) {
      data.notice = 'Changes could not be saved. Resolve the item panel before leaving.';
      editor?.focus();
      return false;
    }
    data.notice = '';
    return true;
  }

  function showDialog(kind: typeof modal) {
    dialogReturn = document.activeElement as HTMLElement;
    modal = kind;
  }
  async function closeDialog() {
    modal = null;
    deleteTarget = null;
    await tick();
    if (dialogReturn?.isConnected && !dialogReturn.closest('[inert]')) dialogReturn.focus();
    else board.focusBoard();
  }
  function showAccount(view: typeof accountView = 'menu') {
    accountView = view;
    showDialog('account');
  }
  function showTags() {
    if (!readonly) showDialog('tags');
  }
  function showHelp() {
    showDialog('shortcuts');
  }
  function showPalette(mode: Menu = 'commands') {
    paletteReturn = document.activeElement as HTMLElement;
    palette = mode;
  }
  async function closePalette() {
    palette = null;
    await tick();
    if (paletteReturn?.isConnected) paletteReturn.focus();
    else board.focusBoard();
  }
  async function logout() {
    if (!(await guardDraft())) return;
    try {
      await onlogout();
    } catch (error) {
      data.error = message(error);
    }
  }

  async function openItem(id: string, focus = true) {
    if (ticket.id !== id) {
      if (!(await guardDraft())) return;
      quick.status = null;
      selectedId = id;
      const loading = ticket.open(id);
      updateURL(true);
      await loading;
    }
    if (focus && ticket.id === id) {
      await tick();
      editor?.focus(readonly || coarse ? undefined : 'title');
    }
  }
  async function closeDetails() {
    if (!(await guardDraft())) return;
    ticket.open('');
    updateURL(true);
    await tick();
    board.focusBoard();
  }
  async function create(status: Status = activeColumn) {
    if (!(await guardDraft()) || readonly) return;
    if (!columns.includes(status)) status = columns[0] || 'todo';
    ticket.open('');
    updateURL(true);
    quick.status = status;
    quick.title = '';
    activeColumn = status;
    await tick();
    board.focusCreate();
  }
  async function quickCreate(event: SubmitEvent, edit = false) {
    event.preventDefault();
    if (readonly || !quick.title.trim() || !quick.status || creating) return;
    creating = true;
    data.error = '';
    try {
      const item = await data.create({
        title: quick.title,
        type: 'task',
        status: quick.status,
        tags: data.filters.tag ? [data.filters.tag] : [],
        assignees: data.users.some(
          (person) => person.id === data.filters.assignee && !person.removed_at,
        )
          ? [data.filters.assignee]
          : [],
      });
      quick.title = '';
      selectedId = item.id;
      announcement = `Created TK-${item.id}`;
      if (edit) {
        quick.status = null;
        await openItem(item.id);
      } else {
        await tick();
        board.focusCreate();
      }
    } catch (error) {
      data.error =
        message(error) +
        (!(error instanceof APIError)
          ? ' Check the board before retrying; the item may have been created.'
          : '');
    } finally {
      creating = false;
    }
  }

  async function updateItem(item: Item, patch: ItemPatch, feedback: string) {
    if (!(await guardDraft()) || readonly || moving) return;
    item = data.itemsById.get(item.id) || item;
    data.error = '';
    try {
      const updated = await data.update(item.id, item.version, patch);
      activeColumn = updated.status;
      selectedId = item.id;
      await tick();
      board.focusBoard();
      announcement = `TK-${item.id} · ${feedback}`;
    } catch (error) {
      data.error = message(error);
    }
  }
  async function changeStatus(id: string, status: Status) {
    const item = data.itemsById.get(id);
    if (item && item.status !== status)
      await updateItem(item, { status }, `Moved to ${label(status)}`);
  }
  async function moveTo(item: Item, anchor: Item, before: boolean) {
    if (item.id === anchor.id || !(await guardDraft()) || readonly || moving) return;
    item = data.itemsById.get(item.id) || item;
    data.error = '';
    try {
      const updated = await data.move(item, anchor, before);
      activeColumn = updated.status;
      selectedId = updated.id;
      await tick();
      board.focusBoard();
      announcement = `TK-${updated.id} moved ${before ? 'before' : 'after'} TK-${anchor.id}`;
    } catch (error) {
      data.error = message(error);
    }
  }
  async function requestDelete(item: Item | null | undefined) {
    if (!item || readonly || expired || deleteTarget || (moving && item.id !== ticket.id)) return;
    if (item.id !== ticket.id && !(await guardDraft())) return;
    deleteTarget = item;
    preparingDelete = true;
    showDialog('delete');
    await tick();
    if (item.id === ticket.id) await editor?.settle();
    if (modal !== 'delete') return;
    deleteTarget =
      (ticket.item?.id === item.id ? ticket.item : data.itemsById.get(item.id)) || item;
    preparingDelete = false;
  }
  async function deleted(id: string) {
    announcement = `Deleted TK-${id}`;
    updateURL();
    await closeDialog();
    board.focusBoard();
  }

  async function clearFilters() {
    if (await guardDraft()) {
      data.filters.tag = '';
      await setView('', '');
    }
  }
  async function setView(status: Status | '', assignee: string) {
    if (!(await guardDraft())) return;
    data.filters.status = status;
    data.filters.assignee = assignee;
    query = '';
    filterChanged();
  }
  function filterChanged() {
    if (
      quick.status &&
      quick.title.trim() &&
      data.filters.status &&
      data.filters.status !== quick.status
    )
      data.filters.status = quick.status;
    updateURL(true);
    void data.reset();
  }
  function keyboard(event: KeyboardEvent) {
    if (!user || expired || event.isComposing || event.defaultPrevented) return;
    const target = event.target as HTMLElement;
    const editing = target.closest(
      'input, textarea, select, [role="combobox"], [contenteditable]:not([contenteditable="false"])',
    );
    if (modal) return;
    if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'k' && !event.altKey) {
      event.preventDefault();
      if (palette) void closePalette();
      else showPalette();
      return;
    }
    if (palette) return;
    if (event.key === 'F6') {
      event.preventDefault();
      if (target.closest('.detail')) {
        if (matchMedia('(max-width: 800px)').matches) void closeDetails();
        else board.focusBoard();
      } else if (ticket.id) editor?.focus();
      else void toolbar.focusSearch();
      return;
    }
    if (event.key === 'Escape') {
      event.preventDefault();
      if (quick.status && !creating) {
        quick.status = null;
        quick.title = '';
        void tick().then(() => board.focusBoard());
      } else if (editing && target.closest('.detail')) editor?.focus();
      else if (target.matches('.search-field input') || target.closest('.toolbar'))
        board.focusBoard();
      else if (ticket.id) void closeDetails();
      else board.focusBoard();
      return;
    }
    if (target.matches('.search-field input') && ['Enter', 'ArrowDown'].includes(event.key)) {
      event.preventDefault();
      board.focusColumn(boardItems[0]?.status || activeColumn, 0);
      return;
    }
    if (editing || event.ctrlKey || event.metaKey) return;
    const key = event.key;
    if (
      key === 'Delete' &&
      !event.altKey &&
      !event.shiftKey &&
      !event.repeat &&
      (target.closest('.detail, .board, .list-view') || target === document.body)
    ) {
      event.preventDefault();
      void requestDelete(target.closest('.detail') ? ticket.item : selected);
      return;
    }
    if (!event.altKey && ['/', 'n', '?', 'r', 'v'].includes(key)) {
      event.preventDefault();
      if (key === 'v' && !event.repeat) void setLayout(layout === 'list' ? 'board' : 'list');
      if (key === '/') void toolbar.focusSearch(true);
      if (key === 'n' && !event.repeat) void create();
      if (key === '?') void showHelp();
      if (key === 'r') void data.refresh();
      return;
    }
    if (
      !event.altKey &&
      !event.shiftKey &&
      ['c', 'e', 'i', 'd', 'a', 's', 't', 'y', 'm'].includes(key)
    ) {
      event.preventDefault();
      if (event.repeat || readonly) return;
      if (key === 'c') void editSelected('comment');
      if (key === 'e' || key === 'i') void editSelected();
      if (key === 'd') void editSelected('description');
      if (key === 'm') assignMe();
      if (key === 'a') propertyMenu('assignee');
      if (key === 's') propertyMenu('status');
      if (key === 't') propertyMenu('tags');
      if (key === 'y') propertyMenu('type');
      return;
    }
    if (target.closest('.detail')) {
      if (!event.altKey && ['j', 'k', '[', ']'].includes(key)) {
        event.preventDefault();
        void adjacentItem(['j', ']'].includes(key) ? 1 : -1);
      }
      return;
    }
    board.handleKey(event);
  }

  onMount(() => {
    const viewport = window.visualViewport;
    const keyboardInset = () => {
      if (viewport)
        document.documentElement.style.setProperty(
          '--keyboard',
          `${Math.max(0, Math.round(innerHeight - viewport.height - viewport.offsetTop))}px`,
        );
    };
    keyboardInset();
    viewport?.addEventListener('resize', keyboardInset);
    viewport?.addEventListener('scroll', keyboardInset);
    return () => {
      viewport?.removeEventListener('resize', keyboardInset);
      viewport?.removeEventListener('scroll', keyboardInset);
      document.documentElement.style.removeProperty('--keyboard');
    };
  });

  function visibilityChanged() {
    if (expired) return;
    if (document.hidden) data.pause();
    else data.start();
  }
  async function restoreURL() {
    if (!(await guardDraft())) {
      updateURL();
      return;
    }
    const url = new URL(location.href);
    query = url.searchParams.get('q') || '';
    data.filters = {
      status: statuses.find((status) => status === url.searchParams.get('status')) || '',
      tag: url.searchParams.get('tag') || '',
      assignee: url.searchParams.get('assignee') || '',
    };
    const id = url.searchParams.get('item') || '';
    const requested = url.searchParams.get('view');
    layout = isView(requested) ? requested : 'board';
    quick.status = null;
    if (id !== ticket.id) {
      selectedId = id;
      void ticket.open(id);
    }
    void data.reset();
  }
</script>

<svelte:document onvisibilitychange={visibilityChanged} />
<svelte:window
  onkeydown={keyboard}
  ononline={() => {
    if (!expired) data.start();
  }}
  onpopstate={restoreURL}
  onbeforeunload={(event) => {
    if (
      dirty ||
      quick.title.trim() ||
      Object.values(ticket.timeline.drafts).some((draft) => draft.text || draft.pending.length)
    )
      event.preventDefault();
  }}
/>

<main class="app" inert={expired}>
  <Toolbar
    bind:this={toolbar}
    {data}
    {user}
    {mobile}
    {readonly}
    view={layout}
    onview={setLayout}
    bind:query
    accountOpen={modal === 'account'}
    onfilters={filterChanged}
    onquery={() => updateURL()}
    onclear={clearFilters}
    onpeople={() => showDialog('people')}
    oninstall={() => showDialog('setup')}
    oncommands={() => showPalette()}
    onhelp={showHelp}
    onaccount={() => showAccount()}
    ontags={showTags}
  />
  {#if data.error}
    <div class="board-alert error-banner" role="alert">
      <span>{data.error}</span><button class="text-button" onclick={() => data.refresh()}
        >Retry</button
      >
    </div>
  {/if}
  {#if data.notice}
    <div class="board-alert notice-banner" role="status">
      {data.notice}<button
        class="icon-button"
        aria-label="Dismiss notice"
        onclick={() => (data.notice = '')}><Icon name="close" size={13} /></button
      >
    </div>
  {/if}
  {#if layout === 'list'}
    <List
      bind:this={board}
      {data}
      {grouped}
      {query}
      {readonly}
      {dirty}
      {coarse}
      bind:selectedId
      bind:activeColumn
      bind:quick
      {creating}
      {announcement}
      onopen={openItem}
      oncreate={create}
      onquickcreate={quickCreate}
      onmove={moveTo}
      onstatus={changeStatus}
      onhelp={showHelp}
    />
  {:else}
    <Board
      bind:this={board}
      {data}
      {visible}
      {grouped}
      {query}
      {readonly}
      {dirty}
      {mobile}
      {coarse}
      bind:selectedId
      bind:activeColumn
      bind:quick
      {creating}
      {announcement}
      onopen={openItem}
      oncreate={create}
      onquickcreate={quickCreate}
      onmove={moveTo}
      onstatus={changeStatus}
      {guardDraft}
      onhelp={showHelp}
    />
  {/if}
  {#if ticket.id}
    <div class="detail-shell" transition:panel>
      {#if ticket.item}
        {#key ticket.id}
          <ItemEditor
            bind:this={editor}
            currentUserId={user.id}
            timeline={ticket.timeline}
            item={ticket.item}
            users={data.users}
            tags={data.tags}
            readonly={readonly || expired}
            deleted={ticket.deleted}
            suspended={modal === 'delete'}
            onmissing={() => (ticket.deleted = true)}
            ondelete={() => requestDelete(ticket.item)}
            canPrevious={openedIndex > 0}
            canNext={openedIndex >= 0 && openedIndex < boardItems.length - 1}
            onnavigate={adjacentItem}
            onclose={closeDetails}
            onpersist={(id, version, patch) => data.update(id, version, patch)}
            onreload={() => ticket.reload()}
          />
        {/key}
      {:else}
        <aside class="detail detail-loading" tabindex="-1" aria-label={`Item ${ticket.id}`}>
          <div class="detail-top">
            <span>#{ticket.id}</span><button
              class="icon-button detail-close"
              aria-label="Close item"
              onclick={closeDetails}><Icon name="close" size={16} /></button
            >
          </div>
          <p>
            {ticket.loading
              ? 'Loading…'
              : ticket.deleted
                ? 'This ticket was deleted or is no longer available.'
                : ticket.error || 'Could not load item.'}
          </p>
          {#if !ticket.loading && !ticket.deleted}<button
              class="small-button"
              onclick={() => ticket.reload()}>Retry</button
            >{/if}
        </aside>
      {/if}
    </div>
  {/if}
</main>

{#if modal === 'delete' && deleteTarget}
  <DeleteDialog
    {data}
    item={deleteTarget}
    preparing={preparingDelete}
    {readonly}
    onclose={closeDialog}
    ondeleted={deleted}
  />
{:else if modal === 'setup'}
  <SetupDialog email={user.email} onclose={closeDialog} />
{:else if modal === 'account'}
  <AccountDialog
    {user}
    initialView={accountView}
    onchange={(changed) => data.userChanged(changed)}
    onclose={closeDialog}
    onlogout={async () => {
      await closeDialog();
      await logout();
    }}
    beforePasswordChange={guardDraft}
    {onpasswordchanged}
  />
{:else if modal === 'tags'}
  <TagsDialog
    {readonly}
    beforeDelete={guardDraft}
    ondeleted={(name) => data.tagDeleted(name)}
    onclose={closeDialog}
  />
{:else if modal === 'people' && user.role === 'admin'}
  <PeopleDialog
    users={data.users}
    currentUserId={user.id}
    onchange={(changed) => data.userChanged(changed)}
    onclose={closeDialog}
  />
{:else if modal === 'shortcuts'}
  <ShortcutsDialog view={layout} onclose={closeDialog} />
{/if}
{#if palette}<CommandMenu actions={menuActions} title={menuTitle} onclose={closePalette} />{/if}
