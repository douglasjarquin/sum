import { defineConfig, devices } from "@playwright/test";
import astroConfig from "./astro.config.mjs";

const previewPort = process.env.PLAYWRIGHT_PORT ?? "4321";
const base = `http://127.0.0.1:${previewPort}${astroConfig.base}/`;

export default defineConfig({
  testDir: "./tests/e2e",
  fullyParallel: true,
  use: {
    ...devices["Desktop Chrome"],
    baseURL: base
  },
  webServer: {
    // CI builds web/dist in a prior step; locally build on demand.
    command: process.env.CI
      ? `ASTRO_PREVIEW_BACKGROUND=0 aube run preview -- --host 127.0.0.1 --port ${previewPort} --ignore-lock`
      : `aube run build && ASTRO_PREVIEW_BACKGROUND=0 aube run preview -- --host 127.0.0.1 --port ${previewPort} --ignore-lock`,
    reuseExistingServer: !process.env.CI,
    timeout: 120000,
    url: base
  }
});
