import { test, expect, type BrowserContext } from '@playwright/test';
import { statuses } from '../src/api';

async function mock(context: BrowserContext) {
  const state = {
    user: { id: '1', name: 'Alex Morgan', email: 'alex@example.test', role: 'viewer' },
    failProfile: false,
    expired: false,
    logout: 0,
    profiles: [] as { name: string }[],
    passwords: [] as { current_password: string; new_password: string }[],
    beforePasswordReply: null as (() => Promise<unknown>) | null,
  };
  await context.addInitScript(() => sessionStorage.setItem('tiki.session', 'test-session'));
  await context.route('**/api/v1/**', async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname.replace('/api/v1', '');
    const reply = (data: unknown, status = 200) =>
      route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(data) });
    if (path === '/auth/login') {
      state.expired = false;
      return reply({ user: state.user, session_token: 'new-test-session', expires_at: 9999999999 });
    }
    if (state.expired)
      return reply({ error: { code: 'unauthorized', message: 'Session expired' } }, 401);
    if (path === '/auth/me' && request.method() === 'PATCH') {
      state.profiles.push(request.postDataJSON());
      if (state.failProfile)
        return reply({ error: { code: 'internal', message: 'Could not save profile' } }, 500);
      state.user.name = request.postDataJSON().name;
      return reply(state.user);
    }
    if (path === '/auth/me') return reply(state.user);
    if (path === '/auth/password') {
      const body = request.postDataJSON();
      state.passwords.push(body);
      if (body.current_password !== 'correct-password')
        return reply(
          { error: { code: 'validation', message: 'current password is incorrect' } },
          400,
        );
      await state.beforePasswordReply?.();
      state.expired = true;
      return reply({ password_changed: true });
    }
    if (path === '/auth/logout') {
      state.logout++;
      state.expired = true;
      return reply({ logged_out: true });
    }
    if (path === '/board')
      return reply({
        columns: Object.fromEntries(statuses.map((status) => [status, { items: [] }])),
        users: { users: [state.user] },
        tags: { tags: [] },
      });
    if (path === '/events')
      return route.fulfill({
        contentType: 'text/event-stream',
        body: 'event: ready\ndata: {"reset":true,"users":true}\n\n',
      });
    return reply({ error: { code: 'not_found', message: 'Not found' } }, 404);
  });
  return state;
}

test('account menu supports arrows and vim keys, and profile saves persist for viewers', async ({
  page,
  context,
}) => {
  const state = await mock(context);
  await page.goto('/');
  const trigger = page.getByRole('button', { name: 'Account menu', exact: true });
  await trigger.click();
  await expect(page.getByRole('button', { name: 'Edit profile', exact: true })).toBeFocused();
  await page.keyboard.press('ArrowDown');
  await expect(page.getByRole('button', { name: 'Change password', exact: true })).toBeFocused();
  await page.keyboard.press('j');
  await expect(page.getByRole('button', { name: 'Sign out', exact: true })).toBeFocused();
  await page.keyboard.press('Home');
  await page.keyboard.press('Enter');
  const dialog = page.getByRole('dialog', { name: 'Edit profile', exact: true });
  await expect(dialog.getByLabel('Name', { exact: true })).toBeFocused();
  await expect(dialog).toContainText('alex@example.test');
  await expect(dialog.locator('input')).toHaveCount(1);
  await dialog.getByLabel('Name', { exact: true }).fill('   ');
  await expect(dialog.getByRole('button', { name: 'Save name' })).toBeDisabled();
  await dialog.getByLabel('Name', { exact: true }).fill('  Alex Rivera  ');
  await dialog.getByRole('button', { name: 'Save name' }).click();
  await expect(dialog.getByRole('status')).toHaveText('Name updated.');
  expect(state.profiles).toEqual([{ name: 'Alex Rivera' }]);
  await page.keyboard.press('Escape');
  await expect(trigger).toBeFocused();
  await expect(trigger).toHaveAttribute('title', 'Alex Rivera');
  await page.reload();
  await trigger.click();
  await expect(page.getByRole('dialog', { name: 'Your account' })).toContainText('Alex Rivera');
});

