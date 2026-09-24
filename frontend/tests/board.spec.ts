import { test, expect, type BrowserContext } from '@playwright/test';
import type { Item, Status, Updates } from '../src/api';

const user = { id: '1', name: 'Alex Morgan', role: 'member' };
const statuses: Status[] = ['backlog', 'todo', 'in_progress', 'code_review', 'blocked', 'complete', 'void'];
const titles = [
  'Add cursor pagination to activity API', 'Surface stale-edit conflicts in the CLI',
  'Preserve focus across live updates', 'Index tag and assignee lookups',
  'Handle cancelled database transactions', 'Verify restore from a WAL backup',
  'Remove legacy token flags', 'Support an empty description', 'Bound the SQLite read pool',
  'Keyboard navigation between columns', 'Document server configuration', 'Test concurrent priority moves',
];
function item(id: number, status: Status, title = titles[(id - 1) % titles.length]): Item {
  return { id: String(id), title, description: 'Keep the API response bounded and preserve the existing contract.',
    type: id % 3 === 0 ? 'bug' : id % 3 === 1 ? 'feature' : 'task', status, priority: id * 1024,
    version: 1, tags: [id % 2 ? 'api' : 'frontend'], assignees: id % 3 ? ['1'] : ['2'],
    created_by: '1', created_at: '2026-09-24T12:00:00Z', updated_at: '2026-09-24T12:00:00Z' };
}

