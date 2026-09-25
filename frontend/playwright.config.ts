import { defineConfig } from '@playwright/test';

const port = Number(process.env.PLAYWRIGHT_PORT || 5174);
const apiPort = port + 1;
const apiURL = `http://127.0.0.1:${apiPort}`;

export default defineConfig({
  testDir: './tests',
  timeout: 20000,
  fullyParallel: true,
  workers: 2,
  use: {
    baseURL: `http://127.0.0.1:${port}`,
    viewport: { width: 1512, height: 900 },
    launchOptions: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE
      ? { executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE }
      : {},
    screenshot: 'only-on-failure',
  },
  webServer: [
    {
      command: 'node tests/server.mjs',
      url: `${apiURL}/readyz`,
      env: { TIKI_TEST_API_PORT: String(apiPort) },
      timeout: 120000,
      gracefulShutdown: { signal: 'SIGTERM', timeout: 10000 },
    },
    {
      command: `npm run dev -- --mode test --port ${port}`,
      url: `http://127.0.0.1:${port}`,
      env: { TIKI_API_URL: apiURL },
    },
  ],
});
