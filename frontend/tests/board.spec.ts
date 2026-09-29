import { test, expect, type BrowserContext, type Page } from '@playwright/test';
import type { Item, Status, Updates } from '../src/api';
import { itemFields, itemPatch, rebaseFields } from '../src/item-edit';
import { applyItemChanges, matchesQuery, reconcileItems } from '../src/items';

const user = { id: '1', name: 'Alex Morgan', role: 'member' };
const statuses: Status[] = [
  'backlog',
  'todo',
  'in_progress',
  'code_review',
  'blocked',
  'complete',
  'void',
];
const titles = [
  'Add cursor pagination to activity API',
  'Surface stale-edit conflicts in the CLI',
  'Preserve focus across live updates',
  'Index tag and assignee lookups',
  'Handle cancelled database transactions',
  'Verify restore from a WAL backup',
  'Remove legacy token flags',
  'Support an empty description',
  'Bound the SQLite read pool',
  'Keyboard navigation between columns',
  'Document server configuration',
  'Test concurrent priority moves',
];
function makeItem(id: number, status: Status, title = titles[(id - 1) % titles.length]): Item {
  return {
    id: String(id),
    title,
    description: 'Keep the API response bounded and preserve the existing contract.',
    type: id % 3 === 0 ? 'bug' : id % 3 === 1 ? 'feature' : 'task',
    status,
    priority: id * 1024,
    version: 1,
    tags: [id % 2 ? 'api' : 'frontend'],
    assignees: id % 3 ? ['1'] : ['2'],
    created_by: '1',
    created_at: '2026-09-24T12:00:00Z',
    updated_at: '2026-09-24T12:00:00Z',
  };
}

async function mock(
  context: BrowserContext,
  rows = Array.from({ length: 12 }, (_, i) => makeItem(i + 1, statuses[i % statuses.length])),
  streamUnavailable = false,
) {
  // Keep a real ReadableStream open, with fragmented SSE frames, while the
  // ordinary API remains mocked. This exercises the production stream parser.
  await context.addInitScript(
    ({ streamUnavailable: offline }) => {
      const fetch = window.fetch.bind(window);
      window.fetch = async (input, init) => {
        if (input !== '/api/v1/events') return fetch(input, init);
        if (offline) return new Response('', { status: 503 });
        let remove = () => {};
        const stream = new ReadableStream<Uint8Array>({
          start(controller) {
            const encoder = new TextEncoder();
            const send = (kind: string, updates: unknown) => {
              if (kind === 'disconnect') {
                remove();
                controller.close();
                return;
              }
              controller.enqueue(encoder.encode(`event: ${kind.slice(0, 2)}`));
              controller.enqueue(
                encoder.encode(`${kind.slice(2)}\ndata: ${JSON.stringify(updates)}\n\n`),
              );
            };
            const change = (event: Event) => {
              const { kind, updates, count } = (event as CustomEvent).detail;
              for (let i = 0; i < count; i++) send(kind, updates);
            };
            const abort = () => {
              remove();
              controller.error(new DOMException('Aborted', 'AbortError'));
            };
            remove = () => {
              window.removeEventListener('test:remote-change', change);
              init?.signal?.removeEventListener('abort', abort);
            };
            window.addEventListener('test:remote-change', change);
            init?.signal?.addEventListener('abort', abort);
            send('ready', { reset: true, users: true });
          },
          cancel() {
            remove();
          },
        });
        return new Response(stream, { headers: { 'Content-Type': 'text/event-stream' } });
      };
    },
    { streamUnavailable },
  );
  const state = {
    items: rows,
    tags: ['api', 'frontend'],
    failWrites: false,
    writes: 0,
    viewer: false,
    listCalls: 0,
    boardCalls: 0,
    beforeWrite: null as (() => Promise<void>) | null,
    beforeReply: null as ((item: Item) => Promise<unknown>) | null,
    bodies: [] as Record<string, unknown>[],
    methods: [] as string[],
    notify: (kind = 'change', updates: Updates = { items: rows }, count = 1): Promise<unknown[]> =>
      Promise.all(
        context
          .pages()
          .map((page) =>
            page.evaluate(
              (detail) => window.dispatchEvent(new CustomEvent('test:remote-change', { detail })),
              { kind, updates, count },
            ),
          ),
      ),
  };
  await context.route('**/api/v1/**', async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const path = url.pathname.replace('/api/v1', '');
    const body = request.postDataJSON();
    const reply = (data: unknown, status = 200) =>
      route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) });
    if (path === '/auth/login')
      return reply({
        user: { ...user, role: state.viewer ? 'viewer' : 'member' },
        session_token: 'test-only-session',
        expires_at: 9999999999,
      });
    if (path === '/auth/me') return reply({ ...user, role: state.viewer ? 'viewer' : 'member' });
    if (path === '/auth/logout') return reply({ logged_out: true });
    if (path === '/users')
      return reply({
        users: [
          { ...user, role: state.viewer ? 'viewer' : 'member' },
          { id: '2', name: 'Build Agent', role: 'member' },
        ],
      });
    const tagPage = (after = '', limit = 200) => {
      const matching = [...state.tags].sort().filter((tag) => tag > after);
      const tags = matching.slice(0, limit);
      return {
        tags,
        usage: Object.fromEntries(
          tags.map((tag) => [tag, state.items.filter((item) => item.tags.includes(tag)).length]),
        ),
        ...(matching.length > limit ? { next_after: tags.at(-1) } : {}),
      };
    };
    if (path === '/tags' && request.method() === 'GET') {
      const result = tagPage(
        url.searchParams.get('after') || '',
        Number(url.searchParams.get('limit') || 200),
      );
      const { usage, ...page } = result;
      return reply(url.searchParams.get('usage') === 'true' ? result : page);
    }
    if (path.endsWith('/activity')) return reply({ activity: [] });
    const list = (status: string | null, limit: number, cursor = '') => {
      const matching = state.items
        .filter(
          (i) =>
            matchesQuery(i, url.searchParams.get('query') || '') &&
            (!status || i.status === status) &&
            (!url.searchParams.get('tag') || i.tags.includes(url.searchParams.get('tag')!)) &&
            (!url.searchParams.get('assignee') ||
              (url.searchParams.get('assignee') === 'none'
                ? !i.assignees.length
                : i.assignees.includes(url.searchParams.get('assignee')!))),
        )
        .sort((a, b) => a.priority - b.priority);
      const start = Number(cursor);
      const end = start + limit;
      return {
        items: matching
          .slice(start, end)
          .map(({ description, ...rest }) =>
            description ? { ...rest, preview: description } : rest,
          ),
        ...(end < matching.length ? { next_cursor: String(end) } : {}),
      };
    };
    if (path === '/board') {
      state.boardCalls++;
      const selected = url.searchParams.get('status');
      const columns = Object.fromEntries(
        statuses
          .filter((status) => !selected || status === selected)
          .map((status) => [
            status,
            list(status, !selected && ['backlog', 'complete', 'void'].includes(status) ? 20 : 100),
          ]),
      );
      return reply({
        columns,
        users: {
          users: [
            { ...user, role: state.viewer ? 'viewer' : 'member' },
            { id: '2', name: 'Build Agent', role: 'member' },
          ],
        },
        tags: tagPage(),
      });
    }
    if (path === '/items' && request.method() === 'GET') {
      state.listCalls++;
      return reply(
        list(
          url.searchParams.get('status'),
          Number(url.searchParams.get('limit')),
          url.searchParams.get('cursor') || '',
        ),
      );
    }
    if (request.method() !== 'GET') {
      state.writes++;
      state.bodies.push(body);
      state.methods.push(request.method());
      await state.beforeWrite?.();
      if (state.failWrites)
        return reply({ error: { code: 'internal', message: 'Save failed' } }, 500);
    }
    if (path === '/tags' && request.method() === 'DELETE') {
      if (!state.tags.includes(body.name))
        return reply({ error: { code: 'not_found', message: 'Tag not found' } }, 404);
      let count = 0;
      for (const item of state.items)
        if (item.tags.includes(body.name)) {
          item.tags = item.tags.filter((tag) => tag !== body.name);
          item.version++;
          count++;
        }
      state.tags = state.tags.filter((tag) => tag !== body.name);
      return reply({ deleted: true, removed_from: count });
    }
    if (path === '/items' && request.method() === 'POST') {
      const created = {
        ...makeItem(Math.max(...state.items.map((i) => Number(i.id)), 0) + 1, body.status),
        ...body,
      };
      state.items.push(created);
      return reply(created);
    }
    const id = path.split('/')[2];
    const current = state.items.find((i) => i.id === id);
    if (!current) return reply({ error: { code: 'not_found', message: 'Item not found' } }, 404);
    if (request.method() === 'GET')
      return reply({ ...current, preview: current.description || undefined });
    if (current.version !== body.version)
      return reply(
        { error: { code: 'conflict', message: 'Item changed', current_version: current.version } },
        409,
      );
    if (request.method() === 'DELETE') {
      state.items.splice(state.items.indexOf(current), 1);
      return reply({ deleted: true });
    }
    if (path.endsWith('/move')) {
      const anchor = state.items.find((i) => i.id === (body.before || body.after))!;
      current.priority = anchor.priority + (body.before ? -1 : 1);
      if (body.status) current.status = body.status;
    } else {
      for (const key of ['title', 'description', 'status', 'type'] as const)
        if (key in body) Object.assign(current, { [key]: body[key] });
      for (const [field, add, remove] of [
        ['tags', 'add_tags', 'remove_tags'],
        ['assignees', 'add_assignees', 'remove_assignees'],
      ] as const)
        current[field] = [
          ...new Set([
            ...current[field].filter((v) => !(body[remove] || []).includes(v)),
            ...(body[add] || []),
          ]),
        ] as string[];
    }
    current.version++;
    await state.beforeReply?.(current);
    return reply({ ...current, preview: current.description || undefined });
  });
  return state;
}

async function signIn(page: Page) {
  await page.goto('/');
  await page.getByLabel('Email', { exact: true }).fill('test@example.invalid');
  await page.getByLabel('Password', { exact: true }).fill('test-password');
  await page.getByLabel('Password', { exact: true }).press('Enter');
  await expect(page.getByRole('region', { name: 'Todo column', exact: true })).toBeVisible();
  await expect(page.locator('.connection')).toHaveText('Live');
  await expect(page.locator('.connection')).toHaveAttribute('title', /Last sync/);
}

test('refreshes reuse unchanged tickets and order detection follows the current rows', async ({
  page,
  context,
}) => {
  await mock(context, [makeItem(1, 'todo'), makeItem(2, 'todo')]);
  await page.goto('/');
  const result = await page.evaluate(async () => {
    const { BoardState } = await import('../src/board-state.svelte.ts');
    const data = new BoardState('1', () => {});
    data.start();
    try {
      await data.refresh();
      const original = data.items;
      await data.refresh();
      const sameRows = data.items === original;
      await data.update('1', 1, { title: 'A new revision' });
      const reusedSibling = data.itemsById.get('2') === original[1];
      const changedTicket = data.itemsById.get('1') !== original[0];
      data.items = [data.items[1], data.items[0]];
      const changedOrder = data.orderChanged;
      await data.remove(data.itemsById.get('1')!);
      return {
        sameRows,
        reusedSibling,
        changedTicket,
        changedOrder,
        remaining: data.items.map((item) => item.id),
        orderedAfterDelete: !data.orderChanged,
      };
    } finally {
      data.stop();
    }
  });
  expect(result).toEqual({
    sameRows: true,
    reusedSibling: true,
    changedTicket: true,
    changedOrder: true,
    remaining: ['2'],
    orderedAfterDelete: true,
  });
});

test('reconciliation preserves versions and positions while paged moves identify only affected columns', () => {
  const filters = { status: '' as const, tag: '', assignee: '' };
  const first = makeItem(1, 'todo');
  const second = makeItem(2, 'todo');
  const current = [first, second];
  expect(reconcileItems(current, [{ ...first }], filters)).toBe(current);
  const changed = { ...second, priority: 0, version: 2 };
  const live = applyItemChanges(current, [changed], {}, filters);
  expect(live.items.map((item) => item.id)).toEqual(['1', '2']);
  expect(live.items[0]).toBe(first);
  expect(live.columns.size).toBe(0);
  expect(reconcileItems(live.items, [second], filters)).toBe(live.items);
  expect(reconcileItems(live.items, [], filters, [], true).map((item) => item.id)).toEqual([
    '2',
    '1',
  ]);
  const moved = { ...first, status: 'complete' as const, version: 2 };
  const page = applyItemChanges(current, [moved], { todo: 'cursor' }, filters);
  expect([...page.columns]).toEqual(['todo']);
  expect(page.items.find((item) => item.id === '1')?.status).toBe('complete');
  expect(
    reconcileItems(current, [first], { ...filters, status: 'todo', tag: 'api' }, ['todo']),
  ).toEqual([first]);
});