async function mock(context: BrowserContext, rows = Array.from({ length: 12 }, (_, i) => item(i + 1, statuses[i % statuses.length]))) {
  // Keep a real ReadableStream open, with fragmented SSE frames, while the
  // ordinary API remains mocked. This exercises the production stream parser.
  await context.addInitScript(() => {
    const fetch = window.fetch.bind(window);
    window.fetch = async (input, init) => {
      if (input !== '/api/v1/events') return fetch(input, init);
      let remove = () => {};
      const stream = new ReadableStream<Uint8Array>({
        start(controller) {
          const encoder = new TextEncoder();
          const send = (kind: string, updates: unknown) => {
            if (kind === 'disconnect') { remove(); controller.close(); return; }
            controller.enqueue(encoder.encode(`event: ${kind.slice(0, 2)}`));
            controller.enqueue(encoder.encode(`${kind.slice(2)}\ndata: ${JSON.stringify(updates)}\n\n`));
          };
          const change = (event: Event) => {
            const { kind, updates, count } = (event as CustomEvent).detail;
            for (let i = 0; i < count; i++) send(kind, updates);
          };
          const abort = () => { remove(); controller.error(new DOMException('Aborted', 'AbortError')); };
          remove = () => { window.removeEventListener('test:remote-change', change); init?.signal?.removeEventListener('abort', abort); };
          window.addEventListener('test:remote-change', change);
          init?.signal?.addEventListener('abort', abort);
          send('ready', { reset: true, users: true });
        },
        cancel() { remove(); },
      });
      return new Response(stream, { headers: { 'Content-Type': 'text/event-stream' } });
    };
  });
  const state = { items: rows, failWrites: false, writes: 0, viewer: false, listCalls: 0, boardCalls: 0,
    beforeWrite: null as (() => Promise<void>) | null, beforeReply: null as ((item: Item) => Promise<unknown>) | null,
    bodies: [] as Record<string, unknown>[],
    notify: (kind = 'change', updates: Updates = { items: rows }, count = 1): Promise<unknown[]> =>
      Promise.all(context.pages().map(page => page.evaluate(detail => window.dispatchEvent(new CustomEvent('test:remote-change', { detail })), { kind, updates, count }))) };
  await context.route('**/api/v1/**', async route => {
    const request = route.request(); const url = new URL(request.url());
    const path = url.pathname.replace('/api/v1', '');
    const body = request.postDataJSON();
    const reply = (data: unknown, status = 200) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) });
    if (path === '/auth/login') return reply({ user: { ...user, role: state.viewer ? 'viewer' : 'member' }, session_token: 'test-only-session', expires_at: 9999999999 });
    if (path === '/auth/me') return reply({ ...user, role: state.viewer ? 'viewer' : 'member' });
    if (path === '/auth/logout') return reply({ logged_out: true });
    if (path === '/users') return reply({ users: [{ ...user, role: state.viewer ? 'viewer' : 'member' }, { id: '2', name: 'Build Agent', role: 'member' }] });
    if (path === '/tags') return reply({ tags: ['api', 'frontend'] });
    if (path.endsWith('/activity')) return reply({ activity: [] });
    const list = (status: string | null, limit: number, cursor = '') => {
      const matching = state.items.filter(i => (!status || i.status === status) && (!url.searchParams.get('tag') || i.tags.includes(url.searchParams.get('tag')!)) && (!url.searchParams.get('assignee') || (url.searchParams.get('assignee') === 'none' ? !i.assignees.length : i.assignees.includes(url.searchParams.get('assignee')!)))).sort((a, b) => a.priority - b.priority);
      const start = Number(cursor); const end = start + limit;
      return { items: matching.slice(start, end).map(({ description, ...rest }) => rest), ...(end < matching.length ? { next_cursor: String(end) } : {}) };
    };
    if (path === '/board') {
      state.boardCalls++;
      const selected = url.searchParams.get('status');
      const columns = Object.fromEntries(statuses.filter(status => !selected || status === selected).map(status =>
        [status, list(status, !selected && ['backlog', 'complete', 'void'].includes(status) ? 20 : 100)]));
      return reply({ columns, users: { users: [{ ...user, role: state.viewer ? 'viewer' : 'member' }, { id: '2', name: 'Build Agent', role: 'member' }] }, tags: { tags: ['api', 'frontend'] } });
    }
    if (path === '/items' && request.method() === 'GET') {
      state.listCalls++;
      return reply(list(url.searchParams.get('status'), Number(url.searchParams.get('limit')), url.searchParams.get('cursor') || ''));
    }
    if (request.method() !== 'GET') {
      state.writes++; state.bodies.push(body);
      await state.beforeWrite?.();
      if (state.failWrites) return reply({ error: { code: 'internal', message: 'Save failed' } }, 500);
    }
    if (path === '/items' && request.method() === 'POST') {
      const created = { ...item(Math.max(...state.items.map(i => Number(i.id)), 0) + 1, body.status), ...body };
      state.items.push(created); return reply(created);
    }
    const id = path.split('/')[2]; const current = state.items.find(i => i.id === id);
    if (!current) return reply({ error: { code: 'not_found', message: 'Item not found' } }, 404);
    if (request.method() === 'GET') return reply(current);
    if (current.version !== body.version) return reply({ error: { code: 'conflict', message: 'Item changed', current_version: current.version } }, 409);
    if (path.endsWith('/move')) {
      const anchor = state.items.find(i => i.id === (body.before || body.after))!;
      current.priority = anchor.priority + (body.before ? -1 : 1);
    } else {
      for (const key of ['title', 'description', 'status', 'type'] as const) if (key in body) Object.assign(current, { [key]: body[key] });
      for (const [field, add, remove] of [['tags', 'add_tags', 'remove_tags'], ['assignees', 'add_assignees', 'remove_assignees']] as const) current[field] = [...new Set([...current[field].filter(v => !(body[remove] || []).includes(v)), ...(body[add] || [])])] as string[];
    }
    current.version++; await state.beforeReply?.(current); return reply(current);
  });
  return state;
}

async function signIn(page: import('@playwright/test').Page) {
  await page.goto('/');
  await page.getByLabel('Email', { exact: true }).fill('test@example.invalid');
  await page.getByLabel('Password', { exact: true }).fill('test-password');
  await page.getByLabel('Password', { exact: true }).press('Enter');
  await expect(page.getByRole('region', { name: 'Todo column', exact: true })).toBeVisible();
  await expect(page.locator('.connection')).toHaveText('Live');
  await expect(page.locator('.connection')).toHaveAttribute('title', /Last sync/);
}

