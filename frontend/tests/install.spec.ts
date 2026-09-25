import { test, expect, type BrowserContext } from '@playwright/test';

async function mock(context: BrowserContext, platforms: string[]) {
  const user = { id: '1', name: 'Viewer', email: "o'hara@example.test", role: 'viewer' };
  const requests: string[] = [];
  await context.addInitScript(() =>
    sessionStorage.setItem('tiki.session', 'private-session-never-copy'),
  );
  await context.route('**/api/v1/**', async (route) => {
    const path = new URL(route.request().url()).pathname.replace('/api/v1', '');
    requests.push(path);
    const reply = (data: unknown) =>
      route.fulfill({ contentType: 'application/json', body: JSON.stringify(data) });
    if (path === '/auth/me') return reply(user);
    if (path === '/board')
      return reply({
        columns: Object.fromEntries(
          ['backlog', 'todo', 'in_progress', 'code_review', 'blocked', 'complete', 'void'].map(
            (status) => [status, { items: [] }],
          ),
        ),
        users: { users: [user] },
        tags: { tags: [] },
      });
    if (path === '/cli') return reply({ platforms });
    if (path === '/events')
      return route.fulfill({
        contentType: 'text/event-stream',
        body: 'event: ready\ndata: {"reset":true,"users":true}\n\n',
      });
    return route.fulfill({ status: 404, contentType: 'application/json', body: '{}' });
  });
  return requests;
}

test('CLI and skill setup copy workspace commands without exposing a session', async ({
  page,
  context,
}, testInfo) => {
  const requests = await mock(context, [
    'linux-amd64',
    'linux-arm64',
    'darwin-amd64',
    'darwin-arm64',
  ]);
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  await page.goto('/');
  await page.emulateMedia({ colorScheme: 'dark' });
  const trigger = page.getByRole('button', { name: 'CLI & agents', exact: true });
  await expect(trigger).toBeVisible();
  expect(requests).not.toContain('/cli');
  await trigger.click();
  const dialog = page.getByRole('dialog', { name: 'CLI & agents', exact: true });
  await dialog.getByRole('button', { name: 'Copy CLI install command', exact: true }).click();
  const command = await page.evaluate(() => navigator.clipboard.readText());
  const origin = new URL(page.url()).origin;
  expect(command).toBe(`curl -fsS '${origin}/api/v1/cli/install.sh' | sh -s -- '${origin}'`);
  expect(command).not.toContain('private-session');
  await expect(dialog.getByRole('link', { name: 'View install script' })).toHaveAttribute(
    'href',
    '/api/v1/cli/install.sh',
  );
  await expect(dialog).toContainText('macOS Apple Silicon');
  await dialog.getByRole('button', { name: 'Copy sign-in command' }).click();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(
    `tiki auth login --server '${origin}' --email 'o'\\''hara@example.test'`,
  );
  await page.screenshot({
    path: testInfo.outputPath('setup-cli-desktop.png'),
    animations: 'disabled',
  });
  await dialog.getByRole('tab', { name: 'CLI', exact: true }).focus();
  await page.keyboard.press('ArrowRight');
  await expect(dialog.getByRole('tab', { name: 'Agent skill', exact: true })).toBeFocused();
  await expect(dialog.getByRole('tab', { name: 'Agent skill', exact: true })).toHaveAttribute(
    'aria-selected',
    'true',
  );
  await expect(dialog.getByRole('tabpanel', { name: 'CLI', exact: true })).toBeHidden();
  await dialog.getByRole('button', { name: 'Copy Skill install command', exact: true }).click();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(`${command} --skill`);
  await expect(dialog.getByRole('link', { name: 'Read SKILL.md' })).toHaveAttribute(
    'href',
    '/api/v1/skills/tiki/SKILL.md',
  );
  await page.screenshot({
    path: testInfo.outputPath('setup-skill-desktop.png'),
    animations: 'disabled',
  });
  await page.keyboard.press('Escape');
  await expect(dialog).toHaveCount(0);
  await expect(trigger).toBeFocused();
});

test('mobile setup keeps the skill available without CLI binaries', async ({
  page,
  context,
}, testInfo) => {
  await mock(context, []);
  await page.setViewportSize({ width: 390, height: 740 });
  await page.goto('/');
  await page.getByRole('button', { name: 'Commands', exact: true }).click();
  await page.getByLabel('Find a command').fill('install');
  await page.getByRole('option', { name: 'Install CLI & agent skill', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'CLI & agents', exact: true });
  await expect(dialog).toContainText('CLI downloads are not available');
  await expect(dialog.getByRole('button', { name: 'Copy CLI install command' })).toHaveCount(0);
  await dialog.getByRole('tab', { name: 'Agent skill' }).click();
  await expect(dialog.getByRole('button', { name: 'Copy skill install command' })).toBeVisible();
  await expect(
    dialog.getByRole('textbox', { name: 'Skill install command', exact: true }),
  ).toHaveValue(/ --skill$/);
  expect(await dialog.evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
  await page.screenshot({
    path: testInfo.outputPath('setup-skill-mobile.png'),
    animations: 'disabled',
  });
  await dialog.getByRole('button', { name: 'Set up CLI', exact: true }).click();
  await expect(dialog.getByRole('tab', { name: 'CLI', exact: true })).toBeFocused();
  await expect(dialog).toContainText('CLI downloads are not available');
});

test('clipboard failure leaves commands available for manual copying', async ({
  page,
  context,
}) => {
  await mock(context, ['linux-amd64']);
  await context.addInitScript(() =>
    Object.defineProperty(navigator, 'clipboard', {
      value: {
        writeText: async () => {
          throw new Error('denied');
        },
      },
    }),
  );
  await page.goto('/');
  await page.getByRole('button', { name: 'CLI & agents', exact: true }).click();
  const dialog = page.getByRole('dialog', { name: 'CLI & agents', exact: true });
  await dialog.getByRole('button', { name: 'Copy CLI install command' }).click();
  await expect(dialog.getByRole('alert')).toHaveText(
    'Clipboard unavailable. Select the command and copy it manually.',
  );
  const command = dialog.getByRole('textbox', { name: 'CLI install command', exact: true });
  await expect(command).toBeVisible();
  await command.click();
  expect(
    await command.evaluate(
      (element: HTMLTextAreaElement) => element.selectionEnd - element.selectionStart,
    ),
  ).toBeGreaterThan(20);
  await dialog.getByRole('tab', { name: 'Agent skill' }).click();
  await expect(dialog.getByRole('alert')).toHaveCount(0);
});