test('description matches survive summary reconciliation and are rechecked on live edits', () => {
  const filters = { status: '' as const, tag: '', assignee: '', query: 'hidden needle' };
  const full = { ...makeItem(1, 'todo', 'Unrelated title'), description: 'Hidden search needle' };
  const { description, ...summary } = full;
  let items = reconcileItems([], [summary], filters);
  expect(items).toEqual([summary]);
  expect(reconcileItems(items, [], filters)).toBe(items);
  expect(matchesQuery(full, filters.query)).toBe(true);
  const edited = { ...full, title: 'Changed title', version: 2 };
  items = applyItemChanges(items, [edited], {}, filters).items;
  expect(items).toHaveLength(1);
  expect(items[0].description).toBeUndefined();
  expect(applyItemChanges(items, [{ ...summary, version: 3 }], {}, filters).items).toEqual([]);
  expect(
    applyItemChanges(items, [{ ...edited, description: 'Other text', version: 3 }], {}, filters)
      .items,
  ).toEqual([]);
});

test('changing filters cancels a slow snapshot without replacing the newer view', async ({
  page,
  context,
}) => {
  await mock(context);
  await signIn(page);
  let release!: () => void;
  const held = new Promise<void>((resolve) => (release = resolve));
  let requested!: () => void;
  const started = new Promise<void>((resolve) => (requested = resolve));
  await page.route('**/api/v1/board?*', async (route) => {
    if (new URL(route.request().url()).searchParams.get('status') === 'todo') {
      requested();
      await held;
    }
    await route.fallback();
  });
  const filter = page.getByRole('combobox', { name: 'Filter status', exact: true });
  await filter.click();
  await page.getByRole('option', { name: 'Todo', exact: true }).click();
  await started;
  await filter.click();
  await page.getByRole('option', { name: 'In progress', exact: true }).click();
  await expect(page.locator('#ticket-3')).toBeVisible();
  release();
  await expect(page.locator('.kanban-column')).toHaveCount(1);
  await expect(page.locator('[data-column="in_progress"] .card')).toHaveCount(2);
  await expect(page.locator('#ticket-2')).toHaveCount(0);
});

test('expired pagination resets the loaded pages and retries through the sync queue', async ({
  page,
  context,
}) => {
  await mock(
    context,
    Array.from({ length: 130 }, (_, i) => makeItem(i + 1, 'backlog')),
  );
  await signIn(page);
  await page.route('**/api/v1/items?*', async (route) => {
    if (new URL(route.request().url()).searchParams.has('cursor')) {
      await route.fulfill({
        status: 409,
        contentType: 'application/json',
        body: JSON.stringify({ error: { code: 'cursor_expired', message: 'Cursor expired' } }),
      });
    } else await route.fallback();
  });
  await page.getByRole('button', { name: 'Load more', exact: true }).click();
  await expect(page.getByText('Order changed; loaded pages reset.', { exact: true })).toBeVisible();
  await expect(page.locator('.connection')).toHaveText('Live');
  await expect(page.locator('[data-column="backlog"] .card')).toHaveCount(20);
  await expect(page.getByRole('button', { name: 'Retry loading more', exact: true })).toBeEnabled();
});

test('switching tickets cancels the previous detail request', async ({ page, context }) => {
  await mock(context);
  await page.goto('/');
  const result = await page.evaluate(async () => {
    const { TicketState } = await import('../src/ticket.svelte.ts');
    const ticket = new TicketState('1');
    const first = ticket.open('1');
    const second = ticket.open('2');
    await Promise.all([first, second]);
    const loaded = {
      id: ticket.id,
      item: ticket.item?.id,
      loading: ticket.loading,
      error: ticket.error,
    };
    const pending = ticket.open('3');
    ticket.stop();
    await pending;
    return { loaded, cancelled: ticket.item === null && !ticket.loading && !ticket.error };
  });
  expect(result).toEqual({
    loaded: { id: '2', item: '2', loading: false, error: '' },
    cancelled: true,
  });
});

test('restarting the stream during a write resumes pending synchronization after its acknowledgement', async ({
  page,
  context,
}) => {
  await mock(context);
  await page.goto('/');
  const title = await page.evaluate(async () => {
    const { BoardState } = await import('../src/board-state.svelte.ts');
    let refreshed!: () => void;
    const restarted = new Promise<void>((resolve) => (refreshed = resolve));
    let firstLoad = true;
    const data = new BoardState('1', () => {
      if (!firstLoad) refreshed();
    });
    data.start();
    await data.refresh();
    const fetch = window.fetch;
    let release!: () => void;
    const held = new Promise<void>((resolve) => (release = resolve));
    window.fetch = async (input, init) => {
      if (init?.method === 'PATCH') await held;
      return fetch(input, init);
    };
    try {
      const saving = data.update('2', 1, { title: 'Saved across a reconnect' });
      data.start();
      firstLoad = false;
      // Let the ready-event debounce run while the old write is still pending.
      await new Promise((resolve) => setTimeout(resolve, 200));
      release();
      await saving;
      await restarted;
      return data.itemsById.get('2')?.title;
    } finally {
      release();
      window.fetch = fetch;
      data.stop();
    }
  });
  expect(title).toBe('Saved across a reconnect');
});

test('unsaved editor state immediately protects unload and disables board dragging', async ({
  page,
  context,
}) => {
  await mock(context);
  await signIn(page);
  await page.locator('#ticket-2').click();
  await page.getByLabel('Ticket title').fill('');
  await expect(page.locator('#ticket-9')).toHaveAttribute('draggable', 'false');
  expect(
    await page.evaluate(() => {
      const event = new Event('beforeunload', { cancelable: true });
      window.dispatchEvent(event);
      return event.defaultPrevented;
    }),
  ).toBe(true);
  await page.getByLabel('Ticket title').fill('Saved title');
  await expect(page.locator('.detail')).toHaveAttribute('data-save-state', 'saved');
  await expect(page.locator('#ticket-9')).toHaveAttribute('draggable', 'true');
  expect(
    await page.evaluate(() => {
      const event = new Event('beforeunload', { cancelable: true });
      window.dispatchEvent(event);
      return event.defaultPrevented;
    }),
  ).toBe(false);
});

test('select popovers toggle, dismiss outside, and hand off to another select', async ({
  page,
  context,
}) => {
  await mock(context);
  await signIn(page);
  const status = page.getByRole('combobox', { name: 'Filter status', exact: true });
  const tag = page.getByRole('combobox', { name: 'Filter tag', exact: true });
  await status.click();
  await expect(status).toHaveAttribute('aria-expanded', 'true');
  await status.click();
  await expect(status).toHaveAttribute('aria-expanded', 'false');
  await status.click();
  await tag.click();
  await expect(status).toHaveAttribute('aria-expanded', 'false');
  await expect(tag).toHaveAttribute('aria-expanded', 'true');
  await page.locator('.wordmark').click();
  await expect(tag).toHaveAttribute('aria-expanded', 'false');
  await status.focus();
  await page.keyboard.press('ArrowDown');
  await page.keyboard.press('Escape');
  await expect(status).toBeFocused();
  await expect(status).toHaveAttribute('aria-expanded', 'false');
});

test('live role changes update permissions without losing an open draft', async ({
  page,
  context,
}) => {
  const state = await mock(context);
  await signIn(page);
  await page.locator('#ticket-2').click();
  await page.getByLabel('Comment', { exact: true }).fill('Keep my comment draft');
  await page.getByLabel('Ticket title', { exact: true }).fill('Keep my draft');
  state.viewer = true;
  await state.notify('change', { users: true });
  await expect(page.getByLabel('Ticket title', { exact: true })).toBeDisabled();
  await expect(page.getByLabel('Ticket title', { exact: true })).toHaveValue('Keep my draft');
  await expect(page.getByLabel('Comment', { exact: true })).toHaveValue('Keep my comment draft');
  await expect(page.getByLabel('Comment', { exact: true })).toHaveJSProperty('readOnly', true);
  await expect(page.getByRole('button', { name: 'Send', exact: true })).toBeDisabled();
  await expect(page.getByRole('button', { name: 'Sign in', exact: true })).toHaveCount(0);
  state.viewer = false;
  await state.notify('change', { users: true });
  await expect(page.getByLabel('Ticket title', { exact: true })).toBeEnabled();
  await expect(page.getByLabel('Comment', { exact: true })).toHaveJSProperty('readOnly', false);
  await expect(page.getByLabel('Ticket title', { exact: true })).toHaveValue('Keep my draft');
  await expect(page.locator('.detail')).toHaveAttribute('data-save-state', 'saved');
  expect(state.items.find((item) => item.id === '2')?.title).toBe('Keep my draft');
  await page.keyboard.press('Escape');
  await page.keyboard.press('Escape');
  await page.keyboard.press('n');
  await page.getByLabel('New item title').fill('Keep my new ticket draft');
  state.viewer = true;
  await state.notify('change', { users: true });
  await expect(page.getByLabel('New item title')).toBeDisabled();
  await expect(page.getByLabel('New item title')).toHaveValue('Keep my new ticket draft');
});

test('fullscreen board supports keyboard capture, navigation, moves, and editing', async ({
  page,
  context,
}) => {
  const state = await mock(context);
  const errors: string[] = [];
  page.on('pageerror', (error) => errors.push(error.message));
  await signIn(page);
  await expect(page.locator('.kanban-column')).toHaveCount(7);
  await expect(page.locator('nav, .sidebar, .list-heading')).toHaveCount(0);
  await page.locator('#column-todo').focus();
  await page.keyboard.press('n');
  await page.getByLabel('New item title').fill('Keyboard-only capture');
  await page.keyboard.press('Enter');
  await expect(page.getByRole('button', { name: /TK-13: Keyboard-only capture/ })).toBeVisible();
  await page.keyboard.press('Escape');
  await page.locator('#ticket-13').focus();
  await page.keyboard.press('Alt+ArrowRight');
  await expect(
    page
      .getByRole('region', { name: 'In progress column', exact: true })
      .getByRole('button', { name: /TK-13:/ }),
  ).toBeVisible();
  await expect.poll(() => state.items.find((i) => i.id === '13')?.status).toBe('in_progress');
  await page.keyboard.press('Enter');
  await page.getByLabel('Ticket title').fill('Edited by keyboard');
  await page.keyboard.press('Control+Enter');
  await expect(page.locator('.detail')).toHaveAttribute('data-save-state', 'saved');
  await page.keyboard.press('Escape');
  await expect(page.getByRole('complementary', { name: 'Item 13', exact: true })).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(page.locator('#ticket-13')).toBeFocused();
  await expect(page.locator('#ticket-13')).toContainText('Edited by keyboard');
  expect(errors).toEqual([]);
});

test('live changes move remote cards, preserves drafts, and reconciles version conflicts', async ({
  page,
  context,
}) => {
  const state = await mock(context);
  await signIn(page);
  await page.locator('#ticket-2').click();
  await page.getByLabel('Description', { exact: true }).fill('My unsaved draft');
  const remote = state.items.find((i) => i.id === '2')!;
  remote.title = 'Changed by another user';
  remote.status = 'code_review';
  remote.description = 'Remote description';
  remote.version++;
  await state.notify();
  await expect(
    page
      .getByRole('region', { name: 'Code review column', exact: true })
      .getByRole('button', { name: /Changed by another user/ }),
  ).toBeVisible({ timeout: 7000 });
  await expect(page.getByLabel('Description', { exact: true })).toHaveValue('My unsaved draft');
  await expect(page.getByText('Updated by someone else')).toBeVisible();
  await page.getByRole('button', { name: 'Keep my edits on latest' }).click();
  await expect(page.getByLabel('Ticket title')).toHaveValue('Changed by another user');
  await expect.poll(() => remote.description).toBe('My unsaved draft');
  expect(remote.title).toBe('Changed by another user');
  expect(remote.status).toBe('code_review');
});

test('one board request loads every status with bounded history and explicit pagination', async ({
  page,
  context,
}) => {
  const rows = Array.from({ length: 55 }, (_, i) => makeItem(i + 1, 'backlog', `Backlog ${i + 1}`));
  rows.push(makeItem(100, 'complete', 'A completed item beyond the first global page'));
  const state = await mock(context, rows);
  await signIn(page);
  expect(state.boardCalls).toBe(1);
  expect(state.listCalls).toBe(0);
  await expect(
    page.getByRole('region', { name: 'Backlog column', exact: true }).locator('.card'),
  ).toHaveCount(20);
  await expect(page.locator('#ticket-100')).toBeVisible();
  await page.getByRole('button', { name: 'Load more', exact: true }).click();
  await expect(
    page.getByRole('region', { name: 'Backlog column', exact: true }).locator('.card'),
  ).toHaveCount(55);
});