test('live role changes update permissions without losing an open draft', async ({ page, context }) => {
  const state = await mock(context);
  await signIn(page);
  await page.locator('#ticket-2').click();
  await page.getByLabel('Ticket title', { exact: true }).fill('Keep my draft');
  state.viewer = true;
  await state.notify('change', { users: true });
  await expect(page.getByLabel('Ticket title', { exact: true })).toBeDisabled();
  await expect(page.getByLabel('Ticket title', { exact: true })).toHaveValue('Keep my draft');
  await expect(page.getByRole('button', { name: 'Sign in', exact: true })).toHaveCount(0);
  state.viewer = false;
  await state.notify('change', { users: true });
  await expect(page.getByLabel('Ticket title', { exact: true })).toBeEnabled();
  await expect(page.getByLabel('Ticket title', { exact: true })).toHaveValue('Keep my draft');
  await expect(page.getByText('All changes saved', { exact: true })).toBeVisible();
  expect(state.items.find(item => item.id === '2')?.title).toBe('Keep my draft');
  await page.keyboard.press('Escape');
  await page.keyboard.press('Escape');
  await page.keyboard.press('c');
  await page.getByLabel('New item title').fill('Keep my new ticket draft');
  state.viewer = true;
  await state.notify('change', { users: true });
  await expect(page.getByLabel('New item title')).toBeDisabled();
  await expect(page.getByLabel('New item title')).toHaveValue('Keep my new ticket draft');
});


test('fullscreen board supports keyboard capture, navigation, moves, and editing', async ({ page, context }) => {
  const state = await mock(context);
  const errors: string[] = []; page.on('pageerror', error => errors.push(error.message));
  await signIn(page);
  await expect(page.locator('.kanban-column')).toHaveCount(7);
  await expect(page.locator('nav, .sidebar, .list-heading')).toHaveCount(0);
  await page.locator('#column-todo').focus();
  await page.keyboard.press('c');
  await page.getByLabel('New item title').fill('Keyboard-only capture');
  await page.keyboard.press('Enter');
  await expect(page.getByRole('button', { name: /TK-13: Keyboard-only capture/ })).toBeVisible();
  await page.keyboard.press('Escape');
  await page.locator('#ticket-13').focus();
  await page.keyboard.press('Alt+ArrowRight');
  await expect(page.getByRole('region', { name: 'In progress column', exact: true }).getByRole('button', { name: /TK-13:/ })).toBeVisible();
  await expect.poll(() => state.items.find(i => i.id === '13')?.status).toBe('in_progress');
  await page.keyboard.press('Enter');
  await page.getByLabel('Ticket title').fill('Edited by keyboard');
  await page.keyboard.press('Control+Enter');
  await expect(page.getByText('All changes saved', { exact: true })).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.getByRole('complementary', { name: 'Item 13', exact: true })).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(page.locator('#ticket-13')).toBeFocused();
  await expect(page.locator('#ticket-13')).toContainText('Edited by keyboard');
  expect(errors).toEqual([]);
});

test('live changes move remote cards, preserves drafts, and reconciles version conflicts', async ({ page, context }) => {
  const state = await mock(context);
  await signIn(page);
  await page.locator('#ticket-2').click();
  await page.getByLabel('Description', { exact: true }).fill('My unsaved draft');
  const remote = state.items.find(i => i.id === '2')!;
  remote.title = 'Changed by another user'; remote.status = 'code_review'; remote.description = 'Remote description'; remote.version++; await state.notify();
  await expect(page.getByRole('region', { name: 'Code review column', exact: true }).getByRole('button', { name: /Changed by another user/ })).toBeVisible({ timeout: 7000 });
  await expect(page.getByLabel('Description', { exact: true })).toHaveValue('My unsaved draft');
  await expect(page.getByText('Updated by someone else')).toBeVisible();
  await page.getByRole('button', { name: 'Keep my edits on latest' }).click();
  await expect(page.getByLabel('Ticket title')).toHaveValue('Changed by another user');
  await expect.poll(() => remote.description).toBe('My unsaved draft');
  expect(remote.title).toBe('Changed by another user');
  expect(remote.status).toBe('code_review');
});

test('one board request loads every status with bounded history and explicit pagination', async ({ page, context }) => {
  const rows = Array.from({ length: 55 }, (_, i) => item(i + 1, 'backlog', `Backlog ${i + 1}`));
  rows.push(item(100, 'complete', 'A completed item beyond the first global page'));
  const state = await mock(context, rows); await signIn(page);
  expect(state.boardCalls).toBe(1);
  expect(state.listCalls).toBe(0);
  await expect(page.getByRole('region', { name: 'Backlog column', exact: true }).locator('.card')).toHaveCount(20);
  await expect(page.locator('#ticket-100')).toBeVisible();
  await page.getByRole('button', { name: 'Load more', exact: true }).click();
  await expect(page.getByRole('region', { name: 'Backlog column', exact: true }).locator('.card')).toHaveCount(55);
});

