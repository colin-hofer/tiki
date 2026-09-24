<script lang="ts">
  import { onMount, tick } from 'svelte';
  import { api, APIError, directory, hasSession, setSession, watchChanges, statuses, label, initials, avatarHue } from './api';
  import type { Board, Item, Page, Session, User, Status } from './api';
  import Icon from './Icon.svelte';
  import ItemEditor from './ItemEditor.svelte';
  import CommandMenu from './CommandMenu.svelte';
  import Select from './Select.svelte';
  import Join from './Join.svelte';
  import PeopleDialog from './PeopleDialog.svelte';
  import SetupDialog from './SetupDialog.svelte';
  import AccountDialog from './AccountDialog.svelte';
  import TagsDialog from './TagsDialog.svelte';
  import { flip } from 'svelte/animate';
  import { arrive, capture, duration, panel, sheet, pin, unpin } from './motion';

  const initialURL = new URL(location.href);
  const initialInvite = new URLSearchParams(initialURL.hash.slice(1)).get('invite');
  let inviteToken = $state(initialInvite);
  let inviting = $state(false);
  let installing = $state(false);
  let managingTags = $state(false);
  let tagsReturn: HTMLElement | null = null;
  let account = $state<'menu' | 'profile' | 'password' | null>(null);
  let accountReturn: HTMLElement | null = null;
  let user = $state<User | null>(null);
  let authExpired = $state(false);
  let checking = $state(initialInvite === null && hasSession());
  let email = $state('');
  let password = $state('');
  let signingIn = $state(false);
  let loginError = $state('');
  let loginNotice = $state('');
  let users = $state<User[]>([]);
  let tags = $state<string[]>([]);
  let items = $state<Item[]>([]);
  let cursors = $state<Partial<Record<Status, string>>>({});
  let selectedId = $state(initialURL.searchParams.get('item') || '');
  let openId = $state(initialURL.searchParams.get('item') || '');
  let detail = $state<Item | null>(null);
  let detailLoading = $state(false);
  let detailDeleted = $state(false);
  let deleteTarget = $state<Item | null>(null);
  let deleteDialog = $state<HTMLDialogElement>(null!);
  let deleting = $state(false);
  let deleteError = $state('');
  let deleteConflict = $state(false);
  let deleteReturn: HTMLElement | null = null;
  let quickStatus = $state<Status | null>(null);
  let quickTitle = $state('');
  let creating = $state(false);
  let activeColumn = $state<Status>('todo');
  let dragging = $state('');
  let dirty = $state(false);
  let query = $state(initialURL.searchParams.get('q') || '');
  let filterStatus = $state(statuses.find(s => s === initialURL.searchParams.get('status')) || '');
  let filterTag = $state(initialURL.searchParams.get('tag') || '');
  let filterAssignee = $state(initialURL.searchParams.get('assignee') || '');
  let busy = $state(false);
  let hasLoaded = $state(false);
  let connection = $state<'connecting' | 'live' | 'offline' | 'paused'>('connecting');
  let lastSync = $state('');
  let error = $state('');
  let notice = $state('');
  let orderChanged = $state(false);
  type Menu = 'commands' | 'status' | 'assignee' | 'tags' | 'type';
  let palette = $state<Menu | null>(null);
  let menuItem = $state<Item | null>(null);
  let announcement = $state('');
  let preferredRow = 0;
  let lastG = 0;
  let dropTarget = $state('');
  let dropBefore = $state(true);
  let help = $state(false);
  let moving = $state(false);
  let searchInput = $state<HTMLInputElement>(null!);
  let helpDialog = $state<HTMLDialogElement>(null!);
  let paletteReturn: HTMLElement | null = null;
  let helpReturn: HTMLElement | null = null;
  let loadedPages: Partial<Record<Status, number>> = {};
  let generation = 0;
  let detailGeneration = 0;
  let controller: AbortController | null = null;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let failures = 0;
  let syncing = false;
  let stopEvents: (() => void) | undefined;
  let pendingRefresh = false;
  let pendingUsers = false;
  const pendingItems = new Map<string, Item>();
  let flushingChanges = false;
  let streamState: typeof connection = 'connecting';
  let stopped = false;

  let readonly = $derived(user?.role === 'viewer');
  let visible = $derived(items.filter(item => {
    const text = query.toLowerCase().trim().replace(/^#/, '').replace(/^tk-/, '');
    return !text || `${item.id} ${item.title} ${item.tags.join(' ')}`.toLowerCase().includes(text);
  }));
  let columns = $derived(statuses.filter(s => !filterStatus || s === filterStatus));
  let boardItems = $derived(columns.flatMap(status => visible.filter(i => i.status === status)));
  // Record card slots before the board re-renders so cross-column moves can animate.
  $effect.pre(() => { void boardItems; capture(); });
  let editor = $state<ItemEditor>();
  let selected = $derived(visible.find(i => i.id === selectedId));
  let openedIndex = $derived(boardItems.findIndex(i => i.id === openId));

  // Phone layout: one status at a time, swiped horizontally, with status tabs above the feed.
  let mobile = $state(false);
  let coarse = $state(false);
  let board = $state<HTMLElement>(null!);
  let tabStrip = $state<HTMLElement>(null!);
  let feedStatus = $state<Status>('todo');
  let feedReady = false;
  let searchOpen = $state(false);
  let filtersOpen = $state(false);
  let moveSheet = $state<Item | null>(null);
  let sheetDialog = $state<HTMLDialogElement>(null!);
  let sheetOpenedAt = 0;
  let pinFeed = false;
  let press: { timer: ReturnType<typeof setTimeout>; x: number; y: number } | undefined;
  let suppressClick = false;
  let activeFilters = $derived([filterAssignee, filterTag, filterStatus].filter(Boolean).length);

  $effect(() => {
    const narrow = matchMedia('(max-width: 700px)'), touch = matchMedia('(pointer: coarse)');
    const media = () => { mobile = narrow.matches; coarse = touch.matches; if (!mobile) { filtersOpen = false; searchOpen = false; } else feedScrolled(); };
    // On-screen keyboards shrink the visual viewport; fixed bottom UI rides above them.
    const viewport = window.visualViewport;
    const keyboardInset = () => { if (viewport) document.documentElement.style.setProperty('--keyboard', `${Math.max(0, Math.round(innerHeight - viewport.height - viewport.offsetTop))}px`); };
    media();
    narrow.addEventListener('change', media); touch.addEventListener('change', media);
    viewport?.addEventListener('resize', keyboardInset); viewport?.addEventListener('scroll', keyboardInset);
    return () => {
      narrow.removeEventListener('change', media); touch.removeEventListener('change', media);
      viewport?.removeEventListener('resize', keyboardInset); viewport?.removeEventListener('scroll', keyboardInset);
    };
  });
  $effect(() => {
    if (!mobile || !hasLoaded || !board || feedReady) return;
    feedReady = true;
    void tick().then(() => showStatus(columns.includes(activeColumn) ? activeColumn : columns[0], true));
  });
  $effect(() => { if (!columns.includes(feedStatus) && columns[0]) feedStatus = columns[0]; });
  $effect(() => {
    const tab = tabStrip?.querySelector<HTMLElement>(`[data-status="${feedStatus}"]`);
    if (tab && mobile) tabStrip.scrollTo({ left: tab.offsetLeft - (tabStrip.clientWidth - tab.offsetWidth) / 2, behavior: duration(1) ? 'smooth' : 'instant' });
  });

  // While a tab tap or drag scrolls the feed, intermediate positions must not reset the chosen status.
  let scrollingTo: { status: Status; until: number } | null = null;
  function feedScrolled() {
    if (!mobile || !board) return;
    const status = columns[Math.round(board.scrollLeft / Math.max(1, board.clientWidth))];
    if (scrollingTo) { if (status === scrollingTo.status || Date.now() > scrollingTo.until) scrollingTo = null; else return; }
    if (status && status !== feedStatus) { feedStatus = status; activeColumn = status; }
  }
  function showStatus(status: Status, instant = false) {
    const index = columns.indexOf(status);
    if (index < 0 || !board) return;
    const smooth = !instant && Boolean(duration(1));
    scrollingTo = smooth ? { status, until: Date.now() + 800 } : null;
    board.scrollTo({ left: index * board.clientWidth, behavior: smooth ? 'smooth' : 'instant' });
    feedStatus = status; activeColumn = status;
  }
  async function openSearch() { searchOpen = true; await tick(); searchInput.focus(); }
  function closeSearch() { query = ''; searchOpen = false; updateURL(); }

  // Touch: press and hold lifts a card. Drag up/down to reorder, to a screen edge or status tab to change status, release to drop.
  // Releasing without moving opens the ticket's actions sheet. Native drag and drop is unavailable on touch screens.
  type TouchDrag = { item: Item; ghost: HTMLElement; dx: number; dy: number; x: number; y: number; startX: number; startY: number; moved: boolean; edgeSince: number; tabSince: number; tab: string };
  let lifted = $state<TouchDrag | null>(null);
  let dragFrame = 0;
  function pressStart(event: PointerEvent, item: Item) {
    suppressClick = false;
    if (event.pointerType !== 'touch' || readonly || moving || lifted) return;
    const card = event.currentTarget as HTMLElement;
    const x = event.clientX, y = event.clientY;
    press = { x, y, timer: setTimeout(() => { press = undefined; void lift(card, item, x, y); }, 350) };
  }
  function pressMove(event: PointerEvent) { if (press && Math.hypot(event.clientX - press.x, event.clientY - press.y) > 8) pressEnd(); }
  function pressEnd() { if (press) { clearTimeout(press.timer); press = undefined; } }
  async function lift(card: HTMLElement, item: Item, x: number, y: number) {
    if (!card.isConnected || !(await guardDraft())) return;
    suppressClick = true; navigator.vibrate?.(10);
    const rect = card.getBoundingClientRect();
    const ghost = card.cloneNode(true) as HTMLElement;
    for (const element of [ghost, ...ghost.querySelectorAll('[id]')]) element.removeAttribute('id');
    ghost.classList.add('card-ghost'); ghost.setAttribute('aria-hidden', 'true'); ghost.inert = true;
    Object.assign(ghost.style, { left: `${rect.left}px`, top: `${rect.top}px`, width: `${rect.width}px`, height: `${rect.height}px` });
    ghost.style.transform = 'scale(1.03) rotate(-1deg)';
    document.body.append(ghost);
    ghost.animate([{ transform: 'none', boxShadow: 'none' }, {}], { duration: duration(160), easing: 'cubic-bezier(.2, .8, .2, 1)' });
    selectedId = item.id;
    lifted = { item, ghost, dx: x - rect.left, dy: y - rect.top, x, y, startX: x, startY: y, moved: false, edgeSince: 0, tabSince: 0, tab: '' };
    window.addEventListener('pointermove', dragMove, { passive: false });
    window.addEventListener('pointerup', dragEnd);
    window.addEventListener('pointercancel', dragCancel);
    window.addEventListener('touchmove', holdScroll, { passive: false });
    dragFrame = requestAnimationFrame(dragTick);
  }
  function holdScroll(event: TouchEvent) { if (lifted) event.preventDefault(); }
  function dragMove(event: PointerEvent) {
    if (!lifted || event.pointerType !== 'touch') return;
    event.preventDefault();
    lifted.x = event.clientX; lifted.y = event.clientY;
    if (Math.hypot(lifted.x - lifted.startX, lifted.y - lifted.startY) > 10) lifted.moved = true;
    lifted.ghost.style.transform = `translate(${lifted.x - lifted.dx - parseFloat(lifted.ghost.style.left)}px, ${lifted.y - lifted.dy - parseFloat(lifted.ghost.style.top)}px) scale(1.03) rotate(-1deg)`;
    placeDrop();
  }
  function feedColumn() { return board?.querySelector<HTMLElement>(`[data-column="${feedStatus}"]`); }
  function placeDrop() {
    if (!lifted) return;
    const cards = [...(feedColumn()?.querySelectorAll<HTMLElement>('[data-card]') || [])].filter(node => node.dataset.card !== lifted!.item.id);
    const next = cards.find(node => { const r = node.getBoundingClientRect(); return lifted!.y < r.top + r.height / 2; });
    const anchor = next || cards.at(-1);
    dropTarget = anchor?.dataset.card || ''; dropBefore = Boolean(next);
  }
  function dragTick(now: number) {
    if (!lifted) return;
    const { x, y } = lifted;
    // Auto-scroll the column near its top and bottom edges.
    const scroller = feedColumn()?.querySelector<HTMLElement>('.column-scroll');
    if (scroller && lifted.moved) {
      const r = scroller.getBoundingClientRect();
      const speed = y < r.top + 56 ? -(r.top + 56 - y) / 4 : y > r.bottom - 72 ? (y - (r.bottom - 72)) / 4 : 0;
      if (speed) { scroller.scrollTop += Math.max(-14, Math.min(14, speed)); placeDrop(); }
    }
    // Hovering a status tab, or holding at a screen edge, switches status.
    const tab = (document.elementFromPoint(x, y) as HTMLElement | null)?.closest<HTMLElement>('.status-tabs [data-status]')?.dataset.status || '';
    if (tab !== lifted.tab) { lifted.tab = tab; lifted.tabSince = now; }
    else if (tab && tab !== feedStatus && now - lifted.tabSince > 260) { showStatus(tab as Status); lifted.tabSince = now; void tick().then(placeDrop); }
    const edge = !tab && lifted.moved ? (x < 28 ? -1 : x > innerWidth - 28 ? 1 : 0) : 0;
    if (!edge) lifted.edgeSince = 0;
    else if (!lifted.edgeSince) lifted.edgeSince = now;
    else if (now - lifted.edgeSince > 380) {
      const status = columns[columns.indexOf(feedStatus) + edge];
      if (status) { showStatus(status); navigator.vibrate?.(6); void tick().then(placeDrop); }
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
    const drag = lifted; lifted = null; dropTarget = '';
    return drag;
  }
  function settle(ghost: HTMLElement) {
    ghost.animate([{ opacity: 1 }, { opacity: 0, transform: `${ghost.style.transform || ''} scale(.98)` }], { duration: duration(140), easing: 'ease-out' }).finished.then(() => ghost.remove(), () => ghost.remove());
  }
  function dragCancel() { const drag = stopDrag(); if (drag) settle(drag.ghost); }
  async function dragEnd() {
    const target = dropTarget, before = dropBefore;
    const drag = stopDrag();
    if (!drag) return;
    const { item, ghost } = drag;
    const status = feedStatus;
    if (!drag.moved && status === item.status) { settle(ghost); void openMoveSheet(item); return; }
    const anchor = items.find(i => i.id === target);
    // Same column and same slot: nothing to save.
    const others = visible.filter(i => i.status === status && i.id !== item.id);
    const slot = anchor ? others.indexOf(anchor) + (before ? 0 : 1) : others.length;
    if (status === item.status && slot === visible.filter(i => i.status === status).findIndex(i => i.id === item.id)) { settle(ghost); return; }
    // A card landing in another column arrives from the drop point; the ghost leaves once its replacement is animating.
    if (status !== item.status) pin(item.id, ghost.getBoundingClientRect(), () => ghost.remove());
    try {
      if (anchor) await moveTo(item, anchor, before);
      else await changeStatus(item.id, status);
    } finally {
      unpin(item.id);
      if (ghost.isConnected) settle(ghost);
      if (mobile) { activeColumn = status; showStatus(status, true); }
    }
  }
  async function openMoveSheet(item: Item) {
    if (!(await guardDraft())) return;
    selectedId = item.id; moveSheet = item; sheetOpenedAt = Date.now();
    await tick(); sheetDialog?.showModal();
  }
  function closeMoveSheet() { if (sheetDialog?.open) sheetDialog.close(); moveSheet = null; }
  async function sheetAction(run: (item: Item) => Promise<void> | void) {
    // Capture the ticket before the sheet unmounts, and stay on the current status tab rather than following it.
    const item = moveSheet, status = feedStatus;
    closeMoveSheet();
    if (!item) return;
    pinFeed = true;
    try { await run(item); } finally { pinFeed = false; if (mobile) activeColumn = status; }
  }
  let actions = $derived([
    ...(!readonly ? [{ id: 'new', label: 'Create a ticket', hint: 'C', run: () => create() }] : []),
    { id: 'search', label: 'Search loaded tickets', hint: '/', run: () => searchInput?.focus() },
    { id: 'all', label: 'Clear all filters', run: clearFilters },
    { id: 'mine', label: 'Filter: assigned to me', run: () => setView('', user?.id || '') },
    { id: 'refresh', label: 'Refresh tickets and apply current order', hint: 'R', run: () => void refresh(true) },
    ...(selected ? [{ id: 'open', label: `Open TK-${selectedId}`, hint: '↵', run: () => void openItem(selectedId) }] : []),
    ...(!readonly && (selected || (detail && !detailDeleted)) ? [{ id: 'delete', label: `Delete TK-${openId && !detailDeleted ? openId : selectedId}…`, hint: 'Delete', run: () => void requestDelete(openId && !detailDeleted ? detail : selected) }] : []),
    ...(!readonly && selected ? [
      { id: 'edit', label: 'Edit ticket title', hint: 'E', run: () => void editSelected('Ticket title') },
      { id: 'description', label: 'Edit description', hint: 'D', run: () => void editSelected('Description') },
      { id: 'assign', label: 'Assign ticket', hint: 'A', run: () => propertyMenu('assignee') },
      { id: 'tags', label: 'Edit tags', hint: 'T', run: () => propertyMenu('tags') },
      { id: 'type', label: 'Change ticket type', hint: 'Y', run: () => propertyMenu('type') },
      { id: 'me', label: selected.assignees.includes(user?.id || '') ? 'Unassign me' : 'Assign to me', hint: 'M', run: assignMe },
      ...statuses.map(status => ({ id: `status-${status}`, label: `Set status: ${label(status)}`, run: () => void changeStatus(selectedId, status) })),
      { id: 'up', label: 'Move selected ticket up', hint: 'Alt ↑', run: () => void move(-1) },
      { id: 'down', label: 'Move selected ticket down', hint: 'Alt ↓', run: () => void move(1) },
    ] : []),
    ...columns.map((status, index) => ({ id: `column-${status}`, label: `Go to ${label(status)} column`, hint: String(index + 1), run: () => focusColumn(status, preferredRow) })),
    { id: 'help', label: 'Keyboard shortcuts', hint: '?', run: showHelp },
    ...(user?.role === 'admin' ? [{ id: 'people', label: 'Manage people', run: () => inviting = true }] : []),
    { id: 'install', label: 'Install CLI & agent skill', run: () => installing = true },
    { id: 'profile', label: 'Edit my profile', run: () => showAccount('profile') },
    ...(!readonly ? [{ id: 'manage-tags', label: 'Manage tags', run: showTags }] : []),
    { id: 'password', label: 'Change my password', run: () => showAccount('password') },
    { id: 'logout', label: 'Sign out', run: () => void logout() },
  ]);

  let menuTitle = $derived(palette === 'commands' ? 'Commands' : `${palette === 'assignee' ? 'Assign' : palette === 'tags' ? 'Tags' : palette === 'type' ? 'Type' : 'Status'} · TK-${menuItem?.id}`);
  let menuActions = $derived.by(() => {
    if (palette === 'commands' || !menuItem) return actions;
    const item = menuItem;
    if (palette === 'status') return statuses.map(status => ({ id: status, label: label(status), hint: item.status === status ? 'Current' : '', run: () => void changeStatus(item.id, status) }));
    if (palette === 'type') return ['task', 'bug', 'feature'].map(type => ({ id: type, label: label(type), hint: item.type === type ? 'Current' : '', run: () => void updateItem(item, { type }, `Type: ${label(type)}`) }));
    if (palette === 'assignee') return [
      { id: 'none', label: 'Unassign everyone', run: () => void updateItem(item, { remove_assignees: item.assignees }, 'Unassigned') },
      ...users.filter(u => !u.removed_at || item.assignees.includes(u.id)).map(u => ({ id: u.id, label: `${item.assignees.includes(u.id) ? 'Remove' : 'Assign'} ${u.name}${u.removed_at ? ' (removed)' : ''}${u.id === user?.id ? ' (me)' : ''}`, run: () => void updateItem(item, { [item.assignees.includes(u.id) ? 'remove_assignees' : 'add_assignees']: [u.id] }, 'Assignment updated') })),
    ];
    return [
      { id: 'new-tag', label: 'Add a new tag…', run: () => void editSelected('Add tag') },
      ...[...new Set([...tags, ...item.tags])].map(tag => ({ id: `tag-${tag}`, label: `${item.tags.includes(tag) ? 'Remove' : 'Add'} #${tag}`, run: () => void updateItem(item, { [item.tags.includes(tag) ? 'remove_tags' : 'add_tags']: [tag] }, 'Tags updated') })),
    ];
  });

  function focusColumn(status = activeColumn, row = preferredRow) {
    activeColumn = columns.includes(status) ? status : columns[0];
    const column = visible.filter(i => i.status === activeColumn);
    const next = column[Math.min(Math.max(row, 0), column.length - 1)];
    selectedId = next?.id || '';
    const element = document.getElementById(next ? `ticket-${next.id}` : `column-${activeColumn}`);
    element?.focus({ preventScroll: true });
    if (!(mobile && pinFeed)) element?.scrollIntoView({ block: 'nearest', inline: 'nearest' });
    preferredRow = row;
  }

  function focusBoard() {
    const item = visible.find(i => i.id === selectedId);
    focusColumn(item?.status || activeColumn, item ? visible.filter(i => i.status === item.status).indexOf(item) : preferredRow);
  }

  async function editSelected(field = 'Ticket title') {
    const id = document.activeElement?.closest('.detail') ? openId : selected?.id || openId;
    if (!id) return;
    if (openId !== id) await openItem(id, false);
    if (openId !== id) return;
    await tick();
    const control = document.querySelector<HTMLElement>(field === 'Description' ? '.detail #description' : `.detail [aria-label="${field}"]`);
    control?.focus();
    // Property shortcuts open the dropdown directly rather than leaving a closed trigger focused.
    if (control?.getAttribute('role') === 'combobox' && control.getAttribute('aria-expanded') !== 'true') control.click();
  }

  async function propertyMenu(mode: Exclude<Menu, 'commands'>) {
    if (readonly || moving) return;
    if (openId && (document.activeElement?.closest('.detail') || selectedId === openId)) { void editSelected({ status: 'Ticket status', assignee: 'Add assignee', tags: 'Add tag', type: 'Ticket type' }[mode]); return; }
    if (!selected || !(await guardDraft())) return;
    menuItem = selected; showPalette(mode);
  }

  function assignMe() {
    if (!user || readonly) return;
    if (openId && (document.activeElement?.closest('.detail') || selectedId === openId)) { editor?.assignSelf(); return; }
    if (selected) void updateItem(selected, { [selected.assignees.includes(user.id) ? 'remove_assignees' : 'add_assignees']: [user.id] }, selected.assignees.includes(user.id) ? 'Unassigned from you' : 'Assigned to you');
  }

  async function adjacentItem(direction: number) {
    const next = boardItems[openedIndex + direction];
    if (openedIndex >= 0 && next) { await openItem(next.id, false); await tick(); document.querySelector<HTMLElement>('.detail')?.focus(); }
  }

  function updateURL(push = false) {
    const url = new URL(location.href);
    for (const [key, value] of Object.entries({ q: query, status: filterStatus, tag: filterTag, assignee: filterAssignee, item: openId })) {
      if (value) url.searchParams.set(key, value); else url.searchParams.delete(key);
    }
    if (url.href !== location.href) history[push ? 'pushState' : 'replaceState']({}, '', url);
  }

  async function guardDraft() {
    if (quickTitle.trim()) { notice = 'Finish or cancel the new ticket first.'; return false; }
    if (editor && !(await editor.flush())) {
      notice = 'Changes could not be saved. Resolve the item panel before leaving.';
      document.querySelector<HTMLElement>('.detail')?.focus();
      return false;
    }
    notice = ''; return true;
  }

  async function login(event: SubmitEvent) {
    event.preventDefault(); signingIn = true; loginError = '';
    try {
      const session = await api<Session>('/auth/login', 'POST', { email, password });
      await signedIn(session);
    } catch (e) { loginError = message(e); }
    finally { signingIn = false; }
  }

  async function signedIn(session: Session) {
    if (user && user.id !== session.user.id) {
      items = []; detail = null; openId = ''; quickStatus = null; quickTitle = ''; creating = false; dirty = false; selectedId = ''; filterAssignee = '';
    }
    if (inviteToken !== null) {
      const url = new URL(location.href); url.hash = ''; history.replaceState(null, '', url);
      inviteToken = null;
    }
    setSession(session.session_token); user = session.user; password = ''; authExpired = false; loginNotice = '';
    await start();
  }

  function message(e: unknown) { return e instanceof Error ? e.message : 'Something went wrong. Please try again.'; }

  function showTags() {
    if (readonly) return;
    tagsReturn = document.activeElement as HTMLElement; managingTags = true;
  }
  async function closeTags() {
    managingTags = false; await tick();
    if (tagsReturn?.isConnected) tagsReturn.focus(); else focusBoard();
  }
  async function tagDeleted(name: string) {
    tags = tags.filter(tag => tag !== name);
    if (filterTag === name) { filterTag = ''; updateURL(); }
    await refresh(false);
  }

  function showAccount(view: 'menu' | 'profile' | 'password' = 'menu') {
    accountReturn = document.activeElement as HTMLElement; account = view;
  }
  async function closeAccount() {
    account = null; await tick();
    if (accountReturn?.isConnected) accountReturn.focus(); else focusBoard();
  }
  function expireSession() {
    if (authExpired) return;
    controller?.abort(); generation++; detailGeneration++; syncing = false; busy = false;
    authExpired = true; account = null; managingTags = false; setSession(''); stopLive(); connection = 'offline';
    loginError = 'Your session expired. Sign in again to continue. Your open draft is preserved.';
  }
  async function passwordChanged() {
    email = user?.email || email;
    expireSession(); loginError = ''; loginNotice = 'Password changed. Sign in again with your new password.';
    await tick(); document.querySelector<HTMLInputElement>('.login-form input[type="password"]')?.focus();
  }

  async function logout() {
    if (!(await guardDraft())) return;
    try { await api('/auth/logout', 'POST'); }
    catch (e) { if (!(e instanceof APIError && e.status === 401)) { error = message(e); return; } }
    stopLive(); controller?.abort(); generation++; detailGeneration++;
    setSession(''); user = null; authExpired = false; inviting = false; installing = false; account = null; items = []; detail = null; openId = ''; selectedId = ''; creating = false;
    users = []; tags = []; managingTags = false; password = ''; quickStatus = null; quickTitle = ''; hasLoaded = false; updateURL();
  }

  async function loadDirectory(board?: Board, signal?: AbortSignal) {
    const actor = user?.id;
    const [nextUsers, nextTags] = await Promise.all([
      directory<User>('/users', 'users', board?.users.users, board?.users.next_after, signal),
      directory<string>('/tags', 'tags', board?.tags.tags, board?.tags.next_after, signal),
    ]);
    if (actor !== user?.id || authExpired || signal?.aborted) return;
    users = nextUsers; tags = nextTags;
    const current = nextUsers.find(u => u.id === user?.id);
    if (current) userChanged(current);
  }

  function userChanged(changed: User) {
    users = users.map(u => u.id === changed.id ? changed : u);
    if (user?.id === changed.id) {
      if (changed.removed_at) { window.dispatchEvent(new Event('tiki:expired')); return; }
      user = changed;
      if (changed.role !== 'admin') inviting = false;
    }
  }

  function stopLive() {
    stopEvents?.(); stopEvents = undefined;
    clearTimeout(timer); pendingRefresh = false; pendingUsers = false; pendingItems.clear();
  }

  function startLive() {
    stopLive();
    if (stopped || !user || authExpired) return;
    if (document.hidden) { streamState = connection = 'paused'; return; }
    stopEvents = watchChanges(updates => {
      pendingRefresh ||= Boolean(updates.reset);
      pendingUsers ||= Boolean(updates.users);
      if (!pendingRefresh) for (const item of updates.items || []) {
        if (item.version > (pendingItems.get(item.id)?.version || 0)) pendingItems.set(item.id, item);
      }
      // Bound queued data while a slow refresh or local write is in progress.
      if (pendingItems.size > 64 || [...pendingItems.values()].reduce((n, item) => n + (item.description?.length || 0), 0) > 2 << 20) pendingRefresh = true;
      if (pendingRefresh) pendingItems.clear();
      clearTimeout(timer); timer = setTimeout(flushChanges, 100);
    }, state => { streamState = connection = state; });
  }

  async function flushChanges() {
    if ((!pendingRefresh && !pendingUsers && !pendingItems.size) || stopped || !user || authExpired || document.hidden) return;
    if (flushingChanges || syncing || busy || moving || creating || deleting) { timer = setTimeout(flushChanges, 100); return; }
    const stream = stopEvents;
    const reset = pendingRefresh || !hasLoaded;
    const directoryChanged = pendingUsers;
    const incoming = [...pendingItems.values()];
    flushingChanges = true;
    pendingRefresh = false; pendingUsers = false; pendingItems.clear();
    let loaded = false;
    try {
      if (!reset && directoryChanged) await loadDirectory();
      if (stream !== stopEvents) return;
      if (reset) loaded = await refresh(false);
      else {
        const affected = await mergeItems(incoming);
        loaded = !affected.size || await refresh(false, false, affected, false);
      }
    } catch (e) { if (stream === stopEvents) { error = message(e); connection = 'offline'; } }
    finally { flushingChanges = false; }
    if (stream !== stopEvents) return;
    if (loaded) { failures = 0; connection = streamState; }
    else { pendingRefresh = true; failures = Math.min(failures + 1, 5); }
    if (pendingRefresh || pendingUsers || pendingItems.size) {
      clearTimeout(timer);
      timer = setTimeout(flushChanges, loaded ? 100 : Math.min(30000, 1000 * 2 ** failures));
    }
  }

  // Keep cards keyed by ID and replace only newer versions. Drafts are owned by
  // ItemEditor, which detects a newer base without overwriting unsaved fields.
  async function mergeItems(incoming: Item[], applyOrder = false): Promise<Set<Status>> {
    const affected = new Set<Status>();
    const focused = document.activeElement;
    const boardFocused = Boolean(focused?.closest('.board'));
    const compare = (a: Item, b: Item) => a.priority - b.priority || (BigInt(a.id) < BigInt(b.id) ? -1 : BigInt(a.id) > BigInt(b.id) ? 1 : 0);
    const byId = new Map(items.map(item => [item.id, item]));
    const newTags = new Set(tags);
    for (const item of incoming) {
      if (item.id === openId && (!detail || item.version > detail.version)) detail = item;
      for (const tag of item.tags) newTags.add(tag);
      const previous = byId.get(item.id);
      if (previous && previous.version >= item.version) continue;
      const matches = (!filterStatus || item.status === filterStatus)
        && (!filterTag || item.tags.includes(filterTag))
        && (!filterAssignee || (filterAssignee === 'none' ? !item.assignees.length : item.assignees.includes(filterAssignee)));
      const inWindow = !cursors[item.status] || previous?.status === item.status
        || items.some(card => card.status === item.status && compare(item, card) <= 0);
      const membershipChanged = !previous || !matches || previous.status !== item.status || previous.priority !== item.priority;
      // Updates beyond a partially loaded column's boundary need no request.
      // A partial column needs the server to fill holes and maintain its cursor.
      if (membershipChanged) {
        if (previous && cursors[previous.status]) affected.add(previous.status);
        if (matches && inWindow && cursors[item.status]) affected.add(item.status);
      }
      if (!matches || !inWindow) byId.delete(item.id);
      else if (previous || !cursors[item.status]) {
        const { description, ...card } = item;
        byId.set(item.id, card);
      }
    }
    items = [...byId.values()];
    if (newTags.size !== tags.length) tags = [...newTags].sort();
    if (applyOrder) { items.sort(compare); orderChanged = false; }
    else orderChanged = columns.some(status => {
      const column = items.filter(item => item.status === status);
      const sorted = [...column].sort(compare);
      return column.some((item, index) => item.id !== sorted[index].id);
    });
    const current = visible.find(item => item.id === selectedId);
    if (current) activeColumn = current.status;
    else selectedId = visible.find(item => item.status === activeColumn)?.id || '';
    await tick();
    if (boardFocused && document.activeElement !== focused) focusBoard();
    lastSync = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
    return affected;
  }

  async function applySaved(item: Item) {
    const affected = await mergeItems([item], true);
    if (affected.size) await refresh(true, false, affected, false);
  }

  function start() {
    loadedPages = {}; failures = 0;
    startLive();
  }

  async function refresh(applyOrder = false, reset = false, only?: Set<Status>, includeDetail = true): Promise<boolean> {
    if (!user || authExpired) return false;
    if (reset) loadedPages = {};
    controller?.abort(); controller = new AbortController();
    const signal = controller.signal;
    const own = ++generation;
    const targetId = openId;
    syncing = true;
    if (applyOrder || !hasLoaded) busy = true;
    try {
      const filters = new URLSearchParams();
      const requestedTag = filterTag;
      if (filterStatus) filters.set('status', filterStatus);
      if (filterTag) filters.set('tag', filterTag);
      if (filterAssignee) filters.set('assignee', filterAssignee);
      // One snapshot includes all column previews and the initial directories.
      const board = only ? undefined : await api<Board>(`/board?${filters}`, 'GET', undefined, signal);
      if (board) await loadDirectory(board, signal);
      if (own !== generation) return false;
      // Another session may delete the active tag. Remove that obsolete filter
      // and fetch the remaining view instead of leaving an empty, hidden filter.
      if (board && requestedTag && filterTag === requestedTag && !tags.includes(requestedTag)) {
        filterTag = ''; updateURL(); return refresh(applyOrder, true);
      }
      const refreshedColumns = columns.filter(status => !only || only.has(status));
      const pages = await Promise.all(refreshedColumns.map(async status => {
        const preview = !filterStatus && ['backlog', 'complete', 'void'].includes(status) ? 20 : 100;
        const params = new URLSearchParams({ status });
        if (filterTag) params.set('tag', filterTag);
        if (filterAssignee) params.set('assignee', filterAssignee);
        const rows: Item[] = [];
        let nextCursor = '';
        for (let i = 0; i < (loadedPages[status] || 1); i++) {
          if (nextCursor) params.set('cursor', nextCursor);
          params.set('limit', String(i === 0 ? preview : 100));
          const page = i === 0 && board ? board.columns[status]! : await api<Page>(`/items?${params}`, 'GET', undefined, signal);
          rows.push(...page.items); nextCursor = page.next_cursor || '';
          if (!nextCursor) break;
        }
        return { status, rows, cursor: nextCursor };
      }));
      const next = [...items.filter(item => only && !refreshedColumns.includes(item.status)), ...pages.flatMap(p => p.rows)];
      if (own !== generation) return false;
      const byVersion = new Map<string, Item>();
      for (const item of next) if (!byVersion.has(item.id) || item.version > byVersion.get(item.id)!.version) byVersion.set(item.id, item);
      const unique = [...byVersion.values()];
      const boardFocused = Boolean(document.activeElement?.closest('.board'));
      const focusedId = document.activeElement?.getAttribute('data-ticket');
      if (applyOrder || !hasLoaded) { items = unique; orderChanged = false; }
      else {
        const byId = new Map(unique.map(i => [i.id, i]));
        const stable = items.filter(i => byId.has(i.id)).map(i => byId.get(i.id)!);
        const oldIds = new Set(stable.map(i => i.id));
        const merged = [...stable, ...unique.filter(i => !oldIds.has(i.id))];
        orderChanged = columns.some(status => {
          const expected = unique.filter(i => i.status === status);
          return merged.filter(i => i.status === status).some((i, index) => i.id !== expected[index]?.id);
        });
        items = merged;
      }
      cursors = { ...(only ? cursors : {}), ...Object.fromEntries(pages.map(p => [p.status, p.cursor])) };
      const current = visible.find(i => i.id === selectedId);
      if (current) activeColumn = current.status;
      else {
        if (!columns.includes(activeColumn)) activeColumn = columns[0];
        selectedId = visible.filter(i => i.status === activeColumn)[Math.min(preferredRow, visible.filter(i => i.status === activeColumn).length - 1)]?.id || '';
      }
      if (boardFocused && (focusedId || !document.activeElement?.isConnected)) { await tick(); focusBoard(); }
      hasLoaded = true; connection = streamState;
      lastSync = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
      // A detail can be outside the loaded/filter window, so it gets its own versioned refresh.
      if (includeDetail && targetId && !creating) {
        try {
          const incoming = await api<Item>(`/items/${targetId}`, 'GET', undefined, signal);
          if (own === generation && targetId === openId && (!detail || incoming.version > detail.version)) detail = incoming;
        } catch (e) {
          if (!(e instanceof APIError && e.status === 404)) throw e;
          if (own === generation && targetId === openId) detailDeleted = true;
        }
      }
      return true;
    } catch (e) {
      if (signal.aborted || own !== generation) return false;
      if (e instanceof APIError && e.code === 'cursor_expired' && Object.values(loadedPages).some(n => n > 1)) {
        loadedPages = {}; notice = 'Order changed; loaded pages reset.';
        return refresh(true);
      }
      connection = 'offline'; error = message(e); return false;
    } finally { if (own === generation) { syncing = false; busy = false; } }
  }

  async function fetchDetail(id: string) {
    const own = ++detailGeneration;
    detailLoading = true;
    try {
      const incoming = await api<Item>(`/items/${id}`);
      if (own === detailGeneration && openId === id && (!detail || detail.id !== id || incoming.version >= detail.version)) detail = incoming;
    } catch (e) {
      if (own === detailGeneration) {
        if (e instanceof APIError && e.status === 404) detailDeleted = true;
        else error = message(e);
      }
    }
    finally { if (own === detailGeneration) detailLoading = false; }
  }

  async function openItem(id: string, focus = true) {
    if (openId === id && !creating) { if (focus) { await tick(); document.querySelector<HTMLElement>('.detail')?.focus(); } return; }
    if (!(await guardDraft())) return;
    quickStatus = null; openId = id; selectedId = id; detail = null; detailDeleted = false; updateURL(true);
    await fetchDetail(id);
    if (focus && openId === id) { await tick(); document.querySelector<HTMLElement>(readonly || coarse ? '.detail' : '.title-editor')?.focus(); }
  }

  async function create(status: Status = activeColumn) {
    if (!(await guardDraft()) || readonly) return;
    if (!columns.includes(status)) status = columns[0] || 'todo';
    openId = ''; detail = null; detailDeleted = false; detailGeneration++; updateURL(true);
    quickStatus = status; quickTitle = ''; activeColumn = status;
    await tick(); document.getElementById('quick-title')?.focus();
  }

  async function quickCreate(event: SubmitEvent, edit = false) {
    event.preventDefault();
    if (readonly || !quickTitle.trim() || !quickStatus || creating) return;
    creating = true; error = '';
    controller?.abort(); generation++; syncing = false; busy = false;
    try {
      const item = await api<Item>('/items', 'POST', {
        title: quickTitle, type: 'task', status: quickStatus,
        tags: filterTag ? [filterTag] : [],
        assignees: users.some(u => u.id === filterAssignee && !u.removed_at) ? [filterAssignee] : [],
      });
      quickTitle = ''; selectedId = item.id;
      await applySaved(item);
      announcement = `Created TK-${item.id}`;
      if (edit) { quickStatus = null; creating = false; await openItem(item.id); }
      else { await tick(); document.getElementById('quick-title')?.focus(); }
    } catch (e) { error = message(e) + (!(e instanceof APIError) ? ' Check the board before retrying; the item may have been created.' : ''); }
    finally { creating = false; }
  }

  async function updateItem(item: Item, patch: Record<string, unknown>, feedback: string) {
    if (!(await guardDraft()) || readonly || moving) return;
    item = items.find(current => current.id === item.id) || item;
    moving = true; error = ''; controller?.abort(); generation++; syncing = false; busy = false;
    try {
      const updated = await api<Item>(`/items/${item.id}`, 'PATCH', { version: item.version, ...patch });
      activeColumn = updated.status; selectedId = item.id;
      await applySaved(updated); await tick(); focusBoard();
      announcement = `TK-${item.id} · ${feedback}`;
    } catch (e) { error = message(e); }
    finally { moving = false; dragging = ''; }
  }

  async function changeStatus(id: string, status: Status) {
    const item = items.find(i => i.id === id);
    if (item && item.status !== status) await updateItem(item, { status }, `Moved to ${label(status)}`);
  }

  async function closeDetails() {
    if (!(await guardDraft())) return;
    openId = ''; detail = null; detailDeleted = false; detailGeneration++; updateURL(true);
    await tick(); focusBoard();
  }

  async function saved(item: Item) {
    if (detailDeleted && item.id === openId) return;
    notice = ''; await applySaved(item);
  }

  async function requestDelete(item: Item | null | undefined) {
    if (!item || readonly || authExpired || moving || deleting || deleteTarget) return;
    // A different open ticket must keep its edits. The target's draft is covered
    // by the deletion confirmation, including invalid or failed edits.
    if (item.id !== openId && !(await guardDraft())) return;
    deleteReturn = document.activeElement as HTMLElement;
    deleteTarget = item; deleteError = ''; deleteConflict = false; deleting = true;
    await tick(); deleteDialog.showModal();
    // Pause autosave, then let any already-sent save finish before capturing its version.
    if (item.id === openId) await editor?.settle();
    deleteTarget = (detail?.id === item.id ? detail : items.find(i => i.id === item.id)) || item;
    deleting = false;
    await tick(); deleteDialog.querySelector<HTMLButtonElement>('button')?.focus();
  }

  async function cancelDelete() {
    if (deleting) return;
    deleteDialog?.close(); deleteTarget = null;
    await tick();
    if (deleteReturn?.isConnected) deleteReturn.focus(); else focusBoard();
  }

  async function deleteItem() {
    if (!deleteTarget || deleting || readonly || authExpired || deleteConflict) return;
    const item = deleteTarget;
    deleting = true; deleteError = '';
    controller?.abort(); generation++; syncing = false; busy = false;
    try {
      try { await api(`/items/${item.id}`, 'DELETE', { version: item.version }); }
      catch (e) { if (!(e instanceof APIError && e.status === 404)) throw e; }
      items = items.filter(i => i.id !== item.id); pendingItems.delete(item.id);
      if (openId === item.id) { openId = ''; detail = null; detailDeleted = false; dirty = false; detailGeneration++; updateURL(); }
      deleteDialog.close(); deleteTarget = null;
      announcement = `Deleted TK-${item.id}`;
      await tick(); focusBoard();
      await refresh(false, false, new Set([item.status]), false);
    } catch (e) {
      deleteConflict = e instanceof APIError && e.status === 409;
      deleteError = deleteConflict ? 'This ticket changed. Cancel and review the latest version before deleting.' : message(e);
      if (deleteConflict) await refresh(false);
    } finally { deleting = false; }
  }

  async function clearFilters() { if (!(await guardDraft())) return; filterTag = ''; setView('', ''); }
  async function setView(status: string, assignee: string) {
    if (!(await guardDraft())) return;
    filterStatus = status; filterAssignee = assignee; query = ''; updateURL(true); void refresh(true, true);
  }
  function filterChanged() {
    if (quickStatus && quickTitle.trim() && filterStatus && filterStatus !== quickStatus) filterStatus = quickStatus;
    updateURL(true); void refresh(true, true);
  }
  async function more(status: Status) { loadedPages[status] = (loadedPages[status] || 1) + 1; await refresh(true, false, new Set([status]), false); }

  async function move(direction: number) {
    const column = visible.filter(i => i.status === activeColumn);
    const index = column.findIndex(i => i.id === selectedId);
    const anchor = column[index + direction];
    if (selected && anchor) await moveTo(selected, anchor, direction < 0);
  }

  async function moveTo(current: Item, anchor: Item, before: boolean) {
    if (current.id === anchor.id || !(await guardDraft()) || readonly || moving) return;
    current = items.find(item => item.id === current.id) || current;
    moving = true; error = '';
    controller?.abort(); generation++; syncing = false; busy = false;
    let changedStatus = false;
    try {
      if (current.status !== anchor.status) {
        current = await api<Item>(`/items/${current.id}`, 'PATCH', { version: current.version, status: anchor.status });
        changedStatus = true;
        await applySaved(current);
      }
      const updated = await api<Item>(`/items/${current.id}/move`, 'POST', { version: current.version, [before ? 'before' : 'after']: anchor.id });
      activeColumn = updated.status; selectedId = updated.id;
      await applySaved(updated); await tick(); focusBoard();
      announcement = `TK-${updated.id} moved ${before ? 'before' : 'after'} TK-${anchor.id}`;
    } catch (e) { error = (changedStatus ? 'Status changed, but reordering failed. ' : '') + message(e); }
    finally { moving = false; dragging = ''; dropTarget = ''; }
  }

  async function showHelp() { helpReturn = document.activeElement as HTMLElement; help = true; await tick(); helpDialog.showModal(); }
  async function closeHelp() { helpDialog.close(); help = false; await tick(); if (helpReturn?.isConnected) helpReturn.focus(); else focusBoard(); }
  function showPalette(mode: Menu = 'commands') { paletteReturn = document.activeElement as HTMLElement; palette = mode; }
  async function closePalette() { palette = null; await tick(); if (paletteReturn?.isConnected) paletteReturn.focus(); else focusBoard(); }

  function keyboard(event: KeyboardEvent) {
    if (!user || authExpired || event.isComposing || event.defaultPrevented) return;
    const target = event.target as HTMLElement;
    const editing = target.closest('input, textarea, select, [role="combobox"], [contenteditable]:not([contenteditable="false"])');
    if (help || moveSheet || inviting || installing || deleteTarget || account || managingTags) return;
    if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'k' && !event.altKey) { event.preventDefault(); if (palette) void closePalette(); else showPalette(); return; }
    if (palette) return;
    if (event.key === 'F6') { event.preventDefault(); if (target.closest('.detail')) { if (matchMedia('(max-width: 800px)').matches) void closeDetails(); else focusBoard(); } else if (openId) document.querySelector<HTMLElement>('.detail')?.focus(); else searchInput.focus(); return; }
    if (event.key === 'Escape') {
      event.preventDefault(); lastG = 0;
      if (quickStatus && !creating) { quickStatus = null; quickTitle = ''; void tick().then(focusBoard); }
      else if (editing && target.closest('.detail')) document.querySelector<HTMLElement>('.detail')?.focus();
      else if (target === searchInput || target.closest('.toolbar')) focusBoard();
      else if (openId) void closeDetails();
      else focusBoard();
      return;
    }
    if (target === searchInput && ['Enter', 'ArrowDown'].includes(event.key)) { event.preventDefault(); focusColumn(boardItems[0]?.status || activeColumn, 0); return; }
    if (editing || event.ctrlKey || event.metaKey) return;
    const key = event.key;
    if (key === 'Delete' && !event.altKey && !event.shiftKey && !event.repeat && (target.closest('.detail, .board') || target === document.body)) {
      event.preventDefault(); void requestDelete(target.closest('.detail') ? detail : selected); return;
    }
    if (key !== 'g') lastG = 0;
    const lower = key.toLowerCase();
    if (!event.altKey && ['/', 'c', '?', 'r'].includes(key)) {
      event.preventDefault();
      if (key === '/') { searchInput.focus(); searchInput.select(); }
      if (key === 'c' && !event.repeat) void create();
      if (key === '?') void showHelp();
      if (key === 'r') void refresh(true);
      return;
    }
    if (!event.altKey && !event.shiftKey && ['e', 'i', 'd', 'a', 's', 't', 'y', 'm'].includes(key)) {
      event.preventDefault(); if (event.repeat || readonly) return;
      if (key === 'e' || key === 'i') void editSelected();
      if (key === 'd') void editSelected('Description');
      if (key === 'm') assignMe();
      if (key === 'a') propertyMenu('assignee');
      if (key === 's') propertyMenu('status');
      if (key === 't') propertyMenu('tags');
      if (key === 'y') propertyMenu('type');
      return;
    }
    if (target.closest('.detail')) {
      if (!event.altKey && ['j', 'k', '[', ']'].includes(key)) { event.preventDefault(); void adjacentItem(['j', ']'].includes(key) ? 1 : -1); }
      return;
    }
    // Directional navigation only takes over on the board (or the unfocused page).
    if (!target.closest('.board') && target !== document.body && target !== document.documentElement) return;
    if (!event.altKey && /^[1-7]$/.test(key)) { event.preventDefault(); const status = columns[Number(key) - 1]; if (status) focusColumn(status, preferredRow); return; }
    if (!event.altKey && (key === 'Home' || key === 'End' || key === 'G' || key === 'g')) {
      event.preventDefault();
      if (key === 'g') { const now = Date.now(); if (now - lastG > 700) { lastG = now; return; } lastG = 0; }
      const row = key === 'G' || key === 'End' ? Math.max(0, visible.filter(i => i.status === activeColumn).length - 1) : 0;
      focusColumn(activeColumn, row); return;
    }
    lastG = 0;
    if (['arrowdown', 'arrowup', 'j', 'k'].includes(lower)) {
      event.preventDefault();
      const direction = key === 'ArrowDown' || lower === 'j' ? 1 : -1;
      if (event.altKey || (event.shiftKey && ['J', 'K'].includes(key))) { void move(direction); return; }
      const column = visible.filter(i => i.status === activeColumn);
      const index = column.findIndex(i => i.id === selectedId);
      focusColumn(activeColumn, Math.min(Math.max(index + direction, 0), Math.max(0, column.length - 1)));
    } else if (['arrowleft', 'arrowright', 'h', 'l'].includes(lower)) {
      event.preventDefault();
      const direction = key === 'ArrowRight' || lower === 'l' ? 1 : -1;
      if (event.altKey || (event.shiftKey && ['H', 'L'].includes(key))) {
        const status = selected && statuses[statuses.indexOf(selected.status) + direction];
        if (status) void changeStatus(selectedId, status);
      } else {
        const status = columns[columns.indexOf(activeColumn) + direction];
        if (status) focusColumn(status, preferredRow);
      }
    } else if (key === 'Enter' && selected && !target.closest('button')) { event.preventDefault(); void openItem(selected.id); }
  }

  onMount(() => {
    if (inviteToken === null && hasSession()) void api<User>('/auth/me').then(async value => { user = value; await start(); }).catch(e => { setSession(''); loginError = message(e); }).finally(() => checking = false);
    const expired = expireSession;
    const visibility = () => { if (document.hidden) { stopLive(); streamState = connection = 'paused'; } else startLive(); };
    const online = () => startLive();
    const unload = (event: BeforeUnloadEvent) => { if (dirty || quickTitle.trim()) event.preventDefault(); };
    const hashchange = async () => {
      const url = new URL(location.href);
      if (new URLSearchParams(url.hash.slice(1)).get('invite') === inviteToken) return;
      if (!(await guardDraft())) {
        url.hash = inviteToken === null ? '' : `invite=${encodeURIComponent(inviteToken)}`;
        history.replaceState(null, '', url);
        return;
      }
      // Pasting a link into an already-open tab is a fragment-only navigation.
      // Reload to start onboarding with the same clean state as opening a new tab.
      location.reload();
    };
    const popstate = async () => {
      if (!(await guardDraft())) { updateURL(); return; }
      const url = new URL(location.href);
      query = url.searchParams.get('q') || ''; filterStatus = url.searchParams.get('status') || '';
      filterTag = url.searchParams.get('tag') || ''; filterAssignee = url.searchParams.get('assignee') || '';
      openId = url.searchParams.get('item') || ''; creating = false; detail = null; detailDeleted = false;
      if (openId) { selectedId = openId; void fetchDetail(openId); }
      void refresh(true, true);
    };
    window.addEventListener('tiki:expired', expired);
    document.addEventListener('visibilitychange', visibility);
    window.addEventListener('online', online);
    window.addEventListener('beforeunload', unload);
    window.addEventListener('popstate', popstate);
    window.addEventListener('hashchange', hashchange);
    return () => {
      stopped = true; stopLive(); controller?.abort();
      window.removeEventListener('tiki:expired', expired); document.removeEventListener('visibilitychange', visibility);
      window.removeEventListener('online', online); window.removeEventListener('beforeunload', unload); window.removeEventListener('popstate', popstate);
      window.removeEventListener('hashchange', hashchange);
    };
  });

</script>

<svelte:window onkeydown={keyboard} />

{#if inviteToken !== null}
  <Join token={inviteToken} onjoin={signedIn} />
{:else if !user || authExpired}
  <div class="login-screen">
    <form class="login-form" onsubmit={login}>
      <h1><span class="logo-mark" aria-hidden="true"></span>tiki</h1><p class="login-sub">Sign in to your workspace</p>
      {#if loginError}<p class="error-banner" role="alert">{loginError}</p>{/if}
      {#if loginNotice}<p class="notice-banner" role="status">{loginNotice}</p>{/if}
      <label>Email<input type="email" autocomplete="username" required bind:value={email} disabled={checking || signingIn} /></label>
      <label>Password<input type="password" autocomplete="current-password" required bind:value={password} disabled={checking || signingIn} /></label>
      <button class="primary-button" disabled={checking || signingIn}>{checking ? 'Restoring session…' : signingIn ? 'Signing in…' : 'Sign in'}</button>
    </form>
  </div>
{/if}

{#if user && inviteToken === null}
  <main class="app" inert={authExpired}>
    <div class="toolbar">
      <span class="wordmark"><span class="logo-mark" aria-hidden="true"></span>tiki</span>
      <div class="search-field" class:open={searchOpen || Boolean(query)}><Icon name="search" size={14} /><input aria-label="Search loaded items" placeholder="Filter loaded items…" bind:value={query} bind:this={searchInput} oninput={() => updateURL()} onblur={() => { if (!query) searchOpen = false; }} /><kbd>/</kbd><button class="icon-button search-close mobile-only" aria-label="Close search" onmousedown={event => event.preventDefault()} onclick={closeSearch}><Icon name="close" size={16} /></button></div>
      <div class="filters" class:open={filtersOpen} inert={mobile && !filtersOpen}>
        <div class="sheet-header mobile-only"><h2>Filters</h2><button class="text-button" onclick={() => filtersOpen = false}>Done</button></div>
        <Select label="Filter assignee" title="Assignee" variant="filter" placeholder="Assignee" placeholderIcon="person" bind:value={filterAssignee} onchange={filterChanged} options={[{ value: '', label: 'Any assignee', icon: 'person' }, { value: 'none', label: 'Unassigned', icon: 'unassigned' }, ...users.map(u => ({ value: u.id, label: u.removed_at ? `${u.name} (removed)` : u.id === user?.id ? `${u.name} (me)` : u.name, avatar: initials(u.name), avatarHue: avatarHue(u.id) }))]} />
        <Select label="Filter tag" title="Tag" variant="filter" placeholder="Tag" placeholderIcon="tag" bind:value={filterTag} onchange={filterChanged} options={[{ value: '', label: 'Any tag', icon: 'tag' }, ...tags.map(tag => ({ value: tag, label: tag, icon: 'hash' })), ...(!readonly ? [{ value: '\u0000manage-tags', label: 'Manage tags…', icon: 'tag', action: showTags }] : [])]} />
        <Select label="Filter status" title="Status" variant="filter" placeholder="Status" placeholderIcon="status" bind:value={filterStatus} onchange={filterChanged} options={[{ value: '', label: 'Any status', icon: 'status' }, ...statuses.map(status => ({ value: status, label: label(status), icon: status, iconClass: `status-icon ${status}` }))]} />
        {#if filterTag || filterAssignee || filterStatus || query}<button class="icon-button" aria-label="Clear filters" title="Clear filters" onclick={clearFilters}><Icon name="clear-filter" size={15} /><span class="mobile-only">Clear all</span></button>{/if}
      </div>
      <span class="toolbar-spacer"></span>
      {#if orderChanged}<button class="text-button order-notice" onclick={() => refresh(true)}>Apply order</button>{/if}
      <span class={`connection ${connection}`} title={`Updates arrive live. ${lastSync ? `Last sync ${lastSync}.` : ''}`}><span class="tiny-dot"></span><span>{connection === 'live' ? 'Live' : connection === 'offline' ? 'Offline' : connection === 'paused' ? 'Paused' : 'Syncing'}</span></span>
      <button class="icon-button mobile-only" aria-label="Search" onclick={openSearch}><Icon name="search" size={18} /></button>
      <button class="icon-button mobile-only filter-toggle" aria-label="Filters" aria-expanded={filtersOpen} onclick={() => filtersOpen = !filtersOpen}><Icon name="filter" size={18} />{#if activeFilters}<span class="badge">{activeFilters}</span>{/if}</button>
      <button class="icon-button desktop-only" aria-label="Refresh board" title="Refresh (R)" disabled={busy} onclick={() => { error = ''; void refresh(true); }}><Icon name="refresh" size={15} /></button>
      {#if user.role === 'admin'}<button class="icon-button" aria-label="Manage people" title="Manage people" onclick={() => inviting = true}><Icon name="add-person" size={17} /></button>{/if}
      <button class="icon-button desktop-only" aria-label="CLI & agents" title="CLI & agents" onclick={() => installing = true}><Icon name="terminal" size={17} /></button>
      <button class="icon-button" aria-label="Commands" title="Commands (Ctrl/Cmd+K)" onclick={() => showPalette()}><span class="desktop-only"><Icon name="command" size={15} /></span><span class="mobile-only"><Icon name="more" size={18} /></span></button>
      <button class="icon-button desktop-only" aria-label="Keyboard shortcuts" title="Keyboard shortcuts (?)" onclick={showHelp}><Icon name="keyboard" size={16} /></button>
      <button class="icon-button" aria-label="Account menu" aria-haspopup="dialog" aria-expanded={Boolean(account)} title={user.name} onclick={() => showAccount()}><span class="mini-avatar account-avatar" style:--hue={avatarHue(user.id)} aria-hidden="true">{initials(user.name)}</span></button>
    </div>
    {#if error}<div class="board-alert error-banner" role="alert"><span>{error}</span><button class="text-button" onclick={() => { error = ''; void refresh(true); }}>Retry</button></div>{/if}
    {#if notice}<div class="board-alert notice-banner" role="status">{notice}<button class="icon-button" aria-label="Dismiss notice" onclick={() => notice = ''}><Icon name="close" size={13} /></button></div>{/if}
    {#if filtersOpen}<button class="sheet-backdrop" aria-label="Close filters" tabindex="-1" onclick={() => filtersOpen = false}></button>{/if}
    {#if mobile}<div class="status-tabs" role="group" bind:this={tabStrip} aria-label="Statuses">{#each columns as status}<button data-status={status} class:active={feedStatus === status} aria-current={feedStatus === status ? 'true' : undefined} onclick={() => showStatus(status)}><span class={`status-icon ${status}`}><Icon name={status} size={14} /></span>{label(status)}<span class="tab-count">{visible.filter(i => i.status === status).length}{cursors[status] ? '+' : ''}</span></button>{/each}</div>{/if}
    <div class="board" bind:this={board} onscroll={feedScrolled} aria-label="Items by status" aria-describedby="board-keyboard-hint">
      {#each columns as status}
        {@const columnItems = visible.filter(i => i.status === status)}
        <!-- Native drag/drop is an additional input; the same action is available through Alt+arrows and the editor. -->
        <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
        <section data-column={status} class="kanban-column" class:drop-target={dragging && items.find(i => i.id === dragging)?.status !== status} aria-label={`${label(status)} column`} ondragover={event => { if (dragging && !readonly) event.preventDefault(); }} ondrop={event => { event.preventDefault(); if (dragging) void changeStatus(dragging, status); }}>
          <div class="column-header"><button id={`column-${status}`} class="column-focus" tabindex={activeColumn === status && !selected ? 0 : -1} onfocus={() => { activeColumn = status; selectedId = ''; }} onclick={() => focusColumn(status, 0)}><span class={`status-icon ${status}`}><Icon name={status} size={14} /></span><h2>{label(status)}</h2><span class="column-count" title="Loaded items">{columnItems.length}{cursors[status] ? '+' : ''}</span></button>{#if !readonly}<button class="icon-button column-add" aria-label={`Add item to ${label(status)}`} title="Add item (C)" onfocus={() => { activeColumn = status; selectedId = ''; }} onclick={() => create(status)}><Icon name="plus" size={15} /></button>{/if}</div>
          <div class="column-scroll">
            {#if quickStatus === status}<form class="quick-create" onsubmit={event => quickCreate(event, (event.submitter as HTMLButtonElement)?.value === 'edit')}><textarea id="quick-title" aria-label="New item title" placeholder="Item title" rows="2" maxlength="300" required bind:value={quickTitle} disabled={creating || readonly} onkeydown={event => { if (event.key === 'Enter' && !event.shiftKey && !event.isComposing) { event.preventDefault(); const form = event.currentTarget.form; form?.requestSubmit(event.ctrlKey || event.metaKey ? form.querySelector<HTMLButtonElement>('[value=edit]')! : undefined); } }}></textarea><div><span class="hint"><kbd>↵</kbd> add <kbd>esc</kbd> cancel</span><button type="button" class="text-button mobile-only quick-cancel" onclick={() => { quickStatus = null; quickTitle = ''; }}>Cancel</button><button type="submit" value="edit" class="text-button" title="Add and edit (Ctrl/Cmd+Enter)" disabled={creating || readonly || !quickTitle.trim()}>Add & edit</button><button class="small-button" disabled={creating || readonly || !quickTitle.trim()}>{creating ? 'Adding…' : 'Add'}</button></div></form>{/if}
            <ul class="cards" aria-label={`${label(status)} items`}>
              {#each columnItems as item (item.id)}
                <li data-card={item.id} animate:flip={{ duration: duration(220) }} in:arrive><button id={`ticket-${item.id}`} data-ticket={item.id} tabindex={selectedId === item.id ? 0 : -1} class="card" class:lifted={lifted?.item.id === item.id} class:drop-before={dropTarget === item.id && dropBefore} class:drop-after={dropTarget === item.id && !dropBefore} class:selected={selectedId === item.id} class:opened={openId === item.id} draggable={!readonly && !moving && !dirty && !coarse} ondragstart={event => { dragging = item.id; selectedId = item.id; event.dataTransfer?.setData('text/plain', item.id); }} ondragend={() => { dragging = ''; dropTarget = ''; }}
                  ondragover={event => { if (!dragging || dragging === item.id || readonly) return; event.preventDefault(); event.stopPropagation(); dropTarget = item.id; const rect = event.currentTarget.getBoundingClientRect(); dropBefore = event.clientY < rect.top + rect.height / 2; }}
                  ondragleave={() => { if (dropTarget === item.id) dropTarget = ''; }}
                  ondrop={event => { event.preventDefault(); event.stopPropagation(); const current = items.find(i => i.id === dragging); if (current) void moveTo(current, item, dropBefore); }}
                  onfocus={() => { selectedId = item.id; activeColumn = status; preferredRow = columnItems.indexOf(item); }} onclick={() => { if (suppressClick) { suppressClick = false; return; } void openItem(item.id); }} onpointerdown={event => pressStart(event, item)} onpointermove={pressMove} onpointerup={pressEnd} onpointercancel={pressEnd} oncontextmenu={event => { if (coarse) event.preventDefault(); }} aria-label={`TK-${item.id}: ${item.title}`} aria-current={openId === item.id ? 'true' : undefined}>
                  <span class="card-title">{item.title}</span>
                  <div class="card-meta"><span class={`item-type ${item.type}`} title={label(item.type)}><Icon name={item.type} size={12} /></span><span class="item-id">#{item.id}</span><span class="card-tags">{#each item.tags.slice(0, 2) as tag}<span>{tag}</span>{/each}{#if item.tags.length > 2}<span>+{item.tags.length - 2}</span>{/if}</span><span class="card-assignees">{#each item.assignees.slice(0, 2) as id}<span class="mini-avatar" style:--hue={avatarHue(id)} title={users.find(u => u.id === id)?.name || id}>{initials(users.find(u => u.id === id)?.name || id)}</span>{/each}{#if item.assignees.length > 2}<span class="muted">+{item.assignees.length - 2}</span>{/if}</span></div>
                </button></li>
              {/each}
            </ul>
            {#if !hasLoaded}<p class="column-empty">Loading…</p>{:else if !columnItems.length && quickStatus !== status}<p class="column-empty">{query ? 'No matches' : 'No tickets'}</p>{/if}
            {#if cursors[status]}<button class="load-more" disabled={busy} onfocus={() => { activeColumn = status; selectedId = ''; preferredRow = Math.max(0, columnItems.length - 1); }} onclick={() => more(status)}>Load more</button>{/if}
          </div>
        </section>
      {/each}
    </div>
    <div class="board-footer" id="board-keyboard-hint"><span><kbd>↑ ↓ ← →</kbd> / <kbd>h j k l</kbd> navigate</span><span><kbd>Enter</kbd> open</span>{#if !readonly}<span><kbd>C</kbd> create</span>{/if}<span class="board-feedback" role="status" aria-live="polite">{moving ? 'Updating…' : announcement}</span><button class="text-button" onclick={showHelp}><kbd>?</kbd> Shortcuts</button></div>
    {#if !readonly && !openId && !(mobile && quickStatus)}<button class="fab" aria-label="New" title="New ticket (C)" onclick={() => create(mobile ? feedStatus : activeColumn)}><Icon name="plus" size={22} strokeWidth={2} /></button>{/if}
    {#if openId}<div class="detail-shell" transition:panel>{#if detail}{#key openId}<ItemEditor bind:this={editor} currentUserId={user.id} item={detail} {users} {tags} readonly={readonly || authExpired} deleted={detailDeleted} suspended={Boolean(palette || help || inviting || installing || deleteTarget)} onmissing={() => detailDeleted = true} ondelete={() => requestDelete(detail)} canPrevious={openedIndex > 0} canNext={openedIndex >= 0 && openedIndex < boardItems.length - 1} onnavigate={adjacentItem} onclose={closeDetails} onsave={saved} ondirty={value => dirty = value} />{/key}
    {:else}<aside class="detail detail-loading" tabindex="-1" aria-label={`Item ${openId}`}><div class="detail-top"><span>#{openId}</span><button class="icon-button detail-close" aria-label="Close item" onclick={closeDetails}><span class="desktop-only"><Icon name="close" size={16} /></span><span class="mobile-only"><Icon name="back" size={22} /></span></button></div><p>{detailLoading ? 'Loading…' : detailDeleted ? 'This ticket was deleted or is no longer available.' : 'Could not load item.'}</p>{#if !detailLoading && !detailDeleted}<button class="small-button" onclick={() => fetchDetail(openId)}>Retry</button>{/if}</aside>{/if}</div>{/if}
  </main>
{/if}

{#if moveSheet}
  {@const item = moveSheet}
  <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions -->
  <dialog class="action-sheet" bind:this={sheetDialog} aria-label={`Actions for TK-${item.id}`} oncancel={event => { event.preventDefault(); closeMoveSheet(); }} onclick={event => { if (event.target === event.currentTarget && Date.now() - sheetOpenedAt > 400) closeMoveSheet(); }}>
    <div class="sheet-body" in:sheet>
      <div class="sheet-grip" aria-hidden="true"></div>
      <div class="sheet-title"><span class="item-id">TK-{item.id}</span><strong>{item.title}</strong></div>
      <h3>Move to</h3>
      <div class="sheet-group">{#each columns.length > 1 ? columns : statuses as status}<button class="sheet-option" disabled={item.status === status} onclick={() => sheetAction(target => changeStatus(target.id, status))}><span class={`status-icon ${status}`}><Icon name={status} size={18} /></span><span>{label(status)}</span>{#if item.status === status}<span class="sheet-current">Current</span>{/if}</button>{/each}</div>
      <div class="sheet-group">
        <button class="sheet-option" onclick={() => sheetAction(target => openItem(target.id))}><Icon name="arrow" size={18} /><span>Open ticket</span></button>
        {#if user}{@const mine = item.assignees.includes(user.id)}<button class="sheet-option" onclick={() => sheetAction(target => { const assigned = target.assignees.includes(user!.id); return updateItem(target, { [assigned ? 'remove_assignees' : 'add_assignees']: [user!.id] }, assigned ? 'Unassigned from you' : 'Assigned to you'); })}><Icon name={mine ? 'unassigned' : 'add-person'} size={18} /><span>{mine ? 'Unassign me' : 'Assign to me'}</span></button>{/if}
        <button class="sheet-option danger-text" onclick={() => sheetAction(requestDelete)}><Icon name="trash" size={18} /><span>Delete ticket…</span></button>
      </div>
      <button class="sheet-cancel" onclick={closeMoveSheet}>Cancel</button>
    </div>
  </dialog>
{/if}
{#if deleteTarget}
  <dialog class="dlg delete-dialog" bind:this={deleteDialog} aria-labelledby="delete-title" aria-describedby="delete-description" oncancel={event => { event.preventDefault(); void cancelDelete(); }}>
    <header class="dlg-head"><h2 id="delete-title">Delete TK-{deleteTarget.id}?</h2></header>
    <div class="dlg-body">
      <div class="dlg-callout danger"><strong class="delete-ticket-title">{deleteTarget.title}</strong><p id="delete-description">Deletes the ticket and its activity, discarding unsaved edits. Can't be undone.</p></div>
      {#if deleteError}<p class="error-banner" role="alert">{deleteError}</p>{/if}
    </div>
    <footer class="dlg-foot"><div class="actions"><button class="small-button" disabled={deleting} onclick={cancelDelete}>Cancel</button><button class="primary-button danger-button" disabled={deleting || readonly || authExpired || deleteConflict} onclick={deleteItem}>{deleting ? 'Please wait…' : 'Delete ticket'}</button></div></footer>
  </dialog>
{/if}
{#if installing && user && !authExpired}<SetupDialog email={user.email} onclose={() => installing = false} />{/if}
{#if account && user && !authExpired}<AccountDialog {user} initialView={account} onchange={userChanged} onclose={closeAccount} onlogout={async () => { await closeAccount(); await logout(); }} beforePasswordChange={guardDraft} onpasswordchanged={passwordChanged} />{/if}
{#if managingTags && user && !authExpired}<TagsDialog {readonly} beforeDelete={guardDraft} ondeleted={tagDeleted} onclose={closeTags} />{/if}
{#if inviting && user?.role === 'admin' && !authExpired}<PeopleDialog {users} currentUserId={user.id} onchange={userChanged} onclose={() => inviting = false} />{/if}
{#if palette}<CommandMenu actions={menuActions} title={menuTitle} onclose={closePalette} />{/if}
{#if help}<dialog class="dlg wide help-dialog" bind:this={helpDialog} oncancel={event => { event.preventDefault(); void closeHelp(); }} aria-label="Keyboard shortcuts"><header class="dlg-head"><h2>Keyboard shortcuts</h2><button class="icon-button" aria-label="Close shortcuts" onclick={closeHelp}><Icon name="close" size={16} /></button></header>
  {#each [
    { title: 'Move around', keys: [['↑ ↓ / j k', 'Previous / next ticket'], ['← → / h l', 'Previous / next column'], ['Home / gg · End / G', 'First · last loaded ticket'], ['1–7', 'Jump to a column'], ['Enter', 'Open ticket'], ['[ ] / j k', 'Previous / next in details'], ['F6', 'Switch board / details or search']] },
    { title: 'Work with tickets', keys: [['C', 'Create in current column'], ['Delete', 'Delete ticket (with confirmation)'], ['E / I · D', 'Edit title · description'], ['A · M', 'Assignees · assign / unassign me'], ['S · T · Y', 'Status · tags · type'], ['Alt ↑ ↓ / Shift K J', 'Reorder ticket'], ['Alt ← → / Shift H L', 'Move to adjacent status'], ['Ctrl / ⌘ Enter', 'Save now / create and edit'], ['Ctrl / ⌘ Shift Enter', 'Save and close details']] },
    { title: 'Find and control', keys: [['/', 'Search loaded tickets'], ['Enter / ↓ in search', 'Focus first result'], ['Ctrl / ⌘ K', 'Commands'], ['↑ ↓ / Ctrl J K', 'Navigate a command menu'], ['R', 'Refresh and apply order'], ['Escape', 'Leave field, close or cancel'], ['?', 'This guide']] },
  ] as group}<h3>{group.title}</h3><dl>{#each group.keys as [keys, action]}<div><dt>{action}</dt><dd><kbd>{keys}</kbd></dd></div>{/each}</dl>{/each}
  <footer class="dlg-foot"><span class="note">Letter keys pause while typing · edits save automatically</span></footer></dialog>{/if}