for (const view of ['board', 'list']) {
  test(`${view}: scrolling appends only the next page and preserves existing tickets`, async ({
    page,
    context,
  }) => {
    await page.setViewportSize({ width: 1512, height: 600 });
    const state = await mock(
      context,
      Array.from({ length: 245 }, (_, i) => makeItem(i + 1, 'backlog', `Backlog ${i + 1}`)),
    );
    await signIn(page);
    await page.locator('#ticket-1').focus();
    if (view === 'list') await page.keyboard.press('v');
    const column = page.locator('[data-column="backlog"]');
    const tickets = column.locator('[data-ticket]');
    await expect(tickets).toHaveCount(20);
    const first = await page.locator('#ticket-1').elementHandle();
    const reads: string[] = [];
    page.on('request', (request) => {
      if (request.method() === 'GET' && request.url().includes('/api/v1/items?'))
        reads.push(request.url());
    });
    await column.locator('.load-more').scrollIntoViewIfNeeded();
    await expect(tickets).toHaveCount(120);
    expect(reads).toHaveLength(1);
    expect(new URL(reads[0]).searchParams.get('cursor')).toBe('20');
    expect(await first!.evaluate((node) => node === document.getElementById('ticket-1'))).toBe(
      true,
    );
    await column.locator('.load-more').scrollIntoViewIfNeeded();
    await expect(tickets).toHaveCount(220);
    expect(reads).toHaveLength(2);
    expect(new URL(reads[1]).searchParams.get('cursor')).toBe('120');
    await column.locator('.load-more').scrollIntoViewIfNeeded();
    await expect(tickets).toHaveCount(245);
    await expect(column.locator('.load-more')).toHaveCount(0);
    expect(state.boardCalls).toBe(1);
  });

  test(`${view}: keyboard waits for the next page without skipping tickets`, async ({
    page,
    context,
  }) => {
    // Exercise keyboard paging independently of scroll prefetching.
    await context.addInitScript(() => {
      window.IntersectionObserver = class {
        observe() {}
        unobserve() {}
        disconnect() {}
      } as unknown as typeof IntersectionObserver;
    });
    await mock(
      context,
      Array.from({ length: 130 }, (_, i) => makeItem(i + 1, 'backlog')),
    );
    await signIn(page);
    if (view === 'list') await page.keyboard.press('v');
    let release!: () => void;
    const held = new Promise<void>((resolve) => (release = resolve));
    let reads = 0;
    await page.route('**/api/v1/items?*', async (route) => {
      reads++;
      await held;
      await route.fallback();
    });
    await page.locator('#ticket-20').focus();
    await page.keyboard.press('j');
    await expect(page.locator('[data-column="backlog"] .load-more')).toHaveText('Loading…');
    await page.keyboard.press('ArrowDown');
    await expect(page.locator('#ticket-20')).toBeFocused();
    release();
    await expect(page.locator('#ticket-21')).toBeFocused();
    expect(reads).toBe(1);
    await page.locator('#ticket-120').focus();
    await page.keyboard.press('ArrowDown');
    await expect(page.locator('#ticket-121')).toBeFocused();
    expect(reads).toBe(2);
  });
}

test('new tickets stay visible beyond a full page through live refreshes and remote deletion', async ({
  page,
  context,
}) => {
  const state = await mock(
    context,
    Array.from({ length: 130 }, (_, i) => makeItem(i + 1, 'backlog')),
  );
  await signIn(page);
  await page.locator('#ticket-1').focus();
  await page.keyboard.press('n');
  await page.getByLabel('New item title').fill('New ticket stays visible');
  await page.getByLabel('New item title').press('Enter');
  const created = state.items.find((item) => item.title === 'New ticket stays visible')!;
  await expect(page.locator(`#ticket-${created.id}`)).toBeInViewport();
  expect(created.priority).toBeGreaterThan(state.items[129].priority);
  expect(state.listCalls).toBe(0);
  await state.notify();
  await expect.poll(() => state.boardCalls).toBe(2);
  await expect(page.locator(`#ticket-${created.id}`)).toBeVisible();
  await expect(page.locator('[data-column="backlog"] [data-ticket]')).toHaveCount(21);
  state.items = state.items.filter((item) => item.id !== created.id);
  await state.notify();
  await expect(page.locator(`#ticket-${created.id}`)).toHaveCount(0);
});

test('server search finds unloaded titles, IDs and tags, and ignores an outdated response', async ({
  page,
  context,
}) => {
  const rows = Array.from({ length: 130 }, (_, i) =>
    makeItem(i + 1, 'backlog', `Ordinary ${i + 1}`),
  );
  rows[129].title = 'Buried needle';
  rows[129].tags = ['rare-tag'];
  await mock(context, rows);
  await signIn(page);
  const search = page.getByLabel('Search tickets');
  for (const query of ['needle', 'TK-130', '#130', 'rare-tag']) {
    await search.fill(query);
    await expect(page.locator('[data-ticket]')).toHaveCount(1);
    await expect(page.locator('#ticket-130')).toBeVisible();
  }
  let release!: () => void;
  const held = new Promise<void>((resolve) => (release = resolve));
  let requested = '';
  let finished = false;
  await page.route('**/api/v1/board?*', async (route) => {
    requested = new URL(route.request().url()).searchParams.get('query') || '';
    if (requested === 'ordinary') {
      await held;
      await route.fallback();
      finished = true;
      return;
    }
    await route.fallback();
  });
  await search.fill('ordinary');
  await expect.poll(() => requested).toBe('ordinary');
  await search.fill('needle');
  await expect.poll(() => page.url()).toContain('q=needle');
  // Wait for the new query's request, not merely the still-visible old results.
  await expect.poll(() => requested).toBe('needle');
  release();
  await expect.poll(() => finished).toBe(true);
  await expect(page.locator('#ticket-130')).toBeVisible();
  await search.fill('ordinary');
  await expect(page.locator('[data-ticket]')).toHaveCount(20);
  await page.locator('[data-column="backlog"] .load-more').scrollIntoViewIfNeeded();
  await expect(page.locator('[data-ticket]')).toHaveCount(120);
  await expect(page.locator('#ticket-130')).toHaveCount(0);
});

test('failed page loads keep existing tickets and can be retried', async ({ page, context }) => {
  await mock(
    context,
    Array.from({ length: 55 }, (_, i) => makeItem(i + 1, 'backlog')),
  );
  await signIn(page);
  let reads = 0;
  await page.route('**/api/v1/items?*', async (route) => {
    if (++reads === 1)
      return route.fulfill({
        status: 503,
        contentType: 'application/json',
        body: JSON.stringify({ error: { code: 'unavailable', message: 'Page unavailable' } }),
      });
    await route.fallback();
  });
  await page.locator('.load-more').scrollIntoViewIfNeeded();
  await expect(page.getByRole('button', { name: 'Retry loading more' })).toBeEnabled();
  await expect(page.locator('[data-ticket]')).toHaveCount(20);
  expect(reads).toBe(1);
  await page.getByRole('button', { name: 'Retry loading more' }).click();
  await expect(page.locator('[data-ticket]')).toHaveCount(55);
  expect(reads).toBe(2);
});

test('paging a recently created ticket preserves edits acknowledged during the read', async ({
  page,
  context,
}) => {
  const state = await mock(
    context,
    Array.from({ length: 55 }, (_, i) => makeItem(i + 1, 'backlog')),
  );
  await signIn(page);
  await page.locator('#ticket-1').focus();
  await page.keyboard.press('n');
  await page.getByLabel('New item title').fill('Recent original');
  await page.getByLabel('New item title').press('Enter');
  const created = state.items.at(-1)!;
  await expect(page.locator(`#ticket-${created.id}`)).toBeVisible();
  let release!: () => void;
  const held = new Promise<void>((resolve) => (release = resolve));
  const stale = state.items.slice(20).map((item) => ({ ...item }));
  await page.route('**/api/v1/items?*', async (route) => {
    await held;
    await route.fulfill({ json: { items: stale } });
  });
  await page.locator('.load-more').scrollIntoViewIfNeeded();
  await expect(page.locator('.load-more')).toHaveText('Loading…');
  // A local save aborts the in-flight page, then a fresh snapshot contains the
  // old version. The acknowledgement must remain the authoritative value.
  await page.locator(`#ticket-${created.id}`).click();
  await page.getByLabel('Ticket title', { exact: true }).fill('Recent saved edit');
  await expect(page.locator('.detail')).toHaveAttribute('data-save-state', 'saved');
  await page.getByRole('combobox', { name: 'Add tag', exact: true }).fill('page-tag');
  await page.getByRole('combobox', { name: 'Add tag', exact: true }).press('Enter');
  await expect(page.locator('.detail')).toHaveAttribute('data-save-state', 'saved');
  await page.getByRole('combobox', { name: 'Filter tag', exact: true }).click();
  await expect(page.getByRole('option', { name: 'page-tag', exact: true })).toBeVisible();
  await page.keyboard.press('Escape');
  release();
  await page.getByRole('button', { name: 'Close details', exact: true }).click();
  await page.locator('.load-more').scrollIntoViewIfNeeded();
  await expect(page.locator('[data-column="backlog"] [data-ticket]')).toHaveCount(56);
  await expect(page.locator(`#ticket-${created.id}`)).toContainText('Recent saved edit');
});