test('failed saves keep draft and viewer controls cannot mutate', async ({ page, context }) => {
  const state = await mock(context); await signIn(page);
  await page.locator('#ticket-1').click();
  await page.getByLabel('Ticket title').fill('Do not lose this draft');
  state.failWrites = true;
  await expect(page.getByText('Save failed', { exact: true })).toBeVisible();
  await expect(page.getByLabel('Ticket title')).toHaveValue('Do not lose this draft');
  expect(state.writes).toBe(1);
  await page.getByRole('button', { name: 'Discard unsaved changes', exact: true }).click();
  state.viewer = true; state.failWrites = false;
  await page.reload(); await expect(page.locator('.connection')).toHaveText('Live');
  await expect(page.getByRole('button', { name: 'New', exact: true })).toHaveCount(0);
  await expect(page.getByLabel('Ticket title')).toBeDisabled();
});

test('compact layout and command palette stay keyboard accessible on narrow screens', async ({ page, context }, testInfo) => {
  await mock(context); await signIn(page);
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


test('arrows and vim motions keep row position through empty columns and never act on stale selection', async ({ page, context }) => {
  const state = await mock(context, [item(1, 'backlog'), item(2, 'backlog'), item(3, 'in_progress'), item(4, 'in_progress')]);
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
  await page.keyboard.press('g'); await page.keyboard.press('g');
  await expect(page.locator('#ticket-3')).toBeFocused();
  await page.keyboard.press('/');
  await page.getByLabel('Search loaded items').fill(state.items[3].title);
  await page.keyboard.press('ArrowDown');
  await expect(page.locator('#ticket-4')).toBeFocused();
  await page.keyboard.press('/');
  await page.getByLabel('Search loaded items').fill('no results');
  await page.keyboard.press('Escape');
  await expect(page.locator('#column-in_progress')).toBeFocused();
  await page.keyboard.press('a');
  await expect(page.getByRole('dialog')).toHaveCount(0);
  expect(state.writes).toBe(0);
  await page.getByRole('button', { name: 'Clear filters', exact: true }).click();
  await page.keyboard.press('Escape');
  await expect(page.locator('.board [tabindex="0"]')).toHaveCount(1);
});

test('quick property menus assign, unassign, tag, change type and status with focus restored', async ({ page, context }) => {
  const state = await mock(context); await signIn(page);
  await page.locator('#ticket-2').focus();
  const ticket = state.items.find(i => i.id === '2')!;
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
  await expect(page.locator('#column-blocked').locator('..').locator('..').getByRole('button', { name: /TK-2:/ })).toBeVisible();
  state.failWrites = true;
  await choose('a', 'Build');
  await expect(page.getByRole('alert')).toContainText('Save failed');
  expect(ticket.assignees).toEqual(['1']);
});

test('create and fully edit a ticket without losing pending tags or drafts', async ({ page, context }) => {
  const state = await mock(context); await signIn(page);
  await page.keyboard.press('c');
  await page.getByLabel('New item title').fill('Keyboard workflow');
  await page.keyboard.press('Control+Enter');
  const panel = page.getByRole('complementary', { name: 'Item 13', exact: true });
  await expect(page.getByLabel('Ticket title')).toBeFocused();
  await page.getByLabel('Ticket title').fill('Full keyboard workflow');
  await page.keyboard.press('Escape');
  await expect(panel).toBeFocused();
  await page.keyboard.press('a');
  await expect(page.getByRole('combobox', { name: 'Add assignee', exact: true })).toBeFocused();
  await page.keyboard.press('ArrowDown'); await page.keyboard.press('a'); await page.keyboard.press('Enter');
  await expect(page.getByRole('button', { name: 'Remove assignee Alex Morgan' })).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(panel).toBeFocused();
  await page.keyboard.press('m');
  await expect(page.getByRole('button', { name: 'Remove assignee Alex Morgan' })).toHaveCount(0);
  await page.keyboard.press('m');
  await expect(page.getByRole('button', { name: 'Remove assignee Alex Morgan' })).toBeVisible();
  await page.keyboard.press('s');
  await expect(page.getByRole('combobox', { name: 'Ticket status', exact: true })).toBeFocused();
  await page.keyboard.press('ArrowDown'); await page.keyboard.press('i'); await page.keyboard.press('Enter');
  await page.keyboard.press('Escape');
  await page.keyboard.press('y');
  await expect(page.getByRole('combobox', { name: 'Ticket type', exact: true })).toBeFocused();
  await page.keyboard.press('ArrowDown'); await page.keyboard.press('b'); await page.keyboard.press('Enter');
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
  const created = state.items.find(i => i.id === '13')!;
  expect(created).toMatchObject({ title: 'Full keyboard workflow', description: 'hjkl / c a s t y should stay text', assignees: ['1'], tags: ['new-label'], type: 'bug', status: 'in_progress' });
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

test('keyboard and drag reordering work within and across columns, including filtered status', async ({ page, context }) => {
  const state = await mock(context, [item(1, 'todo'), item(2, 'todo'), item(3, 'todo'), item(4, 'backlog')]);
  await signIn(page);
  await page.locator('#ticket-2').focus();
  await page.keyboard.press('Shift+K');
  await expect(page.locator('#column-todo').locator('..').locator('..').locator('.card').first()).toHaveAttribute('id', 'ticket-2');
  await expect(page.locator('.board-feedback')).toContainText('moved before');
  await page.keyboard.press('Alt+ArrowDown');
  await expect(page.locator('#column-todo').locator('..').locator('..').locator('.card').nth(1)).toHaveAttribute('id', 'ticket-2');
  await expect(page.locator('.board-feedback')).toContainText('moved after');
  await page.locator('#ticket-3').dragTo(page.locator('#ticket-1'), { targetPosition: { x: 20, y: 5 } });
  await expect(page.getByRole('region', { name: 'Todo column', exact: true }).locator('.card').first()).toHaveAttribute('id', 'ticket-3');
  await page.locator('#ticket-4').dragTo(page.locator('#ticket-1'), { targetPosition: { x: 20, y: 5 } });
  await expect.poll(() => state.items.find(i => i.id === '4')?.status).toBe('todo');
  await expect(page.getByRole('region', { name: 'Todo column', exact: true }).locator('.card').nth(1)).toHaveAttribute('id', 'ticket-4');
  await page.getByLabel('Filter status').click();
  await page.getByRole('option', { name: 'Todo', exact: true }).click();
  await expect(page.locator('.kanban-column')).toHaveCount(1);
  await page.locator('#ticket-4').focus();
  await page.keyboard.press('Shift+L');
  await expect.poll(() => state.items.find(i => i.id === '4')?.status).toBe('in_progress');
  await expect(page.locator('#ticket-4')).toHaveCount(0);
  await expect(page.locator('.board :focus')).toHaveCount(1);
});

test('command menus support vim control keys, escape button, and focus handoff to search', async ({ page, context }) => {
  await mock(context); await signIn(page);
  await page.locator('#ticket-2').focus();
  await page.keyboard.press('Control+k');
  const search = page.getByRole('combobox', { name: 'Find a command' });
  await search.fill('Go to');
  await page.keyboard.press('Control+j');
  await expect(search).toHaveAttribute('aria-activedescendant', 'command-1');
  await page.keyboard.press('Control+k');
  await expect(search).toHaveAttribute('aria-activedescendant', 'command-0');
  await search.fill('Search loaded');
  await page.keyboard.press('Enter');
  await expect(page.getByLabel('Search loaded items')).toBeFocused();
  await page.keyboard.press('Escape');
  await page.keyboard.press('Control+k');
  await search.fill('unmatched command');
  await page.keyboard.press('ArrowDown'); await page.keyboard.press('Enter');
  await expect(page.getByRole('dialog')).toBeVisible();
  await page.keyboard.press('Tab'); await page.keyboard.press('Enter');
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect(page.locator('#ticket-2')).toBeFocused();
  await page.getByRole('button', { name: 'Commands', exact: true }).focus();
  await page.keyboard.press('?');
  await expect(page.getByRole('dialog', { name: 'Keyboard shortcuts', exact: true })).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.getByRole('button', { name: 'Commands', exact: true })).toBeFocused();
});

test('live moves keep keyboard selection and viewer shortcuts never write', async ({ page, context }) => {
  const state = await mock(context); await signIn(page);
  await page.locator('#ticket-2').focus();
  const remote = state.items.find(i => i.id === '2')!;
  remote.status = 'blocked'; remote.version++; await state.notify();
  await expect(page.getByRole('region', { name: 'Blocked column', exact: true }).locator('#ticket-2')).toBeFocused({ timeout: 7000 });
  await page.keyboard.press('c');
  await expect(page.getByRole('region', { name: 'Blocked column', exact: true }).getByLabel('New item title')).toBeFocused();
  await page.keyboard.press('Escape');
  state.viewer = true;
  await page.reload(); await expect(page.locator('.connection')).toHaveText('Live');
  await page.locator('#ticket-2').focus();
  for (const key of ['c', 'a', 's', 't', 'y', 'm', 'Shift+J', 'Alt+ArrowRight']) await page.keyboard.press(key);
  expect(state.writes).toBe(0);
  await page.keyboard.press('Enter');
  await expect(page.getByRole('complementary', { name: 'Item 2', exact: true })).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(page.locator('#ticket-2')).toBeFocused();
});


test('saving a pending tag works from its field and failed save-and-close preserves focus', async ({ page, context }) => {
  const state = await mock(context); await signIn(page);
  await page.locator('#ticket-2').focus(); await page.keyboard.press('t');
  await page.getByRole('combobox', { name: 'Find a command' }).fill('new tag');
  await page.keyboard.press('Enter');
  await expect(page.getByLabel('Add tag')).toBeFocused();
  await page.getByLabel('Add tag').fill('keyboard');
  state.failWrites = true;
  await page.keyboard.press('Control+Shift+Enter');
  await expect(page.getByText('Save failed', { exact: true })).toBeVisible();
  await expect(page.getByLabel('Add tag')).toBeFocused();
  await expect(page.getByRole('button', { name: 'Remove tag keyboard', exact: true })).toBeVisible();
  state.failWrites = false;
  await page.getByRole('button', { name: 'Retry save', exact: true }).click();
  await expect(page.getByText('All changes saved', { exact: true })).toBeVisible();
  expect(state.items.find(i => i.id === '2')?.tags).toEqual(['frontend', 'keyboard']);
  await page.getByRole('button', { name: 'Close details', exact: true }).click();
  await expect(page.locator('.detail')).toHaveCount(0);
  await expect(page.locator('#ticket-2')).toBeFocused();
});

test('mobile details keep Tab in the panel and return to the selected ticket', async ({ page, context }) => {
  await mock(context); await signIn(page);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.locator('#ticket-2').click();
  await expect(page.getByLabel('Ticket title')).toBeFocused();
  const panel = page.getByRole('complementary', { name: 'Item 2', exact: true });
  await panel.getByRole('button', { name: 'Activity', exact: true }).focus();
  await page.keyboard.press('Tab');
  await expect(panel.getByRole('button', { name: 'Previous ticket', exact: true })).toBeFocused();
  await page.keyboard.press('Shift+Tab');
  await expect(panel.getByRole('button', { name: 'Activity', exact: true })).toBeFocused();
  await page.keyboard.press('F6');
  await expect(panel).toHaveCount(0);
  await expect(page.locator('#ticket-2')).toBeFocused();
});

test('idle boards do not poll and pushed edits cause no follow-up reads or card remounts', async ({ page, context }) => {
  const state = await mock(context);
  const reads: string[] = [];
  page.on('request', request => { if (request.method() === 'GET' && request.url().includes('/api/v1/')) reads.push(request.url()); });
  await signIn(page);
  await page.locator('#ticket-2').click();
  await expect(page.getByLabel('Ticket title')).toBeVisible();
  const initialReads = reads.length;
  const card = await page.locator('#ticket-2').elementHandle();
  await page.waitForTimeout(4500);
  expect(reads.length).toBe(initialReads);
  const remote = state.items.find(i => i.id === '2')!;
  remote.title = 'Pushed directly'; remote.description = 'Also pushed directly'; remote.version++;
  await state.notify('change', { items: [remote] }, 30);
  await expect(page.locator('#ticket-2')).toContainText(remote.title);
  await expect(page.getByLabel('Description', { exact: true })).toHaveValue(remote.description);
  expect(reads.length).toBe(initialReads);
  expect(await card!.evaluate(node => node === document.getElementById('ticket-2') && node.isConnected)).toBe(true);
  // A delayed older frame must not roll back the card or detail.
  await state.notify('change', { items: [{ ...remote, title: 'Stale frame', version: 1 }] });
  await page.waitForTimeout(250);
  await expect(page.locator('#ticket-2')).toContainText('Pushed directly');
  expect(reads.length).toBe(initialReads);
});

test('reconnection recovers missed changes and revocation preserves an open draft', async ({ page, context }) => {
  const state = await mock(context);
  await signIn(page);
  await page.locator('#ticket-2').click();
  state.failWrites = true;
  await page.getByLabel('Description', { exact: true }).fill('My unsaved draft');
  await state.notify('disconnect');
  await expect(page.locator('.connection')).toHaveText('Offline');
  const remote = state.items.find(i => i.id === '2')!;
  remote.title = 'Changed while disconnected'; remote.version++;
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

test('paged membership changes refresh only the affected column and keep other cards mounted', async ({ page, context }) => {
  const rows = Array.from({ length: 55 }, (_, i) => item(i + 1, 'backlog', `Backlog ${i + 1}`));
  rows.push(item(100, 'todo', 'Stable neighbor'));
  const state = await mock(context, rows);
  await signIn(page);
  const neighbor = await page.locator('#ticket-100').elementHandle();
  const reads = state.listCalls;
  rows[0].status = 'complete'; rows[0].version++;
  await state.notify('change', { items: [rows[0]] });
  await expect(page.getByRole('region', { name: 'Complete column', exact: true }).locator('#ticket-1')).toBeVisible();
  await expect(page.locator('#ticket-21')).toBeVisible();
  expect(state.listCalls).toBe(reads + 1);
  expect(await neighbor!.evaluate(node => node === document.getElementById('ticket-100') && node.isConnected)).toBe(true);
});

test('pushed tickets enter and leave a filtered view without list requests', async ({ page, context }) => {
  const rows = [item(1, 'todo'), item(2, 'todo')];
  const state = await mock(context, rows);
  await signIn(page);
  await page.getByRole('combobox', { name: 'Filter tag', exact: true }).click();
  await page.getByRole('option', { name: 'api', exact: true }).click();
  await expect(page.locator('.card')).toHaveCount(1);
  const reads = state.listCalls;
  rows[0].tags = []; rows[0].version++;
  rows[1].tags = ['api']; rows[1].version++;
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

  test('status tabs, long-press move, floating create and full-page tickets', async ({ page, context }) => {
    const state = await mock(context); await signIn(page);
    const tabs = page.getByRole('group', { name: 'Statuses' });
    await tabs.getByRole('button', { name: /^In progress/ }).click();
    await expect(tabs.getByRole('button', { name: /^In progress/ })).toHaveAttribute('aria-current', 'true');
    await expect(page.locator('#ticket-3')).toBeInViewport();

    const card = page.locator('#ticket-3'); const box = (await card.boundingBox())!;
    await card.dispatchEvent('pointerdown', { pointerType: 'touch', clientX: box.x + 20, clientY: box.y + 20, isPrimary: true });
    const sheet = page.getByRole('dialog', { name: 'Actions for TK-3' });
    await expect(sheet).toBeVisible();
    await card.dispatchEvent('pointerup', { pointerType: 'touch' }).catch(() => {});
    await sheet.getByRole('button', { name: 'Code review', exact: true }).click();
    await expect.poll(() => state.items.find(i => i.id === '3')?.status).toBe('code_review');
    await expect(tabs.getByRole('button', { name: /^In progress/ })).toHaveAttribute('aria-current', 'true');

    await page.getByRole('button', { name: 'New', exact: true }).click();
    await page.getByLabel('New item title').fill('Captured on the go');
    await page.getByRole('button', { name: 'Add', exact: true }).click();
    await expect.poll(() => state.items.find(i => i.title === 'Captured on the go')?.status).toBe('in_progress');
    await page.getByRole('button', { name: 'Cancel', exact: true }).click();

    await page.locator('#ticket-10').click();
    const panel = page.getByRole('complementary', { name: 'Item 10', exact: true });
    await expect(panel).toBeVisible();
    const size = (await panel.boundingBox())!;
    expect(size.width).toBe(390);
    await panel.getByRole('button', { name: 'Close details', exact: true }).click();
    await expect(panel).toHaveCount(0);
  });
});

test('restoring a session uses one board request with no separate item or directory reads', async ({ page, context }) => {
  const state = await mock(context);
  await signIn(page);
  const requests: string[] = [];
  page.on('request', request => {
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
  const rows = Array.from({ length: 250 }, (_, i) => item(i + 1, 'complete'));
  rows.push(item(251, 'in_progress', 'Active work survives a large history'));
  const state = await mock(context, rows);
  await signIn(page);
  await expect(page.getByRole('region', { name: 'Complete column', exact: true }).locator('.card')).toHaveCount(20);
  await expect(page.locator('#ticket-251')).toBeVisible();
  rows[240].title = 'Edited outside the loaded preview'; rows[240].version++;
  await state.notify('change', { items: [rows[240]] });
  await page.waitForTimeout(300);
  expect(state.listCalls).toBe(0);
  expect(state.boardCalls).toBe(1);
  await expect(page.locator('#ticket-241')).toHaveCount(0);
});


test('autosave debounces text, saves on blur, and commits tags only when finished', async ({ page, context }) => {
  const state = await mock(context); await signIn(page);
  await page.locator('#ticket-2').click();
  const title = page.getByLabel('Ticket title');
  await expect(page.getByRole('button', { name: 'Save', exact: true })).toHaveCount(0);
  await title.fill('First wording');
  await page.waitForTimeout(200);
  expect(state.writes).toBe(0);
  await title.fill('Final wording');
  await expect(page.getByText('All changes saved', { exact: true })).toBeVisible();
  expect(state.writes).toBe(1);
  expect(state.items.find(i => i.id === '2')?.title).toBe('Final wording');
  await expect(title).toBeFocused();
  await page.getByLabel('Description', { exact: true }).fill('Saved on leaving the field');
  await page.getByLabel('Add tag').fill('finished-tag');
  await expect.poll(() => state.items.find(i => i.id === '2')?.description).toBe('Saved on leaving the field');
  await page.waitForTimeout(650);
  expect(state.items.find(i => i.id === '2')?.tags).not.toContain('finished-tag');
  await page.getByRole('button', { name: 'Close details', exact: true }).click();
  await expect(page.locator('.detail')).toHaveCount(0);
  expect(state.items.find(i => i.id === '2')?.tags).toContain('finished-tag');
});

test('autosave queues edits during a slow request and waits before switching tickets', async ({ page, context }) => {
  const state = await mock(context); await signIn(page);
  let release!: () => void;
  const held = new Promise<void>(resolve => release = resolve);
  state.beforeWrite = () => held;
  // Exercise an acknowledgement arriving on the live stream before its HTTP response.
  state.beforeReply = saved => state.notify('change', { items: [{ ...saved }] });
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
  expect(state.items.find(i => i.id === '2')).toMatchObject({ title: 'Latest title', description: 'Typed during the request', tags: ['frontend', 'queued-tag'], version: 3 });
  expect(state.bodies.map(body => body.version)).toEqual([1, 2]);
  await expect(page.getByText('Updated by someone else')).toHaveCount(0);
});

test('invalid titles and failed automatic saves keep the panel open until resolved', async ({ page, context }) => {
  const state = await mock(context); await signIn(page);
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
  await expect(page.getByText('All changes saved', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Close details', exact: true }).click();
  await expect(page.locator('.detail')).toHaveCount(0);
});

test('a conflict before the stream update fetches the latest version for explicit reconciliation', async ({ page, context }) => {
  const state = await mock(context); await signIn(page);
  await page.locator('#ticket-2').click();
  await expect(page.getByLabel('Ticket title')).toBeFocused();
  const remote = state.items.find(i => i.id === '2')!;
  remote.description = 'Remote description'; remote.version++;
  await page.getByLabel('Ticket title').fill('My title');
  await expect(page.getByText('Updated by someone else')).toBeVisible();
  expect(state.writes).toBe(1);
  await expect(page.getByLabel('Ticket title')).toHaveValue('My title');
  await page.getByRole('button', { name: 'Keep my edits on latest', exact: true }).click();
  await expect(page.getByText('All changes saved', { exact: true })).toBeVisible();
  expect(remote).toMatchObject({ title: 'My title', description: 'Remote description', version: 3 });
});
