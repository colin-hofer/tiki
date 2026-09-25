import { test, expect } from '@playwright/test';
import type { Item, Session } from '../src/api';

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
    data: { title: 'Move anchor', status: 'in_progress', type: 'task' },
  });
  expect(anchorResponse.ok()).toBeTruthy();
  const anchor: Item = await anchorResponse.json();
  await expect(page.locator(`#ticket-${anchor.id}`)).toBeVisible();

  await page.getByRole('button', { name: 'Add item to Todo', exact: true }).click();
  await page.getByLabel('New item title').fill('Created through the browser');
  const create = page.waitForResponse(
    (response) =>
      response.url().endsWith('/api/v1/items') && response.request().method() === 'POST',
  );
  await page.getByRole('button', { name: 'Add & edit', exact: true }).click();
  const item: Item = await (await create).json();
  await page.getByLabel('Ticket title', { exact: true }).fill('Saved through the browser');
  await expect(page.getByText('All changes saved', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'Close details', exact: true }).click();
  const before: Item = await (await request.get(`/api/v1/items/${item.id}`, { headers })).json();
  expect(before.title).toBe('Saved through the browser');
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
  expect(errors).toEqual([]);
});