test('mobile paging only loads a column when its footer is on screen', async ({
  page,
  context,
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  const state = await mock(context, [
    ...Array.from({ length: 25 }, (_, i) => makeItem(i + 1, 'backlog')),
    ...Array.from({ length: 25 }, (_, i) => makeItem(i + 26, 'complete')),
  ]);
  await signIn(page);
  await expect(page.locator('.status-tabs [data-status="todo"]')).toHaveAttribute(
    'aria-current',
    'true',
  );
  const column = page.locator('[data-column="backlog"]');
  await column.locator('.column-scroll').evaluate((node) => (node.scrollTop = node.scrollHeight));
  await page.evaluate(
    () =>
      new Promise<void>((resolve) =>
        requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
      ),
  );
  expect(state.listCalls).toBe(0);
  await page.locator('.status-tabs [data-status="backlog"]').click();
  await expect(column.locator('[data-ticket]')).toHaveCount(25);
  await expect(page.locator('[data-column="complete"] [data-ticket]')).toHaveCount(20);
  expect(state.listCalls).toBe(1);
});

test('failed saves keep draft and viewer controls cannot mutate', async ({ page, context }) => {
  const state = await mock(context);
  await signIn(page);
  await page.locator('#ticket-1').click();
  await page.getByLabel('Ticket title').fill('Do not lose this draft');
  state.failWrites = true;
  await expect(page.getByText('Save failed', { exact: true })).toBeVisible();
  await expect(page.getByLabel('Ticket title')).toHaveValue('Do not lose this draft');
  expect(state.writes).toBe(1);
  await page.getByRole('button', { name: 'Discard unsaved changes', exact: true }).click();
  state.viewer = true;
  state.failWrites = false;
  await page.reload();
  await expect(page.locator('.connection')).toHaveText('Live');
  await expect(page.getByRole('button', { name: 'New', exact: true })).toHaveCount(0);
  await expect(page.getByLabel('Ticket title')).toBeDisabled();
  await expect(page.getByLabel('Comment', { exact: true })).toHaveCount(0);
});

test('list view keeps every board shortcut, collapses groups, and remembers the layout', async ({
  page,
  context,
}, testInfo) => {
  const state = await mock(context, [
    makeItem(1, 'backlog'),
    makeItem(2, 'backlog'),
    makeItem(3, 'in_progress'),
    makeItem(4, 'in_progress'),
    makeItem(5, 'complete'),
  ]);
  const errors: string[] = [];
  page.on('pageerror', (error) => errors.push(error.message));
  await signIn(page);
  await page.keyboard.press('v');
  await expect(page.locator('.list-view')).toBeVisible();
  await expect(page.locator('.kanban-column')).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'List view' })).toHaveAttribute(
    'aria-pressed',
    'true',
  );
  await expect(page).toHaveURL(/view=list/);
  await page.screenshot({ path: testInfo.outputPath('list-desktop.png') });

  // j/k walk the list top to bottom across groups, through each group header; h/l leave and enter groups.
  await page.keyboard.press('1');
  await expect(page.locator('#ticket-1')).toBeFocused();
  const order = [
    '#ticket-2',
    '#column-todo',
    '#column-in_progress',
    '#ticket-3',
    '#ticket-4',
    '#column-code_review',
  ];
  for (const stop of order) {
    await page.keyboard.press('j');
    await expect(page.locator(stop)).toBeFocused();
  }
  for (const stop of [
    '#ticket-4',
    '#ticket-3',
    '#column-in_progress',
    '#column-todo',
    '#ticket-2',
  ]) {
    await page.keyboard.press('ArrowUp');
    await expect(page.locator(stop)).toBeFocused();
  }
  await page.keyboard.press('l');
  await expect(page.locator('#ticket-2')).toBeFocused();
  await page.keyboard.press('h');
  await expect(page.locator('#column-backlog')).toBeFocused();
  await page.keyboard.press('3');
  await expect(page.locator('#ticket-3')).toBeFocused();
  await page.keyboard.press('h');
  await expect(page.locator('#column-in_progress')).toBeFocused();
  await page.keyboard.press('h');
  await expect(page.locator('#column-in_progress')).toHaveAttribute('aria-expanded', 'false');
  await expect(page.locator('#ticket-3')).toHaveCount(0);
  await page.keyboard.press('j');
  await expect(page.locator('#column-code_review')).toBeFocused();
  await page.keyboard.press('k');
  await page.keyboard.press('l');
  await expect(page.locator('#column-in_progress')).toHaveAttribute('aria-expanded', 'true');
  await page.keyboard.press('l');
  await expect(page.locator('#ticket-3')).toBeFocused();
  await page.keyboard.press('Shift+G');
  await expect(page.locator('#ticket-5')).toBeFocused();
  await page.keyboard.press('g');
  await page.keyboard.press('g');
  await expect(page.locator('#ticket-1')).toBeFocused();
  await page.keyboard.press('3');
  await expect(page.locator('#ticket-3')).toBeFocused();

  // Alt+↑/↓ reorders, and past a group edge moves into the neighbouring status. Sideways moves do nothing.
  const inProgress = page.getByRole('region', { name: 'In progress group', exact: true });
  await page.keyboard.press('Alt+ArrowDown');
  await expect(inProgress.locator('.row').nth(1)).toHaveAttribute('id', 'ticket-3');
  await page.keyboard.press('Alt+ArrowDown');
  await expect.poll(() => state.items.find((i) => i.id === '3')?.status).toBe('code_review');
  await expect(
    page.getByRole('region', { name: 'Code review group', exact: true }).locator('#ticket-3'),
  ).toBeFocused();
  await page.keyboard.press('Shift+K');
  await expect.poll(() => state.items.find((i) => i.id === '3')?.status).toBe('in_progress');
  await expect(inProgress.locator('.row').nth(1)).toHaveAttribute('id', 'ticket-3');
  await expect(inProgress.locator('#ticket-3')).toBeFocused();
  const writes = state.writes;
  await page.keyboard.press('Alt+ArrowRight');
  await page.keyboard.press('Shift+L');
  await page.keyboard.press('Shift+H');
  await expect(inProgress.locator('#ticket-3')).toBeFocused();
  expect(state.writes).toBe(writes);

  // Property menus, create and open work exactly as on the board.
  await page.keyboard.press('s');
  await page.getByRole('option', { name: 'Blocked' }).click();
  await expect.poll(() => state.items.find((i) => i.id === '3')?.status).toBe('blocked');
  await expect(
    page.getByRole('region', { name: 'Blocked group', exact: true }).locator('#ticket-3'),
  ).toBeFocused();
  await page.keyboard.press('n');
  await page.getByLabel('New item title').fill('Captured from the list');
  await page.keyboard.press('Enter');
  await expect(
    page.getByRole('region', { name: 'Blocked group', exact: true }).getByRole('button', {
      name: /TK-6: Captured from the list/,
    }),
  ).toBeVisible();
  await page.keyboard.press('Escape');
  await page.locator('#ticket-6').focus();
  await page.keyboard.press('Enter');
  await expect(page.getByRole('complementary', { name: 'Item 6', exact: true })).toBeVisible();
  await page.keyboard.press('Escape');
  await page.keyboard.press('Escape');
  await expect(page.locator('#ticket-6')).toBeFocused();

  // X collapses the current group; navigation skips it and it stays collapsed after a reload.
  await page.keyboard.press('1');
  await page.keyboard.press('x');
  await expect(page.locator('#column-backlog')).toHaveAttribute('aria-expanded', 'false');
  await expect(page.locator('#column-backlog')).toBeFocused();
  await expect(page.locator('#ticket-1')).toHaveCount(0);
  await page.keyboard.press('j');
  await expect(page.locator('#column-todo')).toBeFocused();
  await page.keyboard.press('k');
  await expect(page.locator('#column-backlog')).toBeFocused();
  await page.keyboard.press('l');
  await expect(page.locator('#ticket-1')).toBeVisible();
  await page.keyboard.press('x');
  await page.goto('/');
  await expect(page.locator('.list-view')).toBeVisible();
  await expect(page.locator('#column-backlog')).toHaveAttribute('aria-expanded', 'false');
  await page.locator('#column-backlog').click();
  await expect(page.locator('#ticket-1')).toBeVisible();

  await page.getByRole('button', { name: 'Board view' }).click();
  await expect(page.locator('.kanban-column')).toHaveCount(7);
  await expect(page).not.toHaveURL(/view=/);
  expect(errors).toEqual([]);
});

test('list rows reorder and change status by drag and drop', async ({ page, context }) => {
  const state = await mock(context, [
    makeItem(1, 'todo'),
    makeItem(2, 'todo'),
    makeItem(3, 'todo'),
    makeItem(4, 'backlog'),
  ]);
  await signIn(page);
  await page.getByRole('button', { name: 'List view' }).click();
  const todo = page.getByRole('region', { name: 'Todo group', exact: true });
  await page
    .locator('#ticket-3')
    .dragTo(page.locator('#ticket-1'), { targetPosition: { x: 20, y: 3 } });
  await expect(todo.locator('.row').first()).toHaveAttribute('id', 'ticket-3');
  await page
    .locator('#ticket-4')
    .dragTo(page.locator('#ticket-1'), { targetPosition: { x: 20, y: 3 } });
  await expect.poll(() => state.items.find((i) => i.id === '4')?.status).toBe('todo');
  await expect(todo.locator('.row').nth(1)).toHaveAttribute('id', 'ticket-4');
});

test('cards and list rows preview descriptions and follow description edits', async ({
  page,
  context,
}, testInfo) => {
  const state = await mock(context, [
    makeItem(1, 'todo'),
    { ...makeItem(2, 'todo'), description: '' },
  ]);
  await signIn(page);
  const card = page.locator('#ticket-1');
  await expect(card.locator('.card-preview')).toHaveText(state.items[0].description!);
  await expect(page.locator('#ticket-2 .card-preview')).toHaveCount(0);
  await page.screenshot({ path: testInfo.outputPath('preview-board.png') });
  await card.click();
  await page.getByLabel('Description', { exact: true }).fill('Rewritten plan for the endpoint');
  await page.keyboard.press('Control+Enter');
  await expect(page.locator('.detail')).toHaveAttribute('data-save-state', 'saved');
  await expect(card.locator('.card-preview')).toHaveText('Rewritten plan for the endpoint');
  await page.keyboard.press('Escape');
  await page.keyboard.press('Escape');
  await page.keyboard.press('v');
  await expect(page.locator('#ticket-1 .row-preview')).toHaveText(
    'Rewritten plan for the endpoint',
  );
  await expect(page.locator('#ticket-2 .row-preview')).toHaveCount(0);
  await page.screenshot({ path: testInfo.outputPath('preview-list.png') });
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.locator('#ticket-1 .row-preview')).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath('preview-list-mobile.png') });
});

test('compact layout and command palette stay keyboard accessible on narrow screens', async ({
  page,
  context,
}, testInfo) => {
  await mock(context);
  await signIn(page);
  await page.screenshot({ path: testInfo.outputPath('kanban-desktop.png') });
  await page.keyboard.press('Control+k');
  await page.getByRole('combobox', { name: 'Find a command' }).fill('create');
  await page.keyboard.press('Enter');
  await expect(page.getByLabel('New item title')).toBeFocused();
  await page.keyboard.press('Escape');
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.getByRole('button', { name: 'New', exact: true })).toBeVisible();
  await expect(page.locator('.board')).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath('kanban-mobile.png') });
});

test('arrows and vim motions keep row position through empty columns and never act on stale selection', async ({
  page,
  context,
}) => {
  const state = await mock(context, [
    makeItem(1, 'backlog'),
    makeItem(2, 'backlog'),
    makeItem(3, 'in_progress'),
    makeItem(4, 'in_progress'),
  ]);
  await signIn(page);
  await page.keyboard.press('1');
  await expect(page.locator('#ticket-1')).toBeFocused();
  await page.keyboard.press('j');
  await expect(page.locator('#ticket-2')).toBeFocused();
  await page.keyboard.press('ArrowRight');
  await expect(page.locator('#column-todo')).toBeFocused();
  await page.keyboard.press('m');
  await page.keyboard.press('Shift+L');
  expect(state.writes).toBe(0);
  await page.keyboard.press('l');
  await expect(page.locator('#ticket-4')).toBeFocused();
  await page.keyboard.press('Home');
  await expect(page.locator('#ticket-3')).toBeFocused();
  await page.keyboard.press('Shift+G');
  await expect(page.locator('#ticket-4')).toBeFocused();
  await page.keyboard.press('g');
  await page.keyboard.press('g');
  await expect(page.locator('#ticket-3')).toBeFocused();
  await page.keyboard.press('/');
  await page.getByLabel('Search tickets').fill(state.items[3].title);
  await page.keyboard.press('ArrowDown');
  await expect(page.locator('#ticket-4')).toBeFocused();
  await page.keyboard.press('/');
  await page.getByLabel('Search tickets').fill('no results');
  await page.keyboard.press('Escape');
  await expect(page.locator('#column-in_progress')).toBeFocused();
  await page.keyboard.press('a');
  await expect(page.getByRole('dialog')).toHaveCount(0);
  expect(state.writes).toBe(0);
  await page.getByRole('button', { name: 'Clear filters', exact: true }).click();
  await page.keyboard.press('Escape');
  await expect(page.locator('.board [tabindex="0"]')).toHaveCount(1);
});

test('quick property menus assign, unassign, tag, change type and status with focus restored', async ({
  page,
  context,
}) => {
  const state = await mock(context);
  await signIn(page);
  await page.locator('#ticket-2').focus();
  const ticket = state.items.find((i) => i.id === '2')!;
  async function choose(key: string, query: string) {
    await page.keyboard.press(key);
    await page.getByRole('combobox', { name: 'Find a command' }).fill(query);
    await page.keyboard.press('Enter');
    await expect(page.getByRole('dialog')).toHaveCount(0);
    await expect(page.locator('#ticket-2')).toBeFocused();
    await expect(page.locator('.board-feedback')).not.toHaveText('Updating…');
  }
  await choose('a', 'Build');
  await expect.poll(() => ticket.assignees).toEqual(['1', '2']);
  await choose('a', 'Remove Build');
  await expect.poll(() => ticket.assignees).toEqual(['1']);
  await page.keyboard.press('m');
  await expect.poll(() => ticket.assignees).toEqual([]);
  await expect(page.locator('.board-feedback')).toContainText('Unassigned from you');
  await page.keyboard.press('m');
  await expect.poll(() => ticket.assignees).toEqual(['1']);
  await expect(page.locator('.board-feedback')).toContainText('Assigned to you');
  await choose('t', 'Add #api');
  await expect.poll(() => ticket.tags).toEqual(['frontend', 'api']);
  await choose('t', 'Remove #frontend');
  await expect.poll(() => ticket.tags).toEqual(['api']);
  await choose('y', 'Bug');
  await expect.poll(() => ticket.type).toBe('bug');
  await choose('s', 'Blocked');
  await expect.poll(() => ticket.status).toBe('blocked');
  await expect(
    page
      .locator('#column-blocked')
      .locator('..')
      .locator('..')
      .getByRole('button', { name: /TK-2:/ }),
  ).toBeVisible();
  state.failWrites = true;
  await choose('a', 'Build');
  await expect(page.getByRole('alert')).toContainText('Save failed');
  expect(ticket.assignees).toEqual(['1']);
});

