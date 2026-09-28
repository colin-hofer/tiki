import { test, expect } from '@playwright/test';
import type { APIRequestContext, Page } from '@playwright/test';
import type { Activity, ActivityPage, Item, Session } from '../src/api';

let session: Session;
let headers: Record<string, string>;
test.beforeAll(async ({ request }) => {
  const login = await request.post('/api/v1/auth/login', {
    data: { email: 'browser@example.test', password: 'browser-test-password' },
  });
  expect(login.ok()).toBeTruthy();
  session = await login.json();
  headers = { Authorization: `Bearer ${session.session_token}` };
});
test.beforeEach(async ({ context }) => {
  await context.addInitScript(
    (token) => sessionStorage.setItem('tiki.session', token),
    session.session_token,
  );
});
async function create(request: APIRequestContext) {
  const response = await request.post('/api/v1/items', {
    headers,
    data: {
      title: 'Discuss the release',
      description: 'Coordinate the release here.',
      status: 'todo',
    },
  });
  expect(response.ok()).toBeTruthy();
  return (await response.json()) as Item;
}
async function post(request: APIRequestContext, item: Item, body: string) {
  const response = await request.post(`/api/v1/items/${item.id}/comments`, {
    headers,
    data: { body, client_id: crypto.randomUUID() },
  });
  expect(response.ok()).toBeTruthy();
  return (await response.json()) as Activity;
}
async function history(request: APIRequestContext, item: Item) {
  const response = await request.get(`/api/v1/items/${item.id}/activity?limit=200`, { headers });
  expect(response.ok()).toBeTruthy();
  return (await response.json()) as ActivityPage;
}
async function open(page: Page, item: Item) {
  await page.goto(`/?item=${item.id}`);
  await expect(page.getByLabel('Comment', { exact: true })).toBeVisible();
  await expect(page.getByText('Loading activity…')).toHaveCount(0);
}

test('ticket changes and comments share one ordered live timeline', async ({ page, request }) => {
  const item = await create(request);
  await post(request, item, 'Starting work');
  const moved = await request.patch(`/api/v1/items/${item.id}`, {
    headers,
    data: { version: item.version, status: 'in_progress' },
  });
  expect(moved.ok()).toBeTruthy();
  const updated = (await moved.json()) as Item;
  await post(request, item, 'Implementation ready');
  await post(request, item, 'Tests pass too');
  const reads: string[] = [];
  page.on('request', (read) => {
    if (read.method() === 'GET' && /\/(activity|comments)(\?|$)/.test(read.url()))
      reads.push(read.url());
  });
  await open(page, item);
  await expect(page.locator('.connection')).toHaveText('Live');
  const rows = page.locator('.comment-log > .comment, .comment-log > .timeline-event');
  await expect(rows).toHaveCount(5);
  await expect(rows.nth(0)).toContainText('created this ticket');
  await expect(rows.nth(1)).toContainText('Starting work');
  await expect(rows.nth(2)).toContainText('moved to In progress');
  await expect(rows.nth(3)).toContainText('Implementation ready');
  await expect(rows.nth(4)).toContainText('Tests pass too');
  await expect(rows.nth(1)).toHaveClass(/group-end/);
  await expect(rows.nth(3)).toHaveClass(/group-start/);
  await expect(rows.nth(3)).not.toHaveClass(/group-end/);
  await expect(rows.nth(4)).not.toHaveClass(/group-start/);
  await expect(page.getByRole('button', { name: 'Activity', exact: true })).toHaveCount(0);
  const historyReads = [...reads];
  expect(historyReads.length).toBeGreaterThan(0);
  expect(historyReads.every((url) => url.includes('/activity?'))).toBeTruthy();

  const edited = await request.patch(`/api/v1/items/${item.id}`, {
    headers,
    data: { version: updated.version, title: 'Ready for review' },
  });
  expect(edited.ok()).toBeTruthy();
  await post(request, item, 'Please take a look');
  await expect(rows).toHaveCount(7);
  await expect(rows.nth(5)).toContainText('changed the title');
  await expect(rows.nth(6)).toContainText('Please take a look');
  await expect(page.getByLabel('Ticket title', { exact: true })).toHaveValue('Ready for review');
  expect(reads).toEqual(historyReads);
});

