import { defineConfig, devices } from "@playwright/test";

const previewPort = process.env.PLAYWRIGHT_PORT ?? "4321";

export default defineConfig({
  testDir: "./tests/e2e",
  use: {
    ...devices["Desktop Chrome"],
    baseURL: `http://127.0.0.1:${previewPort}/sum/`
  },
  webServer: {
    command:
      `aube run build && ASTRO_PREVIEW_BACKGROUND=0 aube run preview -- --host 127.0.0.1 --port ${previewPort} --ignore-lock`,
    reuseExistingServer: !process.env.CI,
    timeout: 120000,
    url: `http://127.0.0.1:${previewPort}/sum/`
  }
});