test('create and fully edit a ticket without losing pending tags or drafts', async ({
  page,
  context,
}) => {
  const state = await mock(context);
  await signIn(page);
  await page.keyboard.press('n');
  await page.getByLabel('New item title').fill('Keyboard workflow');
  await page.keyboard.press('Control+Enter');
  const panel = page.getByRole('complementary', { name: 'Item 13', exact: true });
  await expect(page.getByLabel('Ticket title')).toBeFocused();
  await page.getByLabel('Ticket title').fill('Full keyboard workflow');
  await page.keyboard.press('Escape');
  await expect(panel).toBeFocused();
  await page.keyboard.press('a');
  await expect(page.getByRole('combobox', { name: 'Add assignee', exact: true })).toBeFocused();
  await page.keyboard.press('ArrowDown');
  await page.keyboard.press('a');
  await page.keyboard.press('Enter');
  await expect(page.getByRole('button', { name: 'Remove assignee Alex Morgan' })).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(panel).toBeFocused();
  await page.keyboard.press('m');
  await expect(page.getByRole('button', { name: 'Remove assignee Alex Morgan' })).toHaveCount(0);
  await page.keyboard.press('m');
  await expect(page.getByRole('button', { name: 'Remove assignee Alex Morgan' })).toBeVisible();
  await page.keyboard.press('s');
  await expect(page.getByRole('combobox', { name: 'Ticket status', exact: true })).toBeFocused();
  await page.keyboard.press('ArrowDown');
  await page.keyboard.press('i');
  await page.keyboard.press('Enter');
  await page.keyboard.press('Escape');
  await page.keyboard.press('y');
  await expect(page.getByRole('combobox', { name: 'Ticket type', exact: true })).toBeFocused();
  await page.keyboard.press('ArrowDown');
  await page.keyboard.press('b');
  await page.keyboard.press('Enter');
  await page.keyboard.press('Escape');
  await page.keyboard.press('d');
  await expect(page.getByLabel('Description', { exact: true })).toBeFocused();
  await page.getByLabel('Description', { exact: true }).fill('hjkl / c a s t y should stay text');
  await page.keyboard.press('Escape');
  await page.keyboard.press('t');
  await page.getByLabel('Add tag').fill('new-label');
  await page.keyboard.press('Escape');
  await page.getByRole('button', { name: 'Close details', exact: true }).click();
  await expect(panel).toHaveCount(0);
  await expect(page.locator('#ticket-13')).toBeFocused();
  const created = state.items.find((i) => i.id === '13')!;
  expect(created).toMatchObject({
    title: 'Full keyboard workflow',
    description: 'hjkl / c a s t y should stay text',
    assignees: ['1'],
    tags: ['new-label'],
    type: 'bug',
    status: 'in_progress',
  });
  await page.keyboard.press('Enter');
  await expect(page.getByLabel('Ticket title')).toBeFocused();
  await page.keyboard.press('Escape');
  await page.keyboard.press('k');
  await expect(page.getByRole('complementary', { name: 'Item 10', exact: true })).toBeFocused();
  await page.keyboard.press('F6');
  await expect(page.locator('#ticket-10')).toBeFocused();
  await page.keyboard.press('F6');
  await expect(page.getByRole('complementary', { name: 'Item 10', exact: true })).toBeFocused();
});

test('keyboard and drag reordering work within and across columns, including filtered status', async ({
  page,
  context,
}) => {
  const state = await mock(context, [
    makeItem(1, 'todo'),
    makeItem(2, 'todo'),
    makeItem(3, 'todo'),
    makeItem(4, 'backlog'),
  ]);
  await signIn(page);
  await page.locator('#ticket-2').focus();
  await page.keyboard.press('Shift+K');
  await expect(
    page.locator('#column-todo').locator('..').locator('..').locator('.card').first(),
  ).toHaveAttribute('id', 'ticket-2');
  await expect(page.locator('.board-feedback')).toContainText('moved before');
  await page.keyboard.press('Alt+ArrowDown');
  await expect(
    page.locator('#column-todo').locator('..').locator('..').locator('.card').nth(1),
  ).toHaveAttribute('id', 'ticket-2');
  await expect(page.locator('.board-feedback')).toContainText('moved after');
  await page
    .locator('#ticket-3')
    .dragTo(page.locator('#ticket-1'), { targetPosition: { x: 20, y: 5 } });
  await expect(
    page.getByRole('region', { name: 'Todo column', exact: true }).locator('.card').first(),
  ).toHaveAttribute('id', 'ticket-3');
  await page
    .locator('#ticket-4')
    .dragTo(page.locator('#ticket-1'), { targetPosition: { x: 20, y: 5 } });
  await expect.poll(() => state.items.find((i) => i.id === '4')?.status).toBe('todo');
  await expect(
    page.getByRole('region', { name: 'Todo column', exact: true }).locator('.card').nth(1),
  ).toHaveAttribute('id', 'ticket-4');
  await page.getByLabel('Filter status', { exact: true }).click();
  await page.getByRole('option', { name: 'Todo', exact: true }).click();
  await expect(page.locator('.kanban-column')).toHaveCount(1);
  await page.locator('#ticket-4').focus();
  await page.keyboard.press('Shift+L');
  await expect.poll(() => state.items.find((i) => i.id === '4')?.status).toBe('in_progress');
  await expect(page.locator('#ticket-4')).toHaveCount(0);
  await expect(page.locator('.board :focus')).toHaveCount(1);
});

test('colon opens the command palette like vim, but types normally in fields', async ({
  page,
  context,
}) => {
  await mock(context);
  await signIn(page);
  await page.locator('#ticket-2').focus();
  await page.keyboard.press(':');
  const search = page.getByRole('combobox', { name: 'Find a command' });
  await expect(search).toBeFocused();
  await expect(search).toHaveValue('');
  await page.keyboard.press('Escape');
  await expect(search).toHaveCount(0);
  await expect(page.locator('#ticket-2')).toBeFocused();
  await page.keyboard.press('/');
  await page.keyboard.type('a:b');
  await expect(page.getByLabel('Search tickets')).toHaveValue('a:b');
  await expect(search).toHaveCount(0);
});

test('command menus support vim control keys, escape button, and focus handoff to search', async ({
  page,
  context,
}) => {
  await mock(context);
  await signIn(page);
  await page.locator('#ticket-2').focus();
  await page.keyboard.press('Control+k');
  const search = page.getByRole('combobox', { name: 'Find a command' });
  await search.fill('Go to');
  await page.keyboard.press('Control+j');
  await expect(search).toHaveAttribute('aria-activedescendant', 'command-1');
  await page.keyboard.press('Control+k');
  await expect(search).toHaveAttribute('aria-activedescendant', 'command-0');
  await search.fill('Search tickets');
  await page.keyboard.press('Enter');
  await expect(page.getByLabel('Search tickets')).toBeFocused();
  await page.keyboard.press('Escape');
  await page.keyboard.press('Control+k');
  await search.fill('unmatched command');
  await page.keyboard.press('ArrowDown');
  await page.keyboard.press('Enter');
  await expect(page.getByRole('dialog')).toBeVisible();
  await page.keyboard.press('Tab');
  await page.keyboard.press('Enter');
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect(page.locator('#ticket-2')).toBeFocused();
  await page.getByRole('button', { name: 'Commands', exact: true }).focus();
  await page.keyboard.press('?');
  await expect(page.getByRole('dialog', { name: 'Keyboard shortcuts', exact: true })).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.getByRole('button', { name: 'Commands', exact: true })).toBeFocused();
});

test('live moves keep keyboard selection and viewer shortcuts never write', async ({
  page,
  context,
}) => {
  const state = await mock(context);
  await signIn(page);
  await page.locator('#ticket-2').focus();
  const remote = state.items.find((i) => i.id === '2')!;
  remote.status = 'blocked';
  remote.version++;
  await state.notify();
  await expect(
    page.getByRole('region', { name: 'Blocked column', exact: true }).locator('#ticket-2'),
  ).toBeFocused({ timeout: 7000 });
  await page.keyboard.press('n');
  await expect(
    page.getByRole('region', { name: 'Blocked column', exact: true }).getByLabel('New item title'),
  ).toBeFocused();
  await page.keyboard.press('Escape');
  state.viewer = true;
  await page.reload();
  await expect(page.locator('.connection')).toHaveText('Live');
  await page.locator('#ticket-2').focus();
  for (const key of ['c', 'n', 'a', 's', 't', 'y', 'm', 'Shift+J', 'Alt+ArrowRight'])
    await page.keyboard.press(key);
  expect(state.writes).toBe(0);
  await page.keyboard.press('Enter');
  await expect(page.getByRole('complementary', { name: 'Item 2', exact: true })).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(page.locator('#ticket-2')).toBeFocused();
});

test('tag picker suggests known tags and works from the keyboard and pointer', async ({
  page,
  context,
}) => {
  const state = await mock(context);
  await signIn(page);
  await page.locator('#ticket-2').click();
  const input = page.getByRole('combobox', { name: 'Add tag', exact: true });
  const options = page.getByRole('listbox', { name: 'Tag suggestions' }).getByRole('option');
  await input.focus();
  await expect(options).toHaveText(['api']);
  await input.pressSequentially('a');
  await expect(options).toHaveText(['Create a', 'api']);
  await expect(options.first()).toHaveAttribute('aria-selected', 'true');
  await page.keyboard.press('ArrowDown');
  await page.keyboard.press('Enter');
  await expect(input).toHaveValue('');
  await expect(input).toBeFocused();
  await expect.poll(() => state.items.find((i) => i.id === '2')?.tags).toEqual(['frontend', 'api']);
  await input.pressSequentially('release');
  await page.keyboard.press('Escape');
  await expect(input).toHaveAttribute('aria-expanded', 'false');
  await page.keyboard.press('Enter');
  await expect
    .poll(() => state.items.find((i) => i.id === '2')?.tags)
    .toEqual(['frontend', 'api', 'release']);
  await page.getByRole('button', { name: 'Remove tag api', exact: true }).click();
  await input.click();
  await options.filter({ hasText: 'api' }).click();
  await expect(input).toBeFocused();
  await expect
    .poll(() => state.items.find((i) => i.id === '2')?.tags)
    .toEqual(['frontend', 'release', 'api']);
});

test('saving a pending tag works from its field and failed save-and-close preserves focus', async ({
  page,
  context,
}) => {
  const state = await mock(context);
  await signIn(page);
  await page.locator('#ticket-2').focus();
  await page.keyboard.press('t');
  await page.getByRole('combobox', { name: 'Find a command' }).fill('new tag');
  await page.keyboard.press('Enter');
  await expect(page.getByLabel('Add tag')).toBeFocused();
  await page.getByLabel('Add tag').fill('keyboard');
  state.failWrites = true;
  await page.keyboard.press('Control+Shift+Enter');
  await expect(page.getByText('Save failed', { exact: true })).toBeVisible();
  await expect(page.getByLabel('Add tag')).toBeFocused();
  await expect(
    page.getByRole('button', { name: 'Remove tag keyboard', exact: true }),
  ).toBeVisible();
  state.failWrites = false;
  await page.getByRole('button', { name: 'Retry save', exact: true }).click();
  await expect(page.locator('.detail')).toHaveAttribute('data-save-state', 'saved');
  expect(state.items.find((i) => i.id === '2')?.tags).toEqual(['frontend', 'keyboard']);
  await page.getByRole('button', { name: 'Close details', exact: true }).click();
  await expect(page.locator('.detail')).toHaveCount(0);
  await expect(page.locator('#ticket-2')).toBeFocused();
});

test('mobile details keep Tab in the panel and return to the selected ticket', async ({
  page,
  context,
}) => {
  await mock(context);
  await signIn(page);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.locator('#ticket-2').click();
  await expect(page.getByLabel('Ticket title')).toBeFocused();
  const panel = page.getByRole('complementary', { name: 'Item 2', exact: true });
  await panel.getByLabel('Description', { exact: true }).focus();
  await page.keyboard.press('Tab');
  const composer = panel.getByLabel('Comment', { exact: true });
  await expect(composer).toBeFocused();
  await page.keyboard.press('Tab');
  await expect(panel.getByRole('button', { name: 'Previous ticket', exact: true })).toBeFocused();
  await page.keyboard.press('Shift+Tab');
  await expect(composer).toBeFocused();
  await composer.fill('Keep this mobile draft');
  await page.keyboard.press('Tab');
  await expect(panel.getByRole('button', { name: 'Send', exact: true })).toBeFocused();
  await page.keyboard.press('Tab');
  await expect(panel.getByRole('button', { name: 'Previous ticket', exact: true })).toBeFocused();
  await page.keyboard.press('Shift+Tab');
  await expect(panel.getByRole('button', { name: 'Send', exact: true })).toBeFocused();
  await page.keyboard.press('F6');
  await expect(panel).toHaveCount(0);
  await expect(page.locator('#ticket-2')).toBeFocused();
});

test('idle boards do not poll and pushed edits cause no follow-up reads or card remounts', async ({
  page,
  context,
}) => {
  const state = await mock(context);
  const reads: string[] = [];
  page.on('request', (request) => {
    if (request.method() === 'GET' && request.url().includes('/api/v1/')) reads.push(request.url());
  });
  await signIn(page);
  await page.locator('#ticket-2').click();
  await expect(page.getByLabel('Ticket title')).toBeVisible();
  const initialReads = reads.length;
  const card = await page.locator('#ticket-2').elementHandle();
  await page.waitForTimeout(4500);
  expect(reads.length).toBe(initialReads);
  const remote = state.items.find((i) => i.id === '2')!;
  remote.title = 'Pushed directly';
  remote.description = 'Also pushed directly';
  remote.version++;
  await state.notify('change', { items: [remote] }, 30);
  await expect(page.locator('#ticket-2')).toContainText(remote.title);
  await expect(page.getByLabel('Description', { exact: true })).toHaveValue(remote.description);
  expect(reads.length).toBe(initialReads);
  expect(
    await card!.evaluate(
      (node) => node === document.getElementById('ticket-2') && node.isConnected,
    ),
  ).toBe(true);
  // A delayed older frame must not roll back the card or detail.
  await state.notify('change', { items: [{ ...remote, title: 'Stale frame', version: 1 }] });
  await page.waitForTimeout(250);
  await expect(page.locator('#ticket-2')).toContainText('Pushed directly');
  expect(reads.length).toBe(initialReads);
});

