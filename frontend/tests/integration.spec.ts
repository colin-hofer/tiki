import { test, expect } from '@playwright/test';
import type { Item, Session } from '../src/api';

test('real Go API: search and paging reach every backlog ticket, including a new one', async ({
  page,
  context,
  request,
}) => {
  const login = await request.post('/api/v1/auth/login', {
    data: { email: 'browser@example.test', password: 'browser-test-password' },
  });
  expect(login.ok()).toBeTruthy();
  const session: Session = await login.json();
  const headers = { Authorization: `Bearer ${session.session_token}` };
  await context.addInitScript(
    (token) => sessionStorage.setItem('tiki.session', token),
    session.session_token,
  );
  const tag = `pagination-${crypto.randomUUID()}`;
  const description = 'Context. '.repeat(1000) + 'Hidden-description-needle';
  let last!: Item;
  for (let n = 0; n < 25; n++) {
    const response = await request.post('/api/v1/items', {
      headers,
      data: {
        title: n === 24 ? 'Buried search needle' : `Paged backlog ${n}`,
        description: n === 24 ? description : '',
        status: 'backlog',
        tags: [tag],
      },
    });
    expect(response.ok()).toBeTruthy();
    last = await response.json();
  }
  await page.goto(`/?tag=${tag}`);
  const column = page.locator('[data-column="backlog"]');
  await expect(column.locator('[data-ticket]')).toHaveCount(20);
  await page.getByLabel('Search tickets').fill('needle');
  await expect(column.locator('[data-ticket]')).toHaveCount(1);
  await expect(page.locator(`#ticket-${last.id}`)).toBeVisible();
  const searchResponse = page.waitForResponse(
    (response) => response.url().includes('/board?') && response.url().includes('query=HIDDEN'),
  );
  await page.getByLabel('Search tickets').fill('HIDDEN-description');
  const result = await (await searchResponse).json();
  expect(result.columns.backlog.items).toHaveLength(1);
  expect(result.columns.backlog.items[0].description).toBeUndefined();
  expect(result.columns.backlog.items[0].preview).not.toContain('Hidden-description');
  await expect(page.locator(`#ticket-${last.id}`)).toBeVisible();
  await page.reload();
  await expect(page.locator(`#ticket-${last.id}`)).toBeVisible();
  // Live updates use the full body, including when clearing it omits description in JSON.
  for (const body of ['', description]) {
    const response = await request.patch(`/api/v1/items/${last.id}`, {
      headers,
      data: { version: last.version, description: body },
    });
    expect(response.ok()).toBeTruthy();
    last = await response.json();
    await expect(column.locator('[data-ticket]')).toHaveCount(body ? 1 : 0);
  }
  await page.getByLabel('Search tickets').fill('');
  await expect(column.locator('[data-ticket]')).toHaveCount(20);
  await page.getByRole('button', { name: 'Add item to Backlog', exact: true }).click();
  await page.getByLabel('New item title').fill('Fresh backlog ticket');
  const saved = page.waitForResponse(
    (response) => response.request().method() === 'POST' && response.url().endsWith('/items'),
  );
  await page.getByLabel('New item title').press('Enter');
  const created: Item = await (await saved).json();
  expect(created.priority).toBeGreaterThan(last.priority);
  await expect(page.locator(`#ticket-${created.id}`)).toBeInViewport();
  await page.keyboard.press('Escape');
  await page.getByRole('button', { name: 'Refresh board', exact: true }).click();
  await expect(column.locator('[data-ticket]')).toHaveCount(21);
  await column.locator('.load-more').scrollIntoViewIfNeeded();
  await expect(column.locator('[data-ticket]')).toHaveCount(26);
  await expect(column.locator('[data-ticket]').last()).toHaveAttribute('data-ticket', created.id);
  await expect(column.locator('.load-more')).toHaveCount(0);
});

