import { defineConfig } from '@playwright/test';

const port = Number(process.env.PLAYWRIGHT_PORT || 5173);

export default defineConfig({
  testDir: './tests',
  timeout: 20000,
  fullyParallel: true,
  workers: 2,
  use: {
    baseURL: `http://127.0.0.1:${port}`,
    viewport: { width: 1512, height: 900 },
    launchOptions: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE } : {},
    screenshot: 'only-on-failure',
  },
  webServer: { command: `npm run dev -- --mode test --port ${port}`, url: `http://127.0.0.1:${port}`, reuseExistingServer: !process.env.CI },
});
