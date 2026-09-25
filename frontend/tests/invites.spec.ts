import { test, expect, type BrowserContext } from '@playwright/test';
import type { User } from '../src/api';

const token = 'a'.repeat(43);
const admin = { id: '1', name: 'Admin', email: 'admin@example.test', role: 'admin' };
const member = { id: '2', name: 'New Person', email: 'new@example.test', role: 'member' };

async function mock(context: BrowserContext) {
  const state = {
    valid: true,
    claims: 0,
    me: 0,
    role: '',
    user: admin,
    users: [{ ...admin }, { ...member }] as User[],
    failChanges: false,
  };
  await context.route('**/api/v1/**', async (route) => {
    const path = new URL(route.request().url()).pathname.replace('/api/v1', '');
    const body = route.request().postDataJSON();
    const reply = (value: unknown, status = 200) =>
      route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(value) });
    if (path === '/auth/me') {
      state.me++;
      return reply(state.user);
    }
    if (path === '/auth/invite')
      return state.valid
        ? reply({ role: 'member', expires_at: 9999999999 })
        : reply(
            {
              error: {
                code: 'not_found',
                message: 'This invite is invalid, expired, or already used; ask for a new link.',
              },
            },
            404,
          );
    if (path === '/auth/join') {
      state.claims++;
      expect(body).toEqual({
        token,
        name: 'New Person',
        email: 'new@example.test',
        password: 'new-password',
      });
      state.user = member;
      return reply({ user: member, session_token: 'new-session', expires_at: 9999999999 });
    }
    if (path === '/invites') {
      state.role = body.role;
      return reply({ id: '3', token, role: body.role, expires_at: 9999999999 });
    }
    if (path === '/invites/3') {
      expect(route.request().method()).toBe('DELETE');
      state.valid = false;
      return reply({ revoked: true });
    }
    if (path === '/users') return reply({ users: state.users });
    if (path.startsWith('/users/')) {
      if (state.failChanges)
        return reply({ error: { code: 'conflict', message: 'Could not save role' } }, 409);
      const user = state.users.find((u) => u.id === path.split('/')[2])!;
      if (route.request().method() === 'DELETE') user.removed_at = 1;
      else user.role = body.role;
      return reply(user);
    }
    if (path === '/board')
      return reply({ columns: {}, users: { users: state.users }, tags: { tags: [] } });
    if (path === '/events')
      return route.fulfill({
        contentType: 'text/event-stream',
        body: 'event: ready\ndata: {"reset":true,"users":true}\n\n',
      });
    return reply({ error: { code: 'not_found', message: 'Unknown route' } }, 404);
  });
  return state;
}

test('admin creates, copies, and revokes an invite without changing the board', async ({
  page,
  context,
}) => {
  const state = await mock(context);
  await context.addInitScript(() => sessionStorage.setItem('tiki.session', 'admin-session'));
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  await page.goto('/');
  const trigger = page.getByRole('button', { name: 'Manage people', exact: true });
  await trigger.click();
  const dialog = page.getByRole('dialog', { name: 'People', exact: true });
  await dialog.getByRole('button', { name: 'Invite people', exact: true }).click();
  await dialog.getByRole('combobox', { name: 'Invite role', exact: true }).click();
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

test('invite claim chooses credentials, signs in, and removes the secret from the URL', async ({
  page,
  context,
}) => {
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
  await expect(page.getByRole('button', { name: 'Manage people', exact: true })).toHaveCount(0);
});

test('an unusable invite never offers account creation, including on a phone', async ({
  page,
  context,
}) => {
  const state = await mock(context);
  state.valid = false;
  await page.setViewportSize({ width: 390, height: 680 });
  await page.goto('/#invite=invalid');
  await expect(page.getByRole('alert')).toContainText('ask for a new link');
  await expect(page.getByRole('button', { name: 'Create account', exact: true })).toHaveCount(0);
  expect(state.claims).toBe(0);
  // A different invite pasted into the same tab must replace the join screen.
  state.valid = true;
  await page.goto(`/#invite=${token}`);
  await expect(page.getByRole('button', { name: 'Create account', exact: true })).toBeVisible();
  await page.getByRole('link', { name: 'Already have an account? Sign in' }).click();
  await expect(page.getByRole('button', { name: 'Sign in', exact: true })).toBeVisible();
});

test('people management changes roles, handles failed saves, and removes access', async ({
  page,
  context,
}) => {
  const state = await mock(context);
  await context.addInitScript(() => sessionStorage.setItem('tiki.session', 'admin-session'));
  await page.goto('/');
  await page.getByRole('button', { name: 'Manage people', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'People', exact: true });
  await expect(dialog.getByText('new@example.test', { exact: true })).toBeVisible();
  await expect(
    dialog.getByRole('combobox', { name: 'Role for Admin', exact: true }),
  ).toBeDisabled();
  await expect(dialog.getByRole('button', { name: 'Remove Admin', exact: true })).toBeDisabled();
  const role = dialog.getByRole('combobox', { name: 'Role for New Person', exact: true });
  state.failChanges = true;
  await role.click();
  await page.getByRole('option', { name: 'Viewer', exact: true }).click();
  await expect(dialog.getByRole('alert')).toContainText('Could not save role');
  await expect(role).toContainText('Member');
  state.failChanges = false;
  await role.click();
  await page.getByRole('option', { name: 'Viewer', exact: true }).click();
  await expect(role).toContainText('Viewer');
  expect(state.users[1].role).toBe('viewer');
  await dialog.getByRole('button', { name: 'Remove New Person', exact: true }).click();
  await expect(dialog.getByText(/They will be signed out/)).toBeVisible();
  await dialog.getByRole('button', { name: 'Remove access', exact: true }).click();
  await expect(role).toHaveCount(0);
  await dialog.getByLabel('Show removed users').check();
  await expect(
    dialog.getByText('Removed · Invite again to restore', { exact: true }),
  ).toBeVisible();
  await dialog.getByLabel('Search people').fill('new@example.test');
  await expect(dialog.getByRole('listitem')).toHaveCount(1);
});