test('real Go API: sign in, autosave, atomic move, live update, and reload', async ({
  page,
  request,
}) => {
  const errors: string[] = [];
  page.on('pageerror', (error) => errors.push(error.message));
  const login = page.waitForResponse((response) => response.url().endsWith('/api/v1/auth/login'));
  await page.goto('/');
  await page.getByLabel('Email', { exact: true }).fill('browser@example.test');
  await page.getByLabel('Password', { exact: true }).fill('browser-test-password');
  await page.getByRole('button', { name: 'Sign in', exact: true }).click();
  const session: Session = await (await login).json();
  const headers = { Authorization: `Bearer ${session.session_token}` };
  await expect(page.locator('.connection')).toHaveText('Live');
  const anchorResponse = await request.post('/api/v1/items', {
    headers,
    data: {
      title: 'Move anchor',
      status: 'in_progress',
      type: 'task',
      url: 'https://example.com/doc',
    },
  });
  expect(anchorResponse.ok()).toBeTruthy();
  const anchor: Item = await anchorResponse.json();
  expect(anchor.url).toBe('https://example.com/doc');
  await expect(page.locator(`#ticket-${anchor.id}`)).toBeVisible();

  await page.getByRole('button', { name: 'Add item to Todo', exact: true }).click();
  await page.getByLabel('New item title').fill('Created through the browser');
  const create = page.waitForResponse(
    (response) =>
      response.url().endsWith('/api/v1/items') && response.request().method() === 'POST',
  );
  await page.getByRole('button', { name: 'Add & edit', exact: true }).click();
  const item: Item = await (await create).json();
  const link = 'https://github.com/colin-hofer/tiki/pull/3';
  await page.getByLabel('Link', { exact: true }).fill(link);
  await page.getByLabel('Ticket title', { exact: true }).fill('Saved through the browser');
  await expect(page.locator('.detail')).toHaveAttribute('data-save-state', 'saved');
  await page.getByRole('button', { name: 'Close details', exact: true }).click();
  const before: Item = await (await request.get(`/api/v1/items/${item.id}`, { headers })).json();
  expect(before.title).toBe('Saved through the browser');
  expect(before.url).toBe(link);
  await expect(page.locator(`#ticket-${item.id} .card-link`)).toHaveAttribute('title', link);
  const mutations: string[] = [];
  page.on('request', (outgoing) => {
    if (outgoing.method() !== 'GET')
      mutations.push(outgoing.method() + ' ' + new URL(outgoing.url()).pathname);
  });
  await page
    .locator(`#ticket-${item.id}`)
    .dragTo(page.locator(`#ticket-${anchor.id}`), { targetPosition: { x: 20, y: 5 } });
  await expect(
    page
      .getByRole('region', { name: 'In progress column', exact: true })
      .locator(`#ticket-${item.id}`),
  ).toBeVisible();
  const moved: Item = await (await request.get(`/api/v1/items/${item.id}`, { headers })).json();
  expect(moved).toMatchObject({ status: 'in_progress', version: before.version + 1 });
  expect(moved.priority).toBeLessThan(anchor.priority);
  expect(mutations).toEqual([`POST /api/v1/items/${item.id}/move`]);

  const remote = await request.patch(`/api/v1/items/${item.id}`, {
    headers,
    data: { version: moved.version, title: 'Updated by another client' },
  });
  expect(remote.ok()).toBeTruthy();
  await expect(page.locator(`#ticket-${item.id}`)).toContainText('Updated by another client');
  await page.reload();
  await expect(page.locator(`#ticket-${item.id}`)).toContainText('Updated by another client');
  await page.locator(`#ticket-${item.id}`).click();
  await expect(page.getByLabel('Link', { exact: true })).toHaveValue(link);
  await expect(page.getByRole('link', { name: 'colin-hofer/tiki#3' })).toHaveAttribute(
    'href',
    link,
  );
  await page.getByLabel('Link', { exact: true }).fill('');
  await expect(page.locator('.detail')).toHaveAttribute('data-save-state', 'saved');
  const cleared: Item = await (await request.get(`/api/v1/items/${item.id}`, { headers })).json();
  expect(cleared.url).toBeUndefined();
  await expect(page.locator(`#ticket-${item.id} .card-link`)).toHaveCount(0);
  expect(errors).toEqual([]);
});