test('reconnection recovers missed changes and revocation preserves an open draft', async ({
  page,
  context,
}) => {
  const state = await mock(context);
  await signIn(page);
  await page.locator('#ticket-2').click();
  state.failWrites = true;
  await page.getByLabel('Description', { exact: true }).fill('My unsaved draft');
  await state.notify('disconnect');
  await expect(page.locator('.connection')).toHaveText('Offline');
  const remote = state.items.find((i) => i.id === '2')!;
  remote.title = 'Changed while disconnected';
  remote.version++;
  await expect(page.locator('#ticket-2')).toContainText(remote.title);
  await expect(page.locator('.connection')).toHaveText('Live');
  await expect(page.getByLabel('Description', { exact: true })).toHaveValue('My unsaved draft');
  await state.notify('expired');
  await expect(page.getByLabel('Email', { exact: true })).toBeVisible();
  await expect(page.getByText(/Your open draft is preserved/)).toBeVisible();
  await page.getByLabel('Password', { exact: true }).fill('test-password');
  await page.getByLabel('Password', { exact: true }).press('Enter');
  await expect(page.locator('.connection')).toHaveText('Live');
  await expect(page.getByLabel('Description', { exact: true })).toHaveValue('My unsaved draft');
});

test('paged membership changes refresh only the affected column and keep other cards mounted', async ({
  page,
  context,
}) => {
  const rows = Array.from({ length: 55 }, (_, i) => makeItem(i + 1, 'backlog', `Backlog ${i + 1}`));
  rows.push(makeItem(100, 'todo', 'Stable neighbor'));
  const state = await mock(context, rows);
  await signIn(page);
  const neighbor = await page.locator('#ticket-100').elementHandle();
  const reads = state.listCalls;
  rows[0].status = 'complete';
  rows[0].version++;
  await state.notify('change', { items: [rows[0]] });
  await expect(
    page.getByRole('region', { name: 'Complete column', exact: true }).locator('#ticket-1'),
  ).toBeVisible();
  await expect(page.locator('#ticket-21')).toBeVisible();
  expect(state.listCalls).toBe(reads + 1);
  expect(
    await neighbor!.evaluate(
      (node) => node === document.getElementById('ticket-100') && node.isConnected,
    ),
  ).toBe(true);
});

test('pushed tickets enter and leave a filtered view without list requests', async ({
  page,
  context,
}) => {
  const rows = [makeItem(1, 'todo'), makeItem(2, 'todo')];
  const state = await mock(context, rows);
  await signIn(page);
  await page.getByRole('combobox', { name: 'Filter tag', exact: true }).click();
  await page.getByRole('option', { name: 'api', exact: true }).click();
  await expect(page.locator('.card')).toHaveCount(1);
  const reads = state.listCalls;
  rows[0].tags = [];
  rows[0].version++;
  rows[1].tags = ['api'];
  rows[1].version++;
  await state.notify();
  await expect(page.locator('#ticket-1')).toHaveCount(0);
  await expect(page.locator('#ticket-2')).toBeVisible();
  expect(state.listCalls).toBe(reads);
  await page.locator('#ticket-2').click();
  await page.getByLabel('Ticket title').fill('Saved directly');
  await expect(page.locator('#ticket-2')).toContainText('Saved directly');
  expect(state.listCalls).toBe(reads);
});

test.describe('phone layout', () => {
  test.use({ viewport: { width: 390, height: 844 }, hasTouch: true, isMobile: true });

  test('swiping right returns from details only after pending edits are saved', async ({
    page,
    context,
  }) => {
    const state = await mock(context);
    await signIn(page);
    await page.locator('#ticket-2').click();
    const panel = page.getByRole('complementary', { name: 'Item 2', exact: true });
    await expect(panel).toBeFocused();
    const cdp = await context.newCDPSession(page);
    const swipe = async (dx: number, dy = 0, selector = '.detail-id', cancel = false) => {
      const box = (await panel.locator(selector).boundingBox())!;
      const x = box.x + box.width / 2;
      const y = box.y + box.height / 2;
      await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x, y }] });
      for (let step = 1; step <= 5; step++)
        await cdp.send('Input.dispatchTouchEvent', {
          type: 'touchMove',
          touchPoints: [{ x: x + (dx * step) / 5, y: y + (dy * step) / 5 }],
        });
      await cdp.send('Input.dispatchTouchEvent', {
        type: cancel ? 'touchCancel' : 'touchEnd',
        touchPoints: [],
      });
    };

    // Taps, left/vertical/diagonal swipes, cancelled gestures, and editing stay in details.
    for (const [dx, dy] of [
      [20, 0],
      [-100, 0],
      [0, 120],
      [90, 100],
    ]) {
      await swipe(dx, dy);
      await expect(panel).toBeVisible();
    }
    await swipe(120, 0, '.detail-id', true);
    await expect(panel).toBeVisible();
    await page.getByLabel('Ticket title').focus();
    await swipe(120, 0, '.title-editor');
    await expect(panel).toBeVisible();

    await page.getByLabel('Ticket title').fill('');
    await swipe(120);
    await expect(panel).toBeVisible();
    expect(state.writes).toBe(0);

    state.failWrites = true;
    await page.getByLabel('Ticket title').fill('Keep my mobile edit');
    await swipe(120);
    await expect(page.getByText('Not saved', { exact: true })).toBeVisible();
    await expect(panel).toBeVisible();
    await expect(page.getByLabel('Ticket title')).toHaveValue('Keep my mobile edit');

    state.failWrites = false;
    await page.getByRole('button', { name: 'Retry save', exact: true }).click();
    await expect(panel).toHaveAttribute('data-save-state', 'saved');
    // A pending tag is only committed when the close path flushes it.
    await page.getByLabel('Add tag').fill('swipe-saved');
    await swipe(120, 0, '.description-editor');
    await expect(panel).toHaveCount(0);
    await expect(page).not.toHaveURL(/item=/);
    await expect(page.locator('#ticket-2')).toBeFocused();
    expect(state.items.find((item) => item.id === '2')).toMatchObject({
      title: 'Keep my mobile edit',
      tags: ['frontend', 'swipe-saved'],
    });
  });

  test('filtering out the active feed status selects the remaining tab for quick create', async ({
    page,
    context,
  }) => {
    const state = await mock(context);
    await signIn(page);
    const tabs = page.getByRole('group', { name: 'Statuses' });
    await expect(tabs.getByRole('button', { name: /^Todo/ })).toHaveAttribute(
      'aria-current',
      'true',
    );
    await expect(page.locator('#ticket-2')).toBeInViewport();
    await page.getByRole('button', { name: 'Filters', exact: true }).click();
    await page.getByLabel('Filter status', { exact: true }).click();
    await page.getByRole('option', { name: 'Complete', exact: true }).click();
    await page.getByRole('button', { name: 'Done', exact: true }).click();
    await expect(tabs.getByRole('button')).toHaveCount(1);
    await expect(tabs.getByRole('button', { name: /^Complete/ })).toHaveAttribute(
      'aria-current',
      'true',
    );
    await page.getByRole('button', { name: 'New', exact: true }).click();
    await page.getByLabel('New item title').fill('Created in the filtered feed');
    await page.getByRole('button', { name: 'Add', exact: true }).click();
    await expect
      .poll(() => state.items.find((item) => item.title === 'Created in the filtered feed')?.status)
      .toBe('complete');
  });

  test('status tabs, floating create, full-page tickets and delete', async ({ page, context }) => {
    const state = await mock(context);
    await signIn(page);
    const tabs = page.getByRole('group', { name: 'Statuses' });
    await tabs.getByRole('button', { name: /^In progress/ }).click();
    await expect(tabs.getByRole('button', { name: /^In progress/ })).toHaveAttribute(
      'aria-current',
      'true',
    );
    await expect(page.locator('#ticket-3')).toBeInViewport();

    await page.getByRole('button', { name: 'New', exact: true }).click();
    await page.getByLabel('New item title').fill('Captured on the go');
    await page.getByRole('button', { name: 'Add', exact: true }).click();
    await expect
      .poll(() => state.items.find((i) => i.title === 'Captured on the go')?.status)
      .toBe('in_progress');
    await page.getByRole('button', { name: 'Cancel', exact: true }).click();

    await page.locator('#ticket-10').click();
    const panel = page.getByRole('complementary', { name: 'Item 10', exact: true });
    await expect(panel).toBeVisible();
    const size = (await panel.boundingBox())!;
    expect(size.width).toBeCloseTo(390, 2);
    await panel.getByRole('button', { name: 'Delete ticket', exact: true }).click();
    const target = page.locator('#ticket-10');
    const confirmation = page.getByRole('dialog', { name: 'Delete TK-10?' });
    await expect(confirmation).toBeInViewport();
    await confirmation.getByRole('button', { name: 'Delete ticket', exact: true }).click();
    await expect(target).toHaveCount(0);
    expect(state.methods.at(-1)).toBe('DELETE');
  });

  test('list view fits the phone, opens tickets, and creates in the current group', async ({
    page,
    context,
  }, testInfo) => {
    const state = await mock(context);
    await signIn(page);
    await page.getByRole('button', { name: 'List view' }).click();
    await expect(page.locator('.list-view')).toBeVisible();
    await expect(page.getByRole('group', { name: 'Statuses' })).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Board view' })).toBeVisible();
    await expect(page.getByRole('button', { name: 'List view' })).toBeHidden();
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    );
    expect(overflow).toBe(0);
    const row = (await page.locator('#ticket-3').boundingBox())!;
    expect(row.height).toBeGreaterThanOrEqual(44);
    expect(row.width).toBe(390);
    await page.screenshot({ path: testInfo.outputPath('list-mobile.png') });

    await page.locator('#column-in_progress').click();
    await expect(page.locator('#ticket-3')).toHaveCount(0);
    await page.locator('#column-in_progress').click();
    await page.locator('#ticket-3').click();
    const panel = page.getByRole('complementary', { name: 'Item 3', exact: true });
    await expect(panel).toBeVisible();
    await panel.getByRole('button', { name: 'Close details', exact: true }).click();

    await page.getByRole('button', { name: 'Add item to Blocked', exact: true }).click();
    await page.getByLabel('New item title').fill('Filed from the phone list');
    await page.getByRole('button', { name: 'Add', exact: true }).click();
    await expect
      .poll(() => state.items.find((i) => i.title === 'Filed from the phone list')?.status)
      .toBe('blocked');
  });

  test('every text field is at least 16px so iOS Safari does not zoom on focus', async ({
    page,
    context,
  }) => {
    await mock(context);
    await signIn(page);
    const small = () =>
      page.evaluate(() =>
        [...document.querySelectorAll<HTMLInputElement>('input, textarea, select')]
          .filter((field) => field.checkVisibility() && !['checkbox', 'radio'].includes(field.type))
          .map((field) => [
            field.getAttribute('aria-label') || field.id,
            getComputedStyle(field).fontSize,
          ])
          .filter(([, size]) => parseFloat(size) < 16),
      );
    expect(await small()).toEqual([]);
    await page
      .getByRole('group', { name: 'Statuses' })
      .getByRole('button', { name: /^In progress/ })
      .click();
    await page.locator('#ticket-3').click();
    await expect(page.getByRole('complementary', { name: 'Item 3', exact: true })).toBeVisible();
    await expect(page.locator('.comment-input textarea')).toBeVisible();
    expect(await small()).toEqual([]);
  });

  test('press and hold lifts a card; drag vertically to reorder and to an edge to change status', async ({
    page,
    context,
  }) => {
    const state = await mock(context);
    await signIn(page);
    const tabs = page.getByRole('group', { name: 'Statuses' });
    await tabs.getByRole('button', { name: /^In progress/ }).click();
    const column = page.getByRole('region', { name: 'In progress column', exact: true });
    await expect(column.locator('.card').first()).toHaveAttribute('id', 'ticket-3');
    const touch = { pointerType: 'touch', isPrimary: true };
    const writes = state.writes;
    const drag = async (
      id: string,
      to: (box: { x: number; y: number; width: number; height: number }) => {
        clientX: number;
        clientY: number;
      },
      hold = 0,
    ) => {
      const card = page.locator(`#ticket-${id}`);
      const box = (await card.boundingBox())!;
      await card.dispatchEvent('pointerdown', {
        ...touch,
        clientX: box.x + 20,
        clientY: box.y + 20,
      });
      await expect(page.locator('.card-ghost')).toHaveCount(1);
      const end = to(box);
      for (let step = 1; step <= 4; step++)
        await page.locator('body').dispatchEvent('pointermove', {
          ...touch,
          clientX: box.x + 20 + ((end.clientX - box.x - 20) * step) / 4,
          clientY: box.y + 20 + ((end.clientY - box.y - 20) * step) / 4,
        });
      if (hold) await page.waitForTimeout(hold);
      await page.locator('body').dispatchEvent('pointerup', { ...touch, ...end });
    };

    // Hold and release in place puts the card back without saving or opening anything.
    const first = page.locator('#ticket-3');
    const start = (await first.boundingBox())!;
    await first.dispatchEvent('pointerdown', {
      ...touch,
      clientX: start.x + 20,
      clientY: start.y + 20,
    });
    await expect(page.locator('.card-ghost')).toHaveCount(1);
    await page
      .locator('body')
      .dispatchEvent('pointerup', { ...touch, clientX: start.x + 20, clientY: start.y + 20 });
    await expect(page.locator('.card-ghost')).toHaveCount(0);
    await expect(page.getByRole('dialog')).toHaveCount(0);
    await expect(page.getByRole('complementary')).toHaveCount(0);
    expect(state.writes).toBe(writes);

    // Down past the next card reorders within the column.
    const below = (await page.locator('#ticket-10').boundingBox())!;
    await drag('3', (box) => ({ clientX: box.x + 20, clientY: below.y + below.height - 4 }));
    await expect(column.locator('.card').first()).toHaveAttribute('id', 'ticket-10');
    await expect(page.locator('.card-ghost')).toHaveCount(0);

    // Once lifted, both edges name the neighbouring statuses.
    const card = page.locator('#ticket-10');
    const lift = (await card.boundingBox())!;
    await card.dispatchEvent('pointerdown', { ...touch, clientX: 30, clientY: lift.y + 20 });
    await expect(page.locator('.drag-edge')).toHaveCount(2);
    await expect(page.locator('.drag-edge').first()).toContainText('Todo');
    await expect(page.locator('.drag-edge.right')).toContainText('Code review');
    // A card lifted inside the left zone can reorder there without changing status.
    await page
      .locator('body')
      .dispatchEvent('pointermove', { ...touch, clientX: 34, clientY: lift.y + 60 });
    await page.waitForTimeout(500);
    await expect(page.locator('.drag-edge.active')).toHaveCount(0);
    await expect(tabs.getByRole('button', { name: /^In progress/ })).toHaveAttribute(
      'aria-current',
      'true',
    );
    await page.locator('body').dispatchEvent('pointercancel', { ...touch });
    await expect(page.locator('.drag-edge')).toHaveCount(0);
    await expect(page.locator('.card-ghost')).toHaveCount(0);

    // The edge zone is a fifth of the screen wide: holding well short of the edge switches status; dropping there changes it.
    await drag('10', () => ({ clientX: 320, clientY: 200 }), 700);
    await expect.poll(() => state.items.find((i) => i.id === '10')?.status).toBe('code_review');
    await expect(tabs.getByRole('button', { name: /^Code review/ })).toHaveAttribute(
      'aria-current',
      'true',
    );
    await expect(page.getByRole('dialog')).toHaveCount(0);
  });
});

