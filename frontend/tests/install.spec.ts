import { test, expect, type BrowserContext } from '@playwright/test';

async function mock(context: BrowserContext, platforms: string[]) {
  const user = { id: '1', name: 'Viewer', email: "o'hara@example.test", role: 'viewer' };
  const requests: string[] = [];
  await context.addInitScript(() => sessionStorage.setItem('tiki.session', 'private-session-never-copy'));
  await context.route('**/api/v1/**', async route => {
    const path = new URL(route.request().url()).pathname.replace('/api/v1', '');
    requests.push(path);
    const reply = (data: unknown) => route.fulfill({ contentType: 'application/json', body: JSON.stringify(data) });
    if (path === '/auth/me') return reply(user);
    if (path === '/board') return reply({ columns: {}, users: { users: [user] }, tags: { tags: [] } });
    if (path === '/cli') return reply({ platforms });
    if (path === '/events') return route.fulfill({ contentType: 'text/event-stream', body: 'event: ready\ndata: {"reset":true,"users":true}\n\n' });
    return route.fulfill({ status: 404, contentType: 'application/json', body: '{}' });
  });
  return requests;
}

test('any user can copy the install command for this workspace without exposing a session', async ({ page, context }) => {
  const requests = await mock(context, ['linux-amd64', 'linux-arm64', 'darwin-amd64', 'darwin-arm64']);
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  await page.goto('/');
  const trigger = page.getByRole('button', { name: 'Install CLI', exact: true });
  await expect(trigger).toBeVisible();
  expect(requests).not.toContain('/cli');
  await trigger.click();
  const dialog = page.getByRole('dialog', { name: 'Install the CLI', exact: true });
  await dialog.getByRole('button', { name: 'Copy install command', exact: true }).click();
  const command = await page.evaluate(() => navigator.clipboard.readText());
  const origin = new URL(page.url()).origin;
  expect(command).toBe(`curl -fsS '${origin}/api/v1/cli/install.sh' | sh -s -- '${origin}'`);
  expect(command).not.toContain('private-session');
  await expect(dialog.getByRole('link', { name: 'View the install script' })).toHaveAttribute('href', '/api/v1/cli/install.sh');
  await expect(dialog).toContainText('macOS Apple Silicon');
  await dialog.getByRole('button', { name: 'Copy sign-in command' }).click();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe("tiki auth login --email 'o'\\''hara@example.test'");
  await page.keyboard.press('Escape');
  await expect(dialog).toHaveCount(0);
  await expect(trigger).toBeFocused();
});

test('CLI setup is accessible through commands on mobile and explains missing downloads', async ({ page, context }) => {
  await mock(context, []);
  await page.setViewportSize({ width: 390, height: 740 });
  await page.goto('/');
  await page.getByRole('button', { name: 'Commands', exact: true }).click();
  await page.getByRole('option', { name: 'Install CLI', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'Install the CLI', exact: true });
  await expect(dialog).toContainText('CLI downloads are not available');
  await expect(dialog.getByRole('button', { name: 'Copy install command' })).toHaveCount(0);
});
