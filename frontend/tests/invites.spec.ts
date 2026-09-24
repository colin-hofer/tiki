import { test, expect, type BrowserContext } from '@playwright/test';

const token = 'a'.repeat(43);
const admin = { id: '1', name: 'Admin', email: 'admin@example.test', role: 'admin' };
const member = { id: '2', name: 'New Person', email: 'new@example.test', role: 'member' };

async function mock(context: BrowserContext) {
  const state = { valid: true, claims: 0, me: 0, role: '', user: admin };
  await context.route('**/api/v1/**', async route => {
    const path = new URL(route.request().url()).pathname.replace('/api/v1', '');
    const body = route.request().postDataJSON();
    const reply = (value: unknown, status = 200) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(value) });
    if (path === '/auth/me') { state.me++; return reply(state.user); }
    if (path === '/auth/invite') return state.valid ? reply({ role: 'member', expires_at: 9999999999 }) : reply({ error: { code: 'not_found', message: 'This invite is invalid, expired, or already used; ask for a new link.' } }, 404);
    if (path === '/auth/join') {
      state.claims++;
      expect(body).toEqual({ token, name: 'New Person', email: 'new@example.test', password: 'new-password' });
      state.user = member;
      return reply({ user: member, session_token: 'new-session', expires_at: 9999999999 });
    }
    if (path === '/invites') { state.role = body.role; return reply({ id: '3', token, role: body.role, expires_at: 9999999999 }); }
    if (path === '/invites/3') { expect(route.request().method()).toBe('DELETE'); state.valid = false; return reply({ revoked: true }); }
    if (path === '/board') return reply({ columns: {}, users: { users: [state.user] }, tags: { tags: [] } });
    if (path === '/events') return route.fulfill({ contentType: 'text/event-stream', body: 'event: ready\ndata: {"reset":true,"users":true}\n\n' });
    return reply({ error: { code: 'not_found', message: 'Unknown route' } }, 404);
  });
  return state;
}

test('admin creates, copies, and revokes an invite without changing the board', async ({ page, context }) => {
  const state = await mock(context);
  await context.addInitScript(() => sessionStorage.setItem('tiki.session', 'admin-session'));
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  await page.goto('/');
  const trigger = page.getByRole('button', { name: 'Invite people', exact: true });
  await trigger.click();
  const dialog = page.getByRole('dialog', { name: 'Invite people' });
  await dialog.getByRole('combobox', { name: 'Role', exact: true }).click();
  await page.getByRole('option', { name: /^Viewer/ }).click();
  await dialog.getByRole('button', { name: 'Create invite link' }).click();
  const link = await dialog.getByLabel('Invite link', { exact: true }).inputValue();
  expect(link).toBe(`${new URL(page.url()).origin}/#invite=${token}`);
  expect(state.role).toBe('viewer');
  await dialog.getByRole('button', { name: 'Copy link' }).click();
  await expect(dialog.getByRole('button', { name: 'Copied' })).toBeVisible();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(link);
  await dialog.getByRole('button', { name: 'Revoke link' }).click();
  await expect(dialog.getByRole('button', { name: 'Create invite link' })).toBeVisible();
  expect(state.valid).toBe(false);
  await page.keyboard.press('Escape');
  await expect(dialog).toHaveCount(0);
  await expect(trigger).toBeFocused();
});

test('invite claim chooses credentials, signs in, and removes the secret from the URL', async ({ page, context }) => {
  const state = await mock(context);
  // A tab with an older login must still show the join screen, not restore it.
  await context.addInitScript(() => sessionStorage.setItem('tiki.session', 'old-session'));
  await page.goto(`/#invite=${token}`);
  await expect(page.getByText('Join your workspace', { exact: true })).toBeVisible();
  await page.getByLabel('Name', { exact: true }).fill('New Person');
  await page.getByLabel('Email', { exact: true }).fill('new@example.test');
  await page.getByLabel('Password', { exact: true }).fill('new-password');
  await page.getByLabel('Confirm password', { exact: true }).fill('wrong-password');
  await page.getByRole('button', { name: 'Create account', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('Passwords do not match');
  expect(state.claims).toBe(0);
  await page.getByLabel('Confirm password', { exact: true }).fill('new-password');
  await page.getByRole('button', { name: 'Create account', exact: true }).click();
  await expect(page.getByRole('main')).toBeVisible();
  expect(state.claims).toBe(1);
  expect(state.me).toBe(0);
  expect(new URL(page.url()).hash).toBe('');
  expect(await page.evaluate(() => sessionStorage.getItem('tiki.session'))).toBe('new-session');
  await expect(page.getByRole('button', { name: 'Invite people', exact: true })).toHaveCount(0);
});

test('an unusable invite never offers account creation, including on a phone', async ({ page, context }) => {
  const state = await mock(context); state.valid = false;
  await page.setViewportSize({ width: 390, height: 680 });
  await page.goto('/#invite=invalid');
  await expect(page.getByRole('alert')).toContainText('ask for a new link');
  await expect(page.getByRole('button', { name: 'Create account', exact: true })).toHaveCount(0);
  expect(state.claims).toBe(0);
  await page.getByRole('link', { name: 'Already have an account? Sign in' }).click();
  await expect(page.getByRole('button', { name: 'Sign in', exact: true })).toBeVisible();
});