test('restoring a session uses one board request with no separate item or directory reads', async ({
  page,
  context,
}) => {
  const state = await mock(context);
  await signIn(page);
  const requests: string[] = [];
  page.on('request', (request) => {
    const url = new URL(request.url());
    if (url.pathname.startsWith('/api/v1/')) requests.push(url.pathname);
  });
  await page.reload();
  await expect(page.locator('.connection')).toHaveAttribute('title', /Last sync/);
  // The streaming fetch is supplied by the fixture; the two remaining network
  // requests are the session check and the aggregate snapshot.
  expect(requests.sort()).toEqual(['/api/v1/auth/me', '/api/v1/board']);
  expect(state.listCalls).toBe(0);
});

test('updates beyond a history preview do not refetch that column', async ({ page, context }) => {
  const rows = Array.from({ length: 250 }, (_, i) => makeItem(i + 1, 'complete'));
  rows.push(makeItem(251, 'in_progress', 'Active work survives a large history'));
  const state = await mock(context, rows);
  await signIn(page);
  await expect(
    page.getByRole('region', { name: 'Complete column', exact: true }).locator('.card'),
  ).toHaveCount(20);
  await expect(page.locator('#ticket-251')).toBeVisible();
  rows[240].title = 'Edited outside the loaded preview';
  rows[240].version++;
  await state.notify('change', { items: [rows[240]] });
  await page.waitForTimeout(300);
  expect(state.listCalls).toBe(0);
  expect(state.boardCalls).toBe(1);
  await expect(page.locator('#ticket-241')).toHaveCount(0);
});

test('autosave debounces text, saves on blur, and commits tags only when finished', async ({
  page,
  context,
}) => {
  const state = await mock(context);
  await signIn(page);
  await page.locator('#ticket-2').click();
  const title = page.getByLabel('Ticket title');
  await expect(page.getByRole('button', { name: 'Save', exact: true })).toHaveCount(0);
  await title.fill('First wording');
  await page.waitForTimeout(200);
  expect(state.writes).toBe(0);
  await title.fill('Final wording');
  await expect(page.locator('.detail')).toHaveAttribute('data-save-state', 'saved');
  expect(state.writes).toBe(1);
  expect(state.items.find((i) => i.id === '2')?.title).toBe('Final wording');
  await expect(title).toBeFocused();
  await page.getByLabel('Description', { exact: true }).fill('Saved on leaving the field');
  await page.getByLabel('Add tag').fill('finished-tag');
  await expect
    .poll(() => state.items.find((i) => i.id === '2')?.description)
    .toBe('Saved on leaving the field');
  await page.waitForTimeout(650);
  expect(state.items.find((i) => i.id === '2')?.tags).not.toContain('finished-tag');
  await page.getByRole('button', { name: 'Close details', exact: true }).click();
  await expect(page.locator('.detail')).toHaveCount(0);
  expect(state.items.find((i) => i.id === '2')?.tags).toContain('finished-tag');
});

test('autosave queues edits during a slow request and waits before switching tickets', async ({
  page,
  context,
}) => {
  const state = await mock(context);
  await signIn(page);
  let release!: () => void;
  const held = new Promise<void>((resolve) => (release = resolve));
  state.beforeWrite = () => held;
  // Exercise an acknowledgement arriving on the live stream before its HTTP response.
  state.beforeReply = (saved) => state.notify('change', { items: [{ ...saved }] });
  await page.locator('#ticket-2').click();
  const title = page.getByLabel('Ticket title');
  await title.fill('First in flight');
  await expect.poll(() => state.writes).toBe(1);
  await expect(title).toBeEnabled();
  await expect(title).toBeFocused();
  await title.fill('Latest title');
  await page.getByLabel('Description', { exact: true }).fill('Typed during the request');
  await page.getByLabel('Add tag').fill('queued-tag');
  await page.keyboard.press('Enter');
  await page.getByRole('button', { name: 'Next ticket', exact: true }).click();
  await expect(page.getByRole('complementary', { name: 'Item 2', exact: true })).toBeVisible();
  expect(state.writes).toBe(1);
  release();
  await expect(page.getByRole('complementary', { name: 'Item 9', exact: true })).toBeVisible();
  expect(state.items.find((i) => i.id === '2')).toMatchObject({
    title: 'Latest title',
    description: 'Typed during the request',
    tags: ['frontend', 'queued-tag'],
    version: 3,
  });
  expect(state.bodies.map((body) => body.version)).toEqual([1, 2]);
  await expect(page.getByText('Updated by someone else')).toHaveCount(0);
});