test('comments arrive in two clients, reconcile stream before POST, and leave the editor independent', async ({
  page,
  context,
  request,
}) => {
  const item = await create(request);
  await open(page, item);
  const second = await context.newPage();
  await open(second, item);
  await expect(second.locator('.connection')).toHaveText('Live');
  let release!: () => void;
  const held = new Promise<void>((resolve) => (release = resolve));
  await page.route(`**/items/${item.id}/comments`, async (route) => {
    if (route.request().method() !== 'POST') return route.fallback();
    const response = await route.fetch();
    await held;
    await route.fulfill({ response });
  });
  // An invalid ticket title must not prevent the separate comment form sending.
  await page.getByLabel('Ticket title', { exact: true }).fill('');
  const composer = page.getByLabel('Comment', { exact: true });
  await composer.fill('Ready for release');
  await composer.press('Shift+Enter');
  await composer.press('End');
  await composer.type('<script>plain text</script>');
  const sent = page.waitForResponse(
    (response) => response.request().method() === 'POST' && response.url().endsWith('/comments'),
  );
  await composer.press('Enter');
  try {
    await expect(second.locator('.comment-content p')).toHaveText([
      'Ready for release\n<script>plain text</script>',
    ]);
    await expect(page.locator('.comment')).toHaveCount(1);
    await expect(page.getByText('Sending…', { exact: true })).toHaveCount(0);
    await expect(composer).toHaveValue('');
    await expect(page.getByLabel('Ticket title', { exact: true })).toHaveValue('');
    await expect(page.getByText('Updated by someone else', { exact: true })).toHaveCount(0);
  } finally {
    release();
  }
  await sent;
  await expect(page.locator('.comment')).toHaveCount(1);
  const unchanged = (await (
    await request.get(`/api/v1/items/${item.id}`, { headers })
  ).json()) as Item;
  expect(unchanged.version).toBe(item.version);
  expect(
    (await history(request, item)).activity.filter((event) => event.kind === 'comment.created'),
  ).toHaveLength(1);
});