test('profile errors preserve the name draft and permit retry', async ({ page, context }) => {
  const state = await mock(context);
  state.failProfile = true;
  await page.goto('/');
  await page.getByRole('button', { name: 'Account menu' }).click();
  await page.getByRole('button', { name: 'Edit profile', exact: true }).click();
  await page.getByLabel('Name', { exact: true }).fill('Keep this name');
  await page.getByRole('button', { name: 'Save name' }).click();
  await expect(page.getByRole('alert')).toHaveText('Could not save profile');
  await expect(page.getByLabel('Name', { exact: true })).toHaveValue('Keep this name');
  state.failProfile = false;
  await page.getByRole('button', { name: 'Save name' }).click();
  await expect(page.getByRole('dialog', { name: 'Edit profile' }).getByRole('status')).toHaveText(
    'Name updated.',
  );
});

test('password form validates confirmation, keeps incorrect-password errors local, then signs out', async ({
  page,
  context,
}) => {
  const state = await mock(context);
  await page.goto('/');
  await page.getByRole('button', { name: 'Account menu' }).click();
  await page.getByRole('button', { name: 'Change password', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Change password', exact: true });
  await expect(dialog.getByLabel('Current password', { exact: true })).toBeFocused();
  await dialog.getByLabel('Current password', { exact: true }).fill('wrong-password');
  await dialog.getByLabel('New password', { exact: true }).fill('replacement-password');
  await dialog.getByLabel('Confirm new password', { exact: true }).fill('different-password');
  await dialog.getByRole('button', { name: 'Change password', exact: true }).click();
  await expect(dialog.getByRole('alert')).toHaveText('New passwords do not match.');
  expect(state.passwords).toHaveLength(0);
  await dialog.getByLabel('Confirm new password', { exact: true }).fill('replacement-password');
  await dialog.getByRole('button', { name: 'Change password', exact: true }).click();
  await expect(dialog.getByRole('alert')).toHaveText('current password is incorrect');
  await expect(page.getByRole('button', { name: 'Sign in', exact: true })).toHaveCount(0);
  await dialog.getByLabel('Current password', { exact: true }).fill('correct-password');
  // The live stream may revoke the session before the successful HTTP response arrives.
  state.beforePasswordReply = () =>
    page.evaluate(() => window.dispatchEvent(new Event('tiki:expired')));
  await dialog.getByRole('button', { name: 'Change password', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Sign in', exact: true })).toBeVisible();
  await expect(
    page.getByText('Password changed. Sign in again with your new password.'),
  ).toBeVisible();
  await expect(page.getByLabel('Password', { exact: true })).toHaveValue('');
  await expect(page.getByLabel('Password', { exact: true })).toBeFocused();
  await expect(page.getByLabel('Email', { exact: true })).toHaveValue('alex@example.test');
  expect(await page.evaluate(() => sessionStorage.getItem('tiki.session'))).toBeNull();
  expect(state.passwords.at(-1)).toEqual({
    current_password: 'correct-password',
    new_password: 'replacement-password',
  });
});

test('account actions fit mobile and sign out clears the session', async ({ page, context }) => {
  const state = await mock(context);
  await page.setViewportSize({ width: 390, height: 740 });
  await page.goto('/');
  const trigger = page.getByRole('button', { name: 'Account menu' });
  await expect(trigger).toBeInViewport();
  await trigger.click();
  await expect(page.getByRole('dialog', { name: 'Your account' })).toBeInViewport();
  await page.getByRole('button', { name: 'Change password', exact: true }).click();
  await expect(page.getByRole('dialog', { name: 'Change password' })).toBeInViewport();
  await page.getByRole('button', { name: 'Back to account menu' }).click();
  await page.getByRole('button', { name: 'Sign out', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Sign in', exact: true })).toBeVisible();
  expect(state.logout).toBe(1);
  expect(await page.evaluate(() => sessionStorage.getItem('tiki.session'))).toBeNull();
});

test('expired sessions close account settings and request sign-in', async ({ page, context }) => {
  const state = await mock(context);
  await page.goto('/');
  await page.getByRole('button', { name: 'Account menu' }).click();
  await page.getByRole('button', { name: 'Edit profile', exact: true }).click();
  await page.getByLabel('Name', { exact: true }).fill('New name');
  state.expired = true;
  await page.getByRole('button', { name: 'Save name' }).click();
  await expect(page.getByRole('dialog')).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Sign in', exact: true })).toBeVisible();
});
