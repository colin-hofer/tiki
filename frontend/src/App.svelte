<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { api, APIError, directory, hasSession, setSession, statuses, label, initials } from './api';
  import type { Item, Page, Session, User, Status } from './api';
  import Icon from './Icon.svelte';
  import ItemEditor from './ItemEditor.svelte';
  import CommandMenu from './CommandMenu.svelte';

  const initialURL = new URL(location.href);
  let user = $state<User | null>(null);
  let authExpired = $state(false);
  let checking = $state(hasSession());
  let email = $state('');
  let password = $state('');
  let signingIn = $state(false);
  let loginError = $state('');
  let users = $state<User[]>([]);
  let tags = $state<string[]>([]);
  let items = $state<Item[]>([]);
  let cursors = $state<Partial<Record<Status, string>>>({});
  let selectedId = $state(initialURL.searchParams.get('item') || '');
  let openId = $state(initialURL.searchParams.get('item') || '');
  let detail = $state<Item | null>(null);
  let detailLoading = $state(false);
  let quickStatus = $state<Status | null>(null);
  let quickTitle = $state('');
  let creating = $state(false);
  let activeColumn = $state<Status>('todo');
  let dragging = $state('');
  let dirty = $state(false);
  let query = $state(initialURL.searchParams.get('q') || '');
  let filterStatus = $state(initialURL.searchParams.get('status') || '');
  let filterTag = $state(initialURL.searchParams.get('tag') || '');
  let filterAssignee = $state(initialURL.searchParams.get('assignee') || '');
  let busy = $state(false);
  let hasLoaded = $state(false);
  let connection = $state<'connecting' | 'live' | 'offline' | 'paused'>('connecting');
  let lastSync = $state('');
  let error = $state('');
  let notice = $state('');
  let orderChanged = $state(false);
  let palette = $state(false);
  let help = $state(false);
  let moving = $state(false);
  let searchInput = $state<HTMLInputElement>(null!);
  let helpDialog = $state<HTMLDialogElement>(null!);
  let paletteReturn: HTMLElement | null = null;
  let loadedPages: Partial<Record<Status, number>> = {};
  let generation = 0;
  let detailGeneration = 0;
  let controller: AbortController | null = null;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let failures = 0;
  let syncing = false;
  let lastDirectory = 0;
  let stopped = false;

  let readonly = $derived(user?.role === 'viewer');
  let visible = $derived(items.filter(item => {
    const text = query.toLowerCase().trim().replace(/^#/, '').replace(/^tk-/, '');
    return !text || `${item.id} ${item.title} ${item.tags.join(' ')}`.toLowerCase().includes(text);
  }));
  let columns = $derived(statuses.filter(s => !filterStatus || s === filterStatus));
  let actions = $derived([
    ...(!readonly ? [{ id: 'new', label: 'Create a ticket', hint: 'C', run: () => create() }] : []),
    { id: 'search', label: 'Search loaded tickets', hint: '/', run: () => searchInput?.focus() },
    { id: 'all', label: 'Go to all tickets', run: () => setView('', '') },
    { id: 'mine', label: 'Go to my queue', run: () => setView('', user?.id || '') },
    { id: 'refresh', label: 'Refresh tickets and apply current order', hint: 'R', run: () => void refresh(true) },
    ...(selectedId ? [{ id: 'open', label: `Open TK-${selectedId}`, hint: '↵', run: () => void openItem(selectedId) }] : []),
    ...(!readonly && selectedId ? [
      ...statuses.map(status => ({ id: `status-${status}`, label: `Set status: ${label(status)}`, run: () => void changeStatus(selectedId, status) })),
      { id: 'up', label: 'Move selected ticket up', hint: 'Alt ↑', run: () => void move(-1) },
      { id: 'down', label: 'Move selected ticket down', hint: 'Alt ↓', run: () => void move(1) },
    ] : []),
    { id: 'help', label: 'Keyboard shortcuts', hint: '?', run: showHelp },
    { id: 'logout', label: 'Sign out', run: () => void logout() },
  ]);

  function updateURL(push = false) {
    const url = new URL(location.href);
    for (const [key, value] of Object.entries({ q: query, status: filterStatus, tag: filterTag, assignee: filterAssignee, item: openId })) {
      if (value) url.searchParams.set(key, value); else url.searchParams.delete(key);
    }
    if (url.href !== location.href) history[push ? 'pushState' : 'replaceState']({}, '', url);
  }

  function guardDraft() {
    if (!dirty && !quickTitle.trim()) { notice = ''; return true; }
    notice = 'Save or discard the open draft first.';
    return false;
  }

  async function login(event: SubmitEvent) {
    event.preventDefault(); signingIn = true; loginError = '';
    try {
      const session = await api<Session>('/auth/login', 'POST', { email, password });
      if (user && user.id !== session.user.id) {
        items = []; detail = null; openId = ''; creating = false; dirty = false; selectedId = ''; filterAssignee = '';
      }
      setSession(session.session_token); user = session.user; password = ''; authExpired = false;
      await start();
    } catch (e) { loginError = message(e); }
    finally { signingIn = false; }
  }

  function message(e: unknown) { return e instanceof Error ? e.message : 'Something went wrong. Please try again.'; }

  async function logout() {
    if (!guardDraft()) return;
    try { await api('/auth/logout', 'POST'); }
    catch (e) { if (!(e instanceof APIError && e.status === 401)) { error = message(e); return; } }
    clearTimeout(timer); controller?.abort(); generation++; detailGeneration++;
    setSession(''); user = null; authExpired = false; items = []; detail = null; openId = ''; selectedId = ''; creating = false;
    users = []; tags = []; password = ''; hasLoaded = false; updateURL();
  }

  async function loadDirectory() {
    const actor = user?.id;
    const [nextUsers, nextTags] = await Promise.all([directory<User>('/users', 'users'), directory<string>('/tags', 'tags')]);
    if (actor !== user?.id || authExpired) return;
    users = nextUsers; tags = nextTags; lastDirectory = Date.now();
  }

  function schedule() {
    clearTimeout(timer);
    if (!stopped && user && !authExpired) timer = setTimeout(poll, Math.min(30000, 2000 * 2 ** failures));
  }

  async function poll() {
    if (stopped || !user || authExpired) return;
    if (document.hidden) { connection = 'paused'; schedule(); return; }
    if (!syncing && !busy && !moving && !creating) await refresh(false);
    schedule();
  }

  async function start() {
    loadedPages = {};
    await loadDirectory().catch(e => { error = message(e); });
    await refresh(true);
    if (openId && !detail) await fetchDetail(openId);
    schedule();
  }

  async function refresh(applyOrder = false, reset = false) {
    if (!user || authExpired) return;
    if (reset) loadedPages = {};
    controller?.abort(); controller = new AbortController();
    const signal = controller.signal;
    const own = ++generation;
    const targetId = openId;
    syncing = true;
    if (applyOrder || !hasLoaded) busy = true;
    try {
      // Page each column independently: a global first page can hide entire statuses.
      const pages = await Promise.all(columns.map(async status => {
        const params = new URLSearchParams({ limit: '50', status });
        if (filterTag) params.set('tag', filterTag);
        if (filterAssignee) params.set('assignee', filterAssignee);
        const rows: Item[] = [];
        let nextCursor = '';
        for (let i = 0; i < (loadedPages[status] || 1); i++) {
          if (nextCursor) params.set('cursor', nextCursor);
          const page = await api<Page>(`/items?${params}`, 'GET', undefined, signal);
          rows.push(...page.items); nextCursor = page.next_cursor || '';
          if (!nextCursor) break;
        }
        return { status, rows, cursor: nextCursor };
      }));
      const next = pages.flatMap(p => p.rows);
      if (own !== generation) return;
      const unique = [...new Map(next.map(i => [i.id, i])).values()];
      const focusedId = document.activeElement?.getAttribute('data-ticket');
      if (applyOrder || !hasLoaded) { items = unique; orderChanged = false; }
      else {
        const byId = new Map(unique.map(i => [i.id, i]));
        const stable = items.filter(i => byId.has(i.id)).map(i => byId.get(i.id)!);
        const oldIds = new Set(stable.map(i => i.id));
        const merged = [...stable, ...unique.filter(i => !oldIds.has(i.id))];
        orderChanged = merged.some((i, index) => i.id !== unique[index]?.id);
        items = merged;
      }
      cursors = Object.fromEntries(pages.map(p => [p.status, p.cursor]));
      if (!selectedId && items.length) selectedId = items[0].id;
      if (focusedId) { await tick(); document.getElementById(`ticket-${focusedId}`)?.focus({ preventScroll: true }); }
      hasLoaded = true; failures = 0; connection = 'live';
      lastSync = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
      // A detail can be outside the loaded/filter window, so it gets its own versioned refresh.
      if (targetId && !creating) {
        const incoming = await api<Item>(`/items/${targetId}`, 'GET', undefined, signal);
        if (own === generation && targetId === openId && (!detail || incoming.version > detail.version)) detail = incoming;
      }
      if (Date.now() - lastDirectory > 60000) await loadDirectory();
    } catch (e) {
      if (signal.aborted || own !== generation) return;
      if (e instanceof APIError && e.code === 'cursor_expired' && Object.values(loadedPages).some(n => n > 1)) {
        loadedPages = {}; notice = 'Order changed; loaded pages reset.';
        void refresh(true); return;
      }
      failures = Math.min(failures + 1, 4); connection = 'offline'; error = message(e);
    } finally { if (own === generation) { syncing = false; busy = false; } }
  }

  async function fetchDetail(id: string) {
    const own = ++detailGeneration;
    detailLoading = true;
    try {
      const incoming = await api<Item>(`/items/${id}`);
      if (own === detailGeneration && openId === id && (!detail || detail.id !== id || incoming.version >= detail.version)) detail = incoming;
    } catch (e) { if (own === detailGeneration) error = message(e); }
    finally { if (own === detailGeneration) detailLoading = false; }
  }

  async function openItem(id: string, focus = true) {
    if (openId === id && !creating) return;
    if (!guardDraft()) return;
    quickStatus = null; openId = id; selectedId = id; detail = null; updateURL(true);
    await fetchDetail(id);
    if (focus) { await tick(); document.querySelector<HTMLInputElement>('.title-editor')?.focus(); }
  }

  async function create(status: Status = activeColumn) {
    if (readonly || !guardDraft()) return;
    quickStatus = status; quickTitle = ''; activeColumn = status;
    await tick(); document.getElementById('quick-title')?.focus();
  }

  async function quickCreate(event: SubmitEvent) {
    event.preventDefault();
    if (!quickTitle.trim() || !quickStatus || creating) return;
    creating = true; error = '';
    controller?.abort(); generation++; syncing = false; busy = false;
    try {
      const item = await api<Item>('/items', 'POST', {
        title: quickTitle, type: 'task', status: quickStatus,
        tags: filterTag ? [filterTag] : [],
        assignees: filterAssignee && filterAssignee !== 'none' ? [filterAssignee] : [],
      });
      quickTitle = ''; selectedId = item.id;
      await refresh(true);
      document.getElementById('quick-title')?.focus();
    } catch (e) { error = message(e) + (!(e instanceof APIError) ? ' Check the board before retrying; the item may have been created.' : ''); }
    finally { creating = false; }
  }

  async function changeStatus(id: string, status: Status) {
    const item = items.find(i => i.id === id);
    if (!item || item.status === status || readonly || moving || !guardDraft()) return;
    moving = true; error = ''; controller?.abort(); generation++; syncing = false; busy = false;
    try {
      const updated = await api<Item>(`/items/${id}`, 'PATCH', { version: item.version, status });
      items = items.map(i => i.id === id ? updated : i);
      if (detail?.id === id) detail = updated;
      activeColumn = status; selectedId = id;
      await refresh(true); await tick(); document.getElementById(`ticket-${id}`)?.focus();
    } catch (e) { error = message(e); }
    finally { moving = false; dragging = ''; }
  }

  async function closeDetails() {
    if (!guardDraft()) return;
    openId = ''; detail = null; detailGeneration++; updateURL(true);
    await tick(); document.getElementById(`ticket-${selectedId}`)?.focus();
  }

  async function saved(item: Item) {
    dirty = false; notice = ''; openId = item.id; selectedId = item.id; detail = item;
    items = items.map(i => i.id === item.id ? item : i);
    updateURL(true); await refresh(true);
  }

  function setView(status: string, assignee: string) {
    filterStatus = status; filterAssignee = assignee; query = ''; updateURL(true); void refresh(true, true);
  }
  function filterChanged() { updateURL(true); void refresh(true, true); }
  async function more(status: Status) { loadedPages[status] = (loadedPages[status] || 1) + 1; await refresh(true); }

  async function move(direction: number) {
    if (readonly || moving || !guardDraft()) return;
    const column = visible.filter(i => i.status === activeColumn);
    const index = column.findIndex(i => i.id === selectedId);
    const current = column[index]; const anchor = column[index + direction];
    if (!current || !anchor) return;
    moving = true; error = '';
    // Prevent a pre-mutation list response from undoing the acknowledged version locally.
    controller?.abort(); generation++; syncing = false; busy = false;
    try {
      const updated = await api<Item>(`/items/${current.id}/move`, 'POST', { version: current.version, [direction < 0 ? 'before' : 'after']: anchor.id });
      if (detail?.id === updated.id) detail = updated;
      await refresh(true); await tick(); document.getElementById(`ticket-${selectedId}`)?.focus();
    } catch (e) { error = message(e); }
    finally { moving = false; }
  }

  async function showHelp() { help = true; await tick(); helpDialog.showModal(); }
  function showPalette() { paletteReturn = document.activeElement as HTMLElement; palette = true; }
  async function closePalette() { palette = false; await tick(); paletteReturn?.focus(); }

  function keyboard(event: KeyboardEvent) {
    if (!user || authExpired || event.isComposing || event.defaultPrevented) return;
    if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'k') { event.preventDefault(); if (palette) void closePalette(); else showPalette(); return; }
    if (palette || help) return;
    const target = event.target as HTMLElement;
    const editing = target.closest('input, textarea, select, [contenteditable="true"]');
    if (event.key === 'Escape') {
      event.preventDefault();
      if (quickStatus && !creating) { quickStatus = null; quickTitle = ''; document.getElementById(`column-${activeColumn}`)?.focus(); }
      else if (openId) void closeDetails();
      else if (editing) { document.getElementById(`ticket-${selectedId}`)?.focus(); }
      return;
    }
    if (editing || event.ctrlKey || event.metaKey) return;
    if (!target.closest('.board')) return;
    if (!event.altKey && ['/', 'c', '?', 'r'].includes(event.key)) {
      event.preventDefault();
      if (event.key === '/') searchInput.focus();
      if (event.key === 'c') void create();
      if (event.key === '?') void showHelp();
      if (event.key === 'r') void refresh(true);
      return;
    }
    const column = visible.filter(i => i.status === activeColumn);
    const index = column.findIndex(i => i.id === selectedId);
    if (['ArrowDown', 'ArrowUp', 'j', 'k'].includes(event.key)) {
      event.preventDefault();
      const direction = ['ArrowDown', 'j'].includes(event.key) ? 1 : -1;
      if (event.altKey) { void move(direction); return; }
      const next = column[Math.min(Math.max(index + direction, 0), column.length - 1)];
      if (next) { selectedId = next.id; document.getElementById(`ticket-${next.id}`)?.focus(); }
    } else if (['ArrowLeft', 'ArrowRight', 'h', 'l'].includes(event.key)) {
      event.preventDefault();
      const direction = ['ArrowRight', 'l'].includes(event.key) ? 1 : -1;
      const nextStatus = columns[columns.indexOf(activeColumn) + direction];
      if (!nextStatus) return;
      if (event.altKey) { void changeStatus(selectedId, nextStatus); return; }
      activeColumn = nextStatus;
      const nextItems = visible.filter(i => i.status === nextStatus);
      const next = nextItems[Math.min(Math.max(index, 0), nextItems.length - 1)];
      if (next) { selectedId = next.id; document.getElementById(`ticket-${next.id}`)?.focus(); }
      else document.getElementById(`column-${nextStatus}`)?.focus();
    }
  }

  onMount(() => {
    if (hasSession()) void api<User>('/auth/me').then(async value => { user = value; await start(); }).catch(e => { setSession(''); loginError = message(e); }).finally(() => checking = false);
    const expired = () => { authExpired = true; setSession(''); clearTimeout(timer); connection = 'offline'; loginError = 'Your session expired. Sign in again to continue. Your open draft is preserved.'; };
    const visibility = () => { if (document.hidden) { connection = 'paused'; clearTimeout(timer); } else if (user && !authExpired) void poll(); };
    const online = () => { failures = 0; void poll(); };
    const unload = (event: BeforeUnloadEvent) => { if (dirty || quickTitle.trim()) event.preventDefault(); };
    const popstate = () => {
      if (!guardDraft()) { updateURL(); return; }
      const url = new URL(location.href);
      query = url.searchParams.get('q') || ''; filterStatus = url.searchParams.get('status') || '';
      filterTag = url.searchParams.get('tag') || ''; filterAssignee = url.searchParams.get('assignee') || '';
      openId = url.searchParams.get('item') || ''; creating = false; detail = null;
      if (openId) { selectedId = openId; void fetchDetail(openId); }
      void refresh(true, true);
    };
    window.addEventListener('tiki:expired', expired);
    document.addEventListener('visibilitychange', visibility);
    window.addEventListener('online', online);
    window.addEventListener('beforeunload', unload);
    window.addEventListener('popstate', popstate);
    return () => {
      stopped = true; clearTimeout(timer); controller?.abort();
      window.removeEventListener('tiki:expired', expired); document.removeEventListener('visibilitychange', visibility);
      window.removeEventListener('online', online); window.removeEventListener('beforeunload', unload); window.removeEventListener('popstate', popstate);
    };
  });