test('uncertain sends survive reload and retry with the same message ID', async ({
  page,
  request,
}) => {
  const item = await create(request);
  await page.route('**/api/v1/events', (route) => route.fulfill({ status: 503, body: '' }));
  await open(page, item);
  await page.route(`**/items/${item.id}/comments`, async (route) => {
    if (route.request().method() !== 'POST') return route.fallback();
    await route.fetch(); // The write committed, but its acknowledgement was lost.
    await route.abort('failed');
  });
  await page.getByLabel('Comment', { exact: true }).fill('Only one copy');
  await page.getByRole('button', { name: 'Send', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Retry sending' })).toBeVisible();
  expect(
    (await history(request, item)).activity.filter((event) => event.kind === 'comment.created'),
  ).toHaveLength(1);
  // History is temporarily unavailable too, leaving the restored send uncertain.
  await page.route(`**/items/${item.id}/activity?*`, (route) =>
    route.fulfill({
      status: 503,
      contentType: 'application/json',
      body: JSON.stringify({ error: { code: 'unavailable', message: 'History unavailable' } }),
    }),
  );
  await page.reload();
  await expect(page.getByRole('button', { name: 'Retry sending' })).toBeVisible();
  await page.unroute(`**/items/${item.id}/comments`);
  await page.getByRole('button', { name: 'Retry sending' }).click();
  await expect(page.getByRole('button', { name: 'Retry sending' })).toHaveCount(0);
  await expect(page.locator('.comment-content p')).toHaveText(['Only one copy']);
  expect(
    (await history(request, item)).activity.filter((event) => event.kind === 'comment.created'),
  ).toHaveLength(1);
});

test('drafts survive ticket navigation and reload without being sent', async ({
  page,
  request,
}) => {
  const first = await create(request);
  const second = await create(request);
  await open(page, first);
  const composer = page.getByLabel('Comment', { exact: true });
  await composer.fill('A draft to keep');
  await page.getByRole('button', { name: 'Close details', exact: true }).click();
  await page.locator(`#ticket-${second.id}`).click();
  await expect(composer).toHaveValue('');
  await composer.fill('A different draft');
  await page.getByRole('button', { name: 'Close details', exact: true }).click();
  await page.locator(`#ticket-${first.id}`).click();
  await expect(composer).toHaveValue('A draft to keep');
  await page.reload();
  await expect(composer).toHaveValue('A draft to keep');
  expect(
    (await history(request, first)).activity.filter((event) => event.kind === 'comment.created'),
  ).toHaveLength(0);
  expect(
    (await history(request, second)).activity.filter((event) => event.kind === 'comment.created'),
  ).toHaveLength(0);
});

test('history loads backward, incoming messages preserve scroll, and paused tabs catch up', async ({
  page,
  request,
}) => {
  const item = await create(request);
  for (let i = 1; i <= 55; i++) await post(request, item, `Message ${i}`);
  await open(page, item);
  await expect(page.locator('.comment')).toHaveCount(50);
  await expect(page.locator('.comment-content p').first()).toHaveText('Message 6');
  await page.getByRole('button', { name: 'Load older events' }).click();
  await expect(page.locator('.comment')).toHaveCount(55);
  await expect(page.locator('.comment-content p').first()).toHaveText('Message 1');
  await page.locator('.detail-content').evaluate((node) => {
    node.scrollTop = 0;
    node.dispatchEvent(new Event('scroll'));
  });
  await post(request, item, 'Arrived while reading');
  await expect(page.getByRole('button', { name: 'New events' })).toBeVisible();
  expect(await page.locator('.detail-content').evaluate((node) => node.scrollTop)).toBe(0);
  await page.getByRole('button', { name: 'New events' }).click();
  await expect(page.locator('.comment-content p').last()).toBeInViewport();
  await page.evaluate(() => {
    Object.defineProperty(document, 'hidden', { configurable: true, value: true });
    document.dispatchEvent(new Event('visibilitychange'));
  });
  await expect(page.locator('.connection')).toHaveText('Paused');
  for (let i = 0; i < 52; i++) await post(request, item, `Missed ${i}`);
  await page.evaluate(() => {
    Object.defineProperty(document, 'hidden', { configurable: true, value: false });
    document.dispatchEvent(new Event('visibilitychange'));
  });
  await expect(page.locator('.connection')).toHaveText('Live');
  await expect(page.locator('.comment')).toHaveCount(108);
  await expect(page.locator('.comment-content p').last()).toHaveText('Missed 51');
});

test('history catch-up never skips messages when a newer live message arrives first', async ({
  page,
}) => {
  await page.goto('/');
  const result = await page.evaluate(async () => {
    const { TimelineState } = await import('../src/timeline.svelte.ts');
    const fetch = window.fetch;
    const comment = (id: string) => ({
      id,
      item_id: '90001',
      actor_id: '2',
      kind: 'comment.created',
      data: { body: id },
      created_at: new Date().toISOString(),
      client_id: id,
    });
    let initial = true;
    let release!: () => void;
    const held = new Promise<void>((resolve) => (release = resolve));
    const cursors: string[] = [];
    window.fetch = async (input, init) => {
      if (!String(input).startsWith('/api/v1/items/90001/activity')) return fetch(input, init);
      const after = new URL(String(input), location.origin).searchParams.get('after') || '';
      if (initial) return Response.json({ activity: [comment('1')] });
      cursors.push(after);
      if (after === '1') {
        await held;
        return Response.json({ activity: [comment('2')], next_after: '2' });
      }
      return Response.json({ activity: [comment('3')] });
    };
    const conversation = new TimelineState('1', () => {});
    try {
      conversation.open('90001');
      await conversation.sync();
      initial = false;
      const catchingUp = conversation.sync();
      conversation.accept([comment('3')]);
      release();
      await catchingUp;
      return { ids: conversation.events.map((c) => c.id), cursors };
    } finally {
      release();
      conversation.stop();
      window.fetch = fetch;
    }
  });
  expect(result).toEqual({ ids: ['1', '2', '3'], cursors: ['1', '2'] });
});
