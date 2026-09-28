import { expect, test } from "@playwright/test";

test("home route renders under the pages base", async ({ page }) => {
  const response = await page.goto("./");
  expect(response?.status()).toBe(200);
  await expect(page).toHaveTitle(/sum/);
});