</script>

<svelte:window onkeydown={keyboard} />

{#if !user || authExpired}
  <div class="login-screen">
    <form class="login-form" onsubmit={login}>
      <h1>tiki <span>/ sign in</span></h1>
      {#if loginError}<p class="error-banner" role="alert">{loginError}</p>{/if}
      <label>Email<input type="email" autocomplete="username" required bind:value={email} disabled={checking || signingIn} /></label>
      <label>Password<input type="password" autocomplete="current-password" required bind:value={password} disabled={checking || signingIn} /></label>
      <button class="primary-button" disabled={checking || signingIn}>{checking ? 'Restoring session…' : signingIn ? 'Signing in…' : 'Sign in'}</button>
    </form>
  </div>
{/if}

{#if user}
  <main class="app" inert={authExpired}>
    <div class="toolbar">
      <span class="wordmark">tiki</span>
      <div class="search-field"><Icon name="search" size={13} /><input aria-label="Search loaded items" placeholder="Filter loaded items…" bind:value={query} bind:this={searchInput} oninput={() => updateURL()} /><kbd>/</kbd></div>
      <select aria-label="Filter assignee" title="Assignee" bind:value={filterAssignee} onchange={filterChanged}><option value="">Assignee</option><option value="none">Unassigned</option>{#each users as u}<option value={u.id}>{u.id === user.id ? 'Me' : u.name}</option>{/each}</select>
      <select aria-label="Filter tag" title="Tag" bind:value={filterTag} onchange={filterChanged}><option value="">Tag</option>{#each tags as tag}<option value={tag}>{tag}</option>{/each}</select>
      <select aria-label="Filter status" title="Status" bind:value={filterStatus} onchange={filterChanged}><option value="">Status</option>{#each statuses as status}<option value={status}>{label(status)}</option>{/each}</select>
      {#if filterTag || filterAssignee || filterStatus || query}<button class="icon-button" aria-label="Clear filters" title="Clear filters" onclick={() => { filterTag = ''; setView('', ''); }}><Icon name="close" size={13} /></button>{/if}
      <span class="toolbar-spacer"></span>
      {#if orderChanged}<button class="text-button order-notice" onclick={() => refresh(true)}>Apply order</button>{/if}
      <span class={`connection ${connection}`} title={`Refreshes every 2 seconds. ${lastSync ? `Last sync ${lastSync}.` : ''}`}><span class="tiny-dot"></span><span>{connection === 'live' ? 'Live' : connection === 'offline' ? 'Offline' : connection === 'paused' ? 'Paused' : 'Syncing'}</span></span>
      <button class="icon-button" aria-label="Refresh board" title="Refresh (R)" disabled={busy} onclick={() => { error = ''; void refresh(true); }}><Icon name="refresh" size={14} /></button>
      <button class="icon-button" aria-label="Commands" title="Commands (Ctrl/Cmd+K)" onclick={showPalette}><Icon name="command" size={14} /></button>
      <button class="icon-button help-button" aria-label="Keyboard shortcuts" title="Keyboard shortcuts (?)" onclick={showHelp}>?</button>
      {#if !readonly}<button class="small-button new-button" onclick={() => create()}><Icon name="plus" size={13} />New</button>{/if}
    </div>
    {#if error}<div class="board-alert error-banner" role="alert"><span>{error}</span><button class="text-button" onclick={() => { error = ''; void refresh(true); }}>Retry</button></div>{/if}
    {#if notice}<div class="board-alert notice-banner" role="status">{notice}<button class="icon-button" aria-label="Dismiss notice" onclick={() => notice = ''}><Icon name="close" size={13} /></button></div>{/if}
    <div class="board" aria-label="Items by status">
      {#each columns as status}
        {@const columnItems = visible.filter(i => i.status === status)}
        <!-- Native drag/drop is an additional input; the same action is available through Alt+arrows and the editor. -->
        <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
        <section class="kanban-column" class:drop-target={dragging && items.find(i => i.id === dragging)?.status !== status} aria-label={`${label(status)} column`} ondragover={event => { if (dragging && !readonly) event.preventDefault(); }} ondrop={event => { event.preventDefault(); if (dragging) void changeStatus(dragging, status); }}>
          <div class="column-header"><button id={`column-${status}`} class="column-focus" onfocus={() => activeColumn = status} onclick={() => { if (columnItems[0]) { selectedId = columnItems[0].id; document.getElementById(`ticket-${selectedId}`)?.focus(); } }}><span class={`status-dot ${status}`}></span><h2>{label(status)}</h2><span class="column-count" title="Loaded items">{columnItems.length}{cursors[status] ? '+' : ''}</span></button>{#if !readonly}<button class="icon-button column-add" aria-label={`Add item to ${label(status)}`} title="Add item (C)" onclick={() => create(status)}><Icon name="plus" size={14} /></button>{/if}</div>
          <div class="column-scroll">
            {#if quickStatus === status}<form class="quick-create" onsubmit={quickCreate}><textarea id="quick-title" aria-label="New item title" placeholder="Item title" rows="2" maxlength="300" required bind:value={quickTitle} disabled={creating} onkeydown={event => { if (event.key === 'Enter' && !event.shiftKey && !event.isComposing) { event.preventDefault(); event.currentTarget.form?.requestSubmit(); } }}></textarea><div><span class="muted">↵ add · esc cancel</span><button class="small-button" disabled={creating || !quickTitle.trim()}>{creating ? 'Adding…' : 'Add'}</button></div></form>{/if}
            <ul class="cards" aria-label={`${label(status)} items`}>
              {#each columnItems as item (item.id)}
                <li><button id={`ticket-${item.id}`} data-ticket={item.id} class="card" class:selected={selectedId === item.id} class:opened={openId === item.id} draggable={!readonly && !moving && !dirty} ondragstart={event => { dragging = item.id; selectedId = item.id; event.dataTransfer?.setData('text/plain', item.id); }} ondragend={() => dragging = ''} onfocus={() => { selectedId = item.id; activeColumn = status; }} onclick={() => openItem(item.id)} aria-label={`TK-${item.id}: ${item.title}`} aria-current={openId === item.id ? 'true' : undefined}>
                  <div class="card-top"><span class="item-id">#{item.id}</span><span class={`item-type ${item.type}`}>{item.type}</span></div>
                  <span class="card-title">{item.title}</span>
                  {#if item.tags.length || item.assignees.length}<div class="card-meta"><span class="card-tags">{#each item.tags.slice(0, 2) as tag}<span>{tag}</span>{/each}{#if item.tags.length > 2}<span>+{item.tags.length - 2}</span>{/if}</span><span class="card-assignees">{#each item.assignees.slice(0, 2) as id}<span class="mini-avatar" title={users.find(u => u.id === id)?.name || id}>{initials(users.find(u => u.id === id)?.name || id)}</span>{/each}{#if item.assignees.length > 2}<span class="muted">+{item.assignees.length - 2}</span>{/if}</span></div>{/if}
                </button></li>
              {/each}
            </ul>
            {#if !hasLoaded && busy}<p class="column-empty">Loading…</p>{:else if !columnItems.length && quickStatus !== status}<p class="column-empty">{query ? 'No matches' : '—'}</p>{/if}
            {#if cursors[status]}<button class="load-more" disabled={busy} onclick={() => more(status)}>Load more</button>{/if}
          </div>
        </section>
      {/each}
    </div>
    {#if openId && detail}{#key openId}<ItemEditor item={detail} {users} {tags} {readonly} onclose={closeDetails} onsave={saved} ondirty={value => dirty = value} />{/key}
    {:else if openId}<aside class="detail detail-loading"><div class="detail-top"><span>#{openId}</span><button class="icon-button" aria-label="Close item" onclick={closeDetails}><Icon name="close" size={16} /></button></div><p>{detailLoading ? 'Loading…' : 'Could not load item.'}</p>{#if !detailLoading}<button class="small-button" onclick={() => fetchDetail(openId)}>Retry</button>{/if}</aside>{/if}
  </main>
{/if}

{#if palette}<CommandMenu {actions} onclose={closePalette} />{/if}
{#if help}<dialog class="help-dialog" bind:this={helpDialog} oncancel={() => help = false} aria-label="Keyboard shortcuts"><div class="detail-top"><h2>Keyboard shortcuts</h2><button class="icon-button" aria-label="Close shortcuts" onclick={() => help = false}><Icon name="close" size={15} /></button></div><dl>{#each [['Ctrl / ⌘ K', 'Commands'], ['↑ ↓ / J K', 'Navigate items'], ['← → / H L', 'Navigate columns'], ['Alt ← →', 'Change status'], ['Alt ↑ ↓', 'Change priority'], ['Enter', 'Open item'], ['C', 'Add item in column'], ['/', 'Filter loaded items'], ['R', 'Refresh order'], ['Ctrl / ⌘ Enter', 'Save edits'], ['Escape', 'Close or cancel'], ['?', 'Shortcuts']] as [keys, action]}<div><dt>{action}</dt><dd><kbd>{keys}</kbd></dd></div>{/each}</dl><p class="muted">Letter and arrow shortcuts apply when the board is focused. Tab reaches every control.</p></dialog>{/if}