test('invalid titles and failed automatic saves keep the panel open until resolved', async ({
  page,
  context,
}) => {
  const state = await mock(context);
  await signIn(page);
  await page.locator('#ticket-2').click();
  const title = page.getByLabel('Ticket title');
  await title.fill('');
  await page.waitForTimeout(600);
  expect(state.writes).toBe(0);
  await page.getByRole('button', { name: 'Close details', exact: true }).click();
  await expect(title).toBeVisible();
  await title.fill('   ');
  await page.getByRole('button', { name: 'Close details', exact: true }).click();
  await expect(page.getByText('Add a title to save', { exact: true })).toBeVisible();
  expect(state.writes).toBe(0);
  state.failWrites = true;
  await title.fill('Preserved on failure');
  await expect(page.getByText('Not saved', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Close details', exact: true }).click();
  await page.waitForTimeout(650);
  expect(state.writes).toBe(1);
  await expect(title).toHaveValue('Preserved on failure');
  state.failWrites = false;
  await page.getByRole('button', { name: 'Retry save', exact: true }).click();
  await expect(page.locator('.detail')).toHaveAttribute('data-save-state', 'saved');
  await page.getByRole('button', { name: 'Close details', exact: true }).click();
  await expect(page.locator('.detail')).toHaveCount(0);
});

test('a conflict before the stream update fetches the latest version for explicit reconciliation', async ({
  page,
  context,
}) => {
  const state = await mock(context);
  await signIn(page);
  await page.locator('#ticket-2').click();
  await expect(page.getByLabel('Ticket title')).toBeFocused();
  const remote = state.items.find((i) => i.id === '2')!;
  remote.description = 'Remote description';
  remote.version++;
  await page.getByLabel('Ticket title').fill('My title');
  await expect(page.getByText('Updated by someone else')).toBeVisible();
  expect(state.writes).toBe(1);
  await expect(page.getByLabel('Ticket title')).toHaveValue('My title');
  await page.getByRole('button', { name: 'Keep my edits on latest', exact: true }).click();
  await expect(page.locator('.detail')).toHaveAttribute('data-save-state', 'saved');
  expect(remote).toMatchObject({
    title: 'My title',
    description: 'Remote description',
    version: 3,
  });
});

test('account actions keep an invalid ticket draft safe', async ({ page, context }) => {
  const state = await mock(context);
  await signIn(page);
  await page.locator('#ticket-2').click();
  await page.getByLabel('Ticket title', { exact: true }).fill('');
  await page.getByRole('button', { name: 'Account menu', exact: true }).click();
  await page.getByRole('button', { name: 'Change password', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Change password', exact: true });
  await dialog.getByLabel('Current password', { exact: true }).fill('old-test-password');
  await dialog.getByLabel('New password', { exact: true }).fill('new-test-password');
  await dialog.getByLabel('Confirm new password', { exact: true }).fill('new-test-password');
  await dialog.getByRole('button', { name: 'Change password', exact: true }).click();
  await expect(dialog.getByRole('alert')).toContainText('Your ticket draft could not be saved');
  expect(state.writes).toBe(0);
  await page.getByRole('button', { name: 'Back to account menu' }).click();
  await page.getByRole('button', { name: 'Sign out', exact: true }).click();
  await expect(page.getByLabel('Ticket title', { exact: true })).toHaveValue('');
  await expect(page.getByRole('button', { name: 'Sign in', exact: true })).toHaveCount(0);
  await expect(
    page.getByText('Changes could not be saved. Resolve the item panel before leaving.'),
  ).toBeVisible();
});

test('delete from details requires confirmation, restores focus, and removes the ticket', async ({
  page,
  context,
}) => {
  const state = await mock(context);
  await signIn(page);
  await page.locator('#ticket-2').click();
  await page.getByRole('button', { name: 'Delete ticket', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Delete TK-2?' });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByRole('button', { name: 'Cancel', exact: true })).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(dialog).not.toBeVisible();
  await expect(page.getByRole('button', { name: 'Delete ticket', exact: true })).toBeFocused();
  expect(state.writes).toBe(0);
  await page.keyboard.press('Delete');
  await dialog.getByRole('button', { name: 'Delete ticket', exact: true }).click();
  await expect(dialog).not.toBeVisible();
  await expect(page.locator('#ticket-2')).toHaveCount(0);
  await expect(page.locator('.detail')).toHaveCount(0);
  await expect(page.locator('#ticket-9')).toBeFocused();
  await expect(page).not.toHaveURL(/item=2/);
  expect(state.methods).toEqual(['DELETE']);
  expect(state.items.some((i) => i.id === '2')).toBe(false);
});

test('delete waits for an in-flight autosave and uses its latest version', async ({
  page,
  context,
}) => {
  const state = await mock(context);
  await signIn(page);
  let release!: () => void;
  state.beforeReply = () => new Promise<void>((resolve) => (release = resolve));
  await page.locator('#ticket-2').click();
  await page.getByLabel('Ticket title', { exact: true }).fill('Saved before deletion');
  await expect.poll(() => state.items.find((i) => i.id === '2')?.version).toBe(2);
  await page.getByRole('button', { name: 'Delete ticket', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Delete TK-2?' });
  await expect(dialog.getByRole('button', { name: 'Please wait…' })).toBeDisabled();
  release();
  await expect(dialog.getByText('Saved before deletion', { exact: true })).toBeVisible();
  await dialog.getByRole('button', { name: 'Delete ticket', exact: true }).click();
  await expect(page.locator('#ticket-2')).toHaveCount(0);
  expect(state.methods).toEqual(['PATCH', 'DELETE']);
  expect(state.bodies.at(-1)).toEqual({ version: 2 });
});

test('delete failures preserve an invalid draft, and confirmation can be retried', async ({
  page,
  context,
}) => {
  const state = await mock(context);
  await signIn(page);
  await page.locator('#ticket-2').click();
  await page.getByLabel('Ticket title', { exact: true }).fill('');
  await page.getByLabel('Description', { exact: true }).fill('Keep this draft if deletion fails');
  await page.getByRole('button', { name: 'Delete ticket', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Delete TK-2?' });
  state.failWrites = true;
  await dialog.getByRole('button', { name: 'Delete ticket', exact: true }).click();
  await expect(dialog.getByRole('alert')).toHaveText('Save failed');
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(page.getByLabel('Description', { exact: true })).toHaveValue(
    'Keep this draft if deletion fails',
  );
  await expect(page.locator('#ticket-2')).toBeVisible();
  state.failWrites = false;
  await page.getByRole('button', { name: 'Delete ticket', exact: true }).click();
  await dialog.getByRole('button', { name: 'Delete ticket', exact: true }).click();
  await expect(page.locator('#ticket-2')).toHaveCount(0);
  expect(state.methods).toEqual(['DELETE', 'DELETE']);
});

test('a stale delete requires review and a fresh confirmation', async ({ page, context }) => {
  const state = await mock(context);
  await signIn(page);
  await page.locator('#ticket-2').focus();
  await page.keyboard.press('Delete');
  const dialog = page.getByRole('dialog', { name: 'Delete TK-2?' });
  await expect(dialog).toBeVisible();
  const current = state.items.find((i) => i.id === '2')!;
  current.version++;
  current.title = 'New work from a teammate';
  await dialog.getByRole('button', { name: 'Delete ticket', exact: true }).click();
  await expect(dialog.getByRole('alert')).toContainText('Cancel and review');
  await expect(dialog.getByRole('button', { name: 'Delete ticket', exact: true })).toBeDisabled();
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(page.locator('#ticket-2')).toContainText(current.title);
  await page.keyboard.press('Delete');
  await expect(dialog).toContainText(current.title);
  await dialog.getByRole('button', { name: 'Delete ticket', exact: true }).click();
  await expect(page.locator('#ticket-2')).toHaveCount(0);
  expect(state.bodies.map((body) => body.version)).toEqual([1, 2]);
});

test('remote deletion refreshes pagination and preserves an open unsaved draft', async ({
  page,
  context,
}) => {
  const rows = Array.from({ length: 25 }, (_, i) => makeItem(i + 1, 'backlog'));
  const state = await mock(context, rows);
  await signIn(page);
  await page.locator('#ticket-2').click();
  await page.getByLabel('Ticket title', { exact: true }).fill('');
  await page.getByLabel('Description', { exact: true }).fill('Copy this unsaved text');
  state.items.splice(
    state.items.findIndex((i) => i.id === '2'),
    1,
  );
  await state.notify('change', { reset: true });
  await expect(page.locator('#ticket-2')).toHaveCount(0);
  await expect(page.locator('#ticket-21')).toBeVisible();
  await expect(page.getByText('This ticket was deleted.', { exact: true })).toBeVisible();
  await expect(page.getByLabel('Description', { exact: true })).toHaveValue(
    'Copy this unsaved text',
  );
  await expect(page.getByLabel('Description', { exact: true })).toHaveAttribute('readonly', '');
  await expect(page.locator('.connection')).toHaveText('Live');
  await page.getByRole('button', { name: 'Discard draft and close' }).click();
  await expect(page.locator('.detail')).toHaveCount(0);
  expect(state.writes).toBe(0);
});

test('Delete edits text normally; commands expose deletion and viewers cannot delete', async ({
  page,
  context,
}) => {
  const state = await mock(context);
  await signIn(page);
  await page.locator('#ticket-2').click();
  await page.getByLabel('Description', { exact: true }).press('Delete');
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await page.keyboard.press('Escape');
  await page.keyboard.press('Control+k');
  await page.getByRole('combobox', { name: 'Find a command' }).fill('Delete');
  await page.keyboard.press('Enter');
  await expect(page.getByRole('dialog', { name: 'Delete TK-2?' })).toBeVisible();
  await page.keyboard.press('Escape');
  state.viewer = true;
  await state.notify('change', { users: true });
  await expect(page.getByLabel('Ticket title', { exact: true })).toBeDisabled();
  await expect(page.getByRole('button', { name: 'Delete ticket', exact: true })).toHaveCount(0);
  await page.locator('.detail').focus();
  await page.keyboard.press('Delete');
  await expect(page.getByRole('dialog')).toHaveCount(0);
  expect(state.writes).toBe(0);
});

test('tag manager removes an in-use tag everywhere, clears its filter, and updates open details', async ({
  page,
  context,
}) => {
  const state = await mock(context);
  await signIn(page);
  const filter = page.getByRole('combobox', { name: 'Filter tag', exact: true });
  await filter.click();
  await page.getByRole('option', { name: 'frontend', exact: true }).click();
  await page.locator('#ticket-2').click();
  await filter.click();
  await filter.press('End');
  await filter.press('Enter');
  const manager = page.getByRole('dialog', { name: 'Manage tags', exact: true });
  await expect(manager.getByLabel('Search tags')).toBeFocused();
  await manager.getByLabel('Search tags').fill('frontend');
  await expect(manager.getByRole('listitem')).toContainText('6 tickets');
  await manager.getByLabel('Search tags').press('ArrowDown');
  await page.keyboard.press('Enter');
  const confirmation = page.getByRole('dialog', { name: 'Delete tag?', exact: true });
  await expect(confirmation.getByRole('button', { name: 'Cancel', exact: true })).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(
    manager.getByRole('button', { name: 'Delete tag frontend', exact: true }),
  ).toBeFocused();
  expect(state.writes).toBe(0);
  await page.keyboard.press('Enter');
  state.failWrites = true;
  await confirmation.getByRole('button', { name: 'Delete tag', exact: true }).click();
  await expect(confirmation.getByRole('alert')).toHaveText('Save failed');
  expect(state.items.filter((item) => item.tags.includes('frontend'))).toHaveLength(6);
  state.failWrites = false;
  await confirmation.getByRole('button', { name: 'Delete tag', exact: true }).click();
  await expect(manager.getByRole('status')).toHaveText('Deleted #frontend. Tickets were kept.');
  await expect(page).not.toHaveURL(/tag=frontend/);
  await expect(page.locator('.card')).toHaveCount(12);
  await expect(page.getByRole('button', { name: 'Remove tag frontend', exact: true })).toHaveCount(
    0,
  );
  expect(state.items).toHaveLength(12);
  expect(state.items.some((item) => item.tags.includes('frontend'))).toBe(false);
  await page.keyboard.press('Escape');
  await expect(filter).toBeFocused();
  await filter.click();
  await expect(page.getByRole('option', { name: 'frontend', exact: true })).toHaveCount(0);
});

test('tag manager handles unused tags, pagination, special names, and the command menu on mobile', async ({
  page,
  context,
}) => {
  const state = await mock(context);
  state.tags.push(
    '__proto__',
    ...Array.from({ length: 205 }, (_, i) => `tag-${String(i).padStart(3, '0')}`),
    'z/repo',
  );
  await page.setViewportSize({ width: 390, height: 844 });
  await signIn(page);
  await page.getByRole('button', { name: 'Commands', exact: true }).click();
  await page.getByRole('combobox', { name: 'Find a command' }).fill('Manage tags');
  await page.keyboard.press('Enter');
  const manager = page.getByRole('dialog', { name: 'Manage tags', exact: true });
  await expect(manager).toBeInViewport();
  await manager.getByLabel('Search tags').fill('__proto__');
  await expect(manager.getByRole('listitem')).toContainText('0 tickets');
  await manager.getByLabel('Search tags').fill('z/repo');
  await expect(manager.getByRole('listitem')).toContainText('0 tickets');
  await manager.getByRole('button', { name: 'Delete tag z/repo', exact: true }).click();
  const confirmation = page.getByRole('dialog', { name: 'Delete tag?', exact: true });
  await expect(confirmation).toBeInViewport();
  await confirmation.getByRole('button', { name: 'Delete tag', exact: true }).click();
  await expect(manager.getByRole('status')).toHaveText('Deleted #z/repo. Tickets were kept.');
  expect(state.bodies.at(-1)).toEqual({ name: 'z/repo' });
  expect(state.items).toHaveLength(12);
});

test('remote tag deletion clears obsolete filters and preserves unsaved ticket edits', async ({
  page,
  context,
}) => {
  const state = await mock(context);
  await signIn(page);
  const filter = page.getByRole('combobox', { name: 'Filter tag', exact: true });
  await filter.click();
  await page.getByRole('option', { name: 'frontend', exact: true }).click();
  await page.locator('#ticket-2').click();
  await page.getByLabel('Description', { exact: true }).fill('Keep this edit through tag deletion');
  state.tags = state.tags.filter((tag) => tag !== 'frontend');
  for (const item of state.items)
    if (item.tags.includes('frontend')) {
      item.tags = [];
      item.version++;
    }
  await state.notify('change', { reset: true });
  await expect(page).not.toHaveURL(/tag=frontend/);
  await expect(page.getByText('Updated by someone else')).toBeVisible();
  await expect(page.getByLabel('Description', { exact: true })).toHaveValue(
    'Keep this edit through tag deletion',
  );
  await page.getByRole('button', { name: 'Keep my edits on latest' }).click();
  await expect(page.locator('.detail')).toHaveAttribute('data-save-state', 'saved');
  const current = state.items.find((item) => item.id === '2')!;
  expect(current.description).toBe('Keep this edit through tag deletion');
  expect(current.tags).toEqual([]);
});

test('viewers cannot manage tags', async ({ page, context }) => {
  const state = await mock(context);
  state.viewer = true;
  await signIn(page);
  const filter = page.getByRole('combobox', { name: 'Filter tag', exact: true });
  await filter.click();
  await expect(page.getByRole('option', { name: 'Manage tags…', exact: true })).toHaveCount(0);
  await page.keyboard.press('Escape');
  await page.getByRole('button', { name: 'Commands', exact: true }).click();
  await page.getByRole('combobox', { name: 'Find a command' }).fill('Manage tags');
  await expect(page.getByText('No matching commands.')).toBeVisible();
  expect(state.writes).toBe(0);
});

test('a delayed refresh cannot replace an acknowledged autosave', async ({ page, context }) => {
  const state = await mock(context);
  await signIn(page);
  await page.locator('#ticket-2').click();
  const old = structuredClone(state.items);
  let release!: () => void;
  const held = new Promise<void>((resolve) => (release = resolve));
  let requested!: () => void;
  const started = new Promise<void>((resolve) => (requested = resolve));
  await page.route('**/api/v1/board?*', async (route) => {
    requested();
    await held;
    await route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({
        columns: Object.fromEntries(
          statuses.map((status) => [
            status,
            {
              items: old.filter((i) => i.status === status).map(({ description, ...rest }) => rest),
            },
          ]),
        ),
        users: { users: [user, { id: '2', name: 'Build Agent', role: 'member' }] },
        tags: { tags: ['api', 'frontend'] },
      }),
    });
  });
  await page.keyboard.press('r');
  await started;
  await page.getByLabel('Ticket title', { exact: true }).fill('Saved while refresh was pending');
  await expect(page.locator('.detail')).toHaveAttribute('data-save-state', 'saved');
  await expect(page.locator('#ticket-2')).toContainText('Saved while refresh was pending');
  expect(state.items.find((i) => i.id === '2')?.version).toBe(2);
  const refreshed = page.waitForResponse('**/api/v1/board?*');
  release();
  await refreshed;
  await expect(page.locator('#ticket-2')).toContainText('Saved while refresh was pending');
});

test('the board loads and can save when the event stream is unavailable', async ({
  page,
  context,
}) => {
  const state = await mock(context, undefined, true);
  await page.goto('/');
  await page.getByLabel('Email', { exact: true }).fill('test@example.invalid');
  await page.getByLabel('Password', { exact: true }).fill('test-password');
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  await expect(page.locator('#ticket-2')).toBeVisible();
  await page.locator('#ticket-2').click();
  await page.getByLabel('Ticket title').fill('Saved without the stream');
  await expect(page.locator('.detail')).toHaveAttribute('data-save-state', 'saved');
  expect(state.items.find((item) => item.id === '2')?.title).toBe('Saved without the stream');
});

test('draft rebasing keeps later edits and accepts unrelated server values', () => {
  const base = makeItem(1, 'todo');
  const sent = { ...itemFields(base), title: 'Submitted', tags: ['api', 'submitted'] };
  const latest = {
    ...base,
    ...sent,
    title: 'Normalized by server',
    type: 'bug' as const,
    version: 2,
  };
  const draft = {
    ...sent,
    description: 'Typed during save',
    tags: ['api', 'queued'],
    assignees: ['2'],
  };
  const rebased = rebaseFields(latest, sent, draft);
  expect(rebased).toMatchObject({
    title: 'Normalized by server',
    type: 'bug',
    description: 'Typed during save',
    tags: ['api', 'queued'],
    assignees: ['2'],
  });
  expect(itemPatch(latest, rebased)).toEqual({
    description: 'Typed during save',
    add_tags: ['queued'],
    remove_tags: ['submitted'],
    add_assignees: ['2'],
    remove_assignees: ['1'],
  });
  expect(itemPatch(latest, itemFields(latest))).toEqual({});
});
