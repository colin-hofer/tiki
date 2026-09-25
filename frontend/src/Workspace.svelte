<script lang="ts">
  import { onMount, tick, untrack } from 'svelte';
  import { APIError, message, statuses, label } from './api';
  import type { Item, ItemPatch, ItemType, User, Status } from './api';
  import type { EditorField } from './item-edit';
  import { BoardState } from './board.svelte';
  import Board from './Board.svelte';
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
  data.filters = {
    status: statuses.find((status) => status === initialURL.searchParams.get('status')) || '',
    tag: initialURL.searchParams.get('tag') || '',
    assignee: initialURL.searchParams.get('assignee') || '',
  };
  data.openId = initialURL.searchParams.get('item') || '';
  let selectedId = $state(data.openId);
  let activeColumn = $state<Status>('todo');
  let query = $state(initialURL.searchParams.get('q') || '');
  let quick = $state<{ status: Status | null; title: string }>({ status: null, title: '' });
  let creating = $state(false);
  let dirty = $state(false);
  let announcement = $state('');
  let mobile = $state(false);
  let coarse = $state(false);
  let modal = $state<'people' | 'setup' | 'tags' | 'shortcuts' | 'account' | 'delete' | null>(null);
  let accountView = $state<'menu' | 'profile' | 'password'>('menu');
  let dialogReturn: HTMLElement | null = null;
  let deleteTarget = $state<Item | null>(null);
  let preparingDelete = $state(false);
  type Menu = 'commands' | 'status' | 'assignee' | 'tags' | 'type';
  let palette = $state<Menu | null>(null);
  let menuItem = $state<Item | null>(null);
  let paletteReturn: HTMLElement | null = null;
  let board: Board;
  let toolbar: Toolbar;
  let editor = $state<ItemEditor>();
  const readonly = $derived(user.role === 'viewer');
  const moving = $derived(Boolean(data.writing));
  const columns = $derived(data.columns);
  const visible = $derived.by(() => {
    const text = query.toLowerCase().trim().replace(/^#/, '').replace(/^tk-/, '');
    return data.items.filter(
      (item) =>
        !text || `${item.id} ${item.title} ${item.tags.join(' ')}`.toLowerCase().includes(text),
    );
  });
  const selected = $derived(visible.find((item) => item.id === selectedId));
  const boardItems = $derived(
    columns.flatMap((status) => visible.filter((item) => item.status === status)),
  );
  const openedIndex = $derived(boardItems.findIndex((item) => item.id === data.openId));

  $effect(() => {
    if (expired) {
      data.stop();
      modal = null;
      palette = null;
    } else untrack(() => data.start());
  });
  $effect(() => {
    if (user.role !== 'admin' && modal === 'people') modal = null;
  });
  // Server-side tag deletion can remove a filter without a toolbar event.
  $effect(() => {
    void data.filters.tag;
    untrack(() => updateURL());
  });
  let actions = $derived([
    ...(!readonly ? [{ id: 'new', label: 'Create a ticket', hint: 'C', run: () => create() }] : []),
    { id: 'search', label: 'Search loaded tickets', hint: '/', run: () => toolbar.focusSearch() },
    { id: 'all', label: 'Clear all filters', run: clearFilters },
    { id: 'mine', label: 'Filter: assigned to me', run: () => setView('', user?.id || '') },
    {
      id: 'refresh',
      label: 'Refresh tickets and apply current order',
      hint: 'R',
      run: () => void data.refresh({ order: true }),
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
    ...(!readonly && (selected || (data.detail && !data.detailDeleted))
      ? [
          {
            id: 'delete',
            label: `Delete TK-${data.openId && !data.detailDeleted ? data.openId : selectedId}…`,
            hint: 'Delete',
            run: () =>
              void requestDelete(data.openId && !data.detailDeleted ? data.detail : selected),
          },
        ]
      : []),
    ...(!readonly && selected
      ? [
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
      label: `Go to ${label(status)} column`,
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
    const id = document.activeElement?.closest('.detail')
      ? data.openId
      : selected?.id || data.openId;
    if (!id) return;
    if (data.openId !== id) await openItem(id, false);
    if (data.openId !== id) return;
    await tick();
    editor?.focus(field);
  }

  async function propertyMenu(mode: Exclude<Menu, 'commands'>) {
    if (readonly) return;
    if (data.openId && (document.activeElement?.closest('.detail') || selectedId === data.openId)) {
      void editSelected(mode);
      return;
    }
    if (moving || !selected || !(await guardDraft())) return;
    menuItem = selected;
    showPalette(mode);
  }

  function assignMe() {
    if (!user || readonly) return;
    if (data.openId && (document.activeElement?.closest('.detail') || selectedId === data.openId)) {
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
      item: data.openId,
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
    if (data.openId !== id) {
      if (!(await guardDraft())) return;
      quick.status = null;
      selectedId = id;
      const loading = data.open(id);
      updateURL(true);
      await loading;
    }
    if (focus && data.openId === id) {
      await tick();
      editor?.focus(readonly || coarse ? undefined : 'title');
    }
  }
  async function closeDetails() {
    if (!(await guardDraft())) return;
    data.open('');
    updateURL(true);
    await tick();
    board.focusBoard();
  }
  async function create(status: Status = activeColumn) {
    if (!(await guardDraft()) || readonly) return;
    if (!columns.includes(status)) status = columns[0] || 'todo';
    data.open('');
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
    item = data.items.find((current) => current.id === item.id) || item;
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
    const item = data.items.find((item) => item.id === id);
    if (item && item.status !== status)
      await updateItem(item, { status }, `Moved to ${label(status)}`);
  }
  async function moveTo(item: Item, anchor: Item, before: boolean) {
    if (item.id === anchor.id || !(await guardDraft()) || readonly || moving) return;
    item = data.items.find((current) => current.id === item.id) || item;
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
    if (!item || readonly || expired || deleteTarget || (moving && item.id !== data.openId)) return;
    if (item.id !== data.openId && !(await guardDraft())) return;
    deleteTarget = item;
    preparingDelete = true;
    showDialog('delete');
    await tick();
    if (item.id === data.openId) await editor?.settle();
    if (modal !== 'delete') return;
    deleteTarget =
      (data.detail?.id === item.id
        ? data.detail
        : data.items.find((current) => current.id === item.id)) || item;
    preparingDelete = false;
  }
  async function deleted(id: string) {
    dirty = false;
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
    void data.refresh({ order: true, reset: true });
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
      } else if (data.openId) editor?.focus();
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
      else if (data.openId) void closeDetails();
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
      (target.closest('.detail, .board') || target === document.body)
    ) {
      event.preventDefault();
      void requestDelete(target.closest('.detail') ? data.detail : selected);
      return;
    }
    if (!event.altKey && ['/', 'c', '?', 'r'].includes(key)) {
      event.preventDefault();
      if (key === '/') void toolbar.focusSearch(true);
      if (key === 'c' && !event.repeat) void create();
      if (key === '?') void showHelp();
      if (key === 'r') void data.refresh({ order: true });
      return;
    }
    if (
      !event.altKey &&
      !event.shiftKey &&
      ['e', 'i', 'd', 'a', 's', 't', 'y', 'm'].includes(key)
    ) {
      event.preventDefault();
      if (event.repeat || readonly) return;
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
    const narrow = matchMedia('(max-width: 700px)'),
      touch = matchMedia('(pointer: coarse)');
    const media = () => {
      mobile = narrow.matches;
      coarse = touch.matches;
    };
    const viewport = window.visualViewport;
    const keyboardInset = () => {
      if (viewport)
        document.documentElement.style.setProperty(
          '--keyboard',
          `${Math.max(0, Math.round(innerHeight - viewport.height - viewport.offsetTop))}px`,
        );
    };
    media();
    keyboardInset();
    const visibility = () => {
      if (!expired) {
        if (document.hidden) data.pause();
        else data.start();
      }
    };
    const online = () => {
      if (!expired) data.start();
    };
    const unload = (event: BeforeUnloadEvent) => {
      if (dirty || quick.title.trim()) event.preventDefault();
    };
    const popstate = async () => {
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
      quick.status = null;
      if (id !== data.openId) {
        selectedId = id;
        void data.open(id);
      }
      void data.refresh({ order: true, reset: true });
    };
    narrow.addEventListener('change', media);
    touch.addEventListener('change', media);
    viewport?.addEventListener('resize', keyboardInset);
    viewport?.addEventListener('scroll', keyboardInset);
    document.addEventListener('visibilitychange', visibility);
    window.addEventListener('online', online);
    window.addEventListener('beforeunload', unload);
    window.addEventListener('popstate', popstate);
    return () => {
      data.stop();
      narrow.removeEventListener('change', media);
      touch.removeEventListener('change', media);
      viewport?.removeEventListener('resize', keyboardInset);
      viewport?.removeEventListener('scroll', keyboardInset);
      document.documentElement.style.removeProperty('--keyboard');
      document.removeEventListener('visibilitychange', visibility);
      window.removeEventListener('online', online);
      window.removeEventListener('beforeunload', unload);
      window.removeEventListener('popstate', popstate);
    };
  });
</script>

<svelte:window onkeydown={keyboard} />

<main class="app" inert={expired}>
  <Toolbar
    bind:this={toolbar}
    {data}
    {user}
    {mobile}
    {readonly}
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
      <span>{data.error}</span><button
        class="text-button"
        onclick={() => data.refresh({ order: true })}>Retry</button
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
  <Board
    bind:this={board}
    {data}
    {visible}
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
  {#if data.openId}
    <div class="detail-shell" transition:panel>
      {#if data.detail}
        {#key data.openId}
          <ItemEditor
            bind:this={editor}
            currentUserId={user.id}
            item={data.detail}
            users={data.users}
            tags={data.tags}
            readonly={readonly || expired}
            deleted={data.detailDeleted}
            suspended={modal === 'delete'}
            onmissing={() => (data.detailDeleted = true)}
            ondelete={() => requestDelete(data.detail)}
            canPrevious={openedIndex > 0}
            canNext={openedIndex >= 0 && openedIndex < boardItems.length - 1}
            onnavigate={adjacentItem}
            onclose={closeDetails}
            onpersist={(id, version, patch) => data.update(id, version, patch)}
            onreload={() => data.loadDetail()}
            ondirty={(value) => (dirty = value)}
          />
        {/key}
      {:else}
        <aside class="detail detail-loading" tabindex="-1" aria-label={`Item ${data.openId}`}>
          <div class="detail-top">
            <span>#{data.openId}</span><button
              class="icon-button detail-close"
              aria-label="Close item"
              onclick={closeDetails}><Icon name="close" size={16} /></button
            >
          </div>
          <p>
            {data.detailLoading
              ? 'Loading…'
              : data.detailDeleted
                ? 'This ticket was deleted or is no longer available.'
                : 'Could not load item.'}
          </p>
          {#if !data.detailLoading && !data.detailDeleted}<button
              class="small-button"
              onclick={() => data.loadDetail()}>Retry</button
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
  <ShortcutsDialog onclose={closeDialog} />
{/if}
{#if palette}<CommandMenu actions={menuActions} title={menuTitle} onclose={closePalette} />{/if}
