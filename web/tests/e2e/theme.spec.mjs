import { expect, test } from "@playwright/test";

const htmlBg = (page) =>
  page.evaluate(
    () => getComputedStyle(document.documentElement).backgroundColor,
  );

test("follows the OS palette with no override", async ({ page }) => {
  await page.emulateMedia({ colorScheme: "dark" });
  await page.goto("./");
  await expect(page.locator("html")).not.toHaveAttribute("data-theme", /.*/);
  expect(await htmlBg(page)).toBe("rgb(19, 20, 21)");
  const toggle = page.locator("#theme-toggle");
  await expect(toggle).toHaveText("light");
  await expect(toggle).toHaveAttribute("title", "Following system theme");
});

test("toggle writes an override that persists across reloads", async ({
  page,
}) => {
  await page.emulateMedia({ colorScheme: "dark" });
  await page.goto("./");
  const toggle = page.locator("#theme-toggle");
  await toggle.click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
  await expect(toggle).toHaveText("dark");
  await expect(toggle).toHaveAttribute(
    "title",
    "Theme override on; following light",
  );
  expect(await htmlBg(page)).toBe("rgb(244, 241, 234)");
  await page.reload();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "light");
});

test("a light-OS toggle pins the dark palette and survives reload", async ({
  page,
}) => {
  await page.emulateMedia({ colorScheme: "light" });
  await page.goto("./");
  const toggle = page.locator("#theme-toggle");
  await toggle.click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await expect(toggle).toHaveText("light");
  await expect(toggle).toHaveAttribute(
    "title",
    "Theme override on; following dark",
  );
  expect(await htmlBg(page)).toBe("rgb(19, 20, 21)");
  await page.reload();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  expect(await htmlBg(page)).toBe("rgb(19, 20, 21)");
});

test.describe("without JavaScript", () => {
  test.use({ javaScriptEnabled: false });

  test("content renders and the toggle stays hidden", async ({ page }) => {
    await page.emulateMedia({ colorScheme: "dark" });
    const response = await page.goto("./");
    expect(response?.status()).toBe(200);
    await expect(page.locator("#theme-toggle")).toBeHidden();
    expect(await htmlBg(page)).toBe("rgb(19, 20, 21)");
    await expect(page.locator(".topbar .brand")).toContainText("sum");
  });
});
