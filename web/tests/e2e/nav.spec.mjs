import { expect, test } from "@playwright/test";

test("topbar shows the tagline and the nav links", async ({ page }) => {
  await page.goto("docs/");
  const topbar = page.locator(".topbar");
  await expect(topbar).toContainText("Herdr-native agent distro");
  const hrefs = await topbar
    .locator("a")
    .evaluateAll((els) => els.map((el) => el.getAttribute("href")));
  expect(hrefs).toContain("/sum/");
  expect(hrefs).toContain("/sum/docs/");
  expect(hrefs).toContain("/sum/install/");
  expect(hrefs).toContain("https://github.com/douglasjarquin/sum");
});

test.describe("docs sidebar at 1280px", () => {
  test.use({ viewport: { width: 1280, height: 800 } });

  test("renders two labeled groups and marks the active entry", async ({
    page,
  }) => {
    await page.goto("install/");
    const side = page.locator(".side");
    const labels = side.locator(".lbl");
    await expect(labels).toHaveCount(2);
    await expect(labels.nth(0)).toHaveText("sum");
    await expect(labels.nth(1)).toHaveText("source");
    const active = side.locator("a.active");
    await expect(active).toHaveCount(1);
    await expect(active).toHaveText("Install");
    await expect(active).toHaveAttribute("aria-current", "page");
  });
});

test.describe("docs sidebar at 390px", () => {
  test.use({ viewport: { width: 390, height: 800 } });

  test("collapses to horizontal chips", async ({ page }) => {
    await page.goto("install/");
    const side = page.locator(".side");
    expect(
      await side.evaluate((el) => getComputedStyle(el).flexDirection),
    ).toBe("row");
    const link = side.locator("a").first();
    expect(await link.evaluate((el) => getComputedStyle(el).borderTopStyle))
      .toBe("solid");
    const active = side.locator("a.active");
    await expect(active).toHaveCount(1);
    await expect(active).toHaveAttribute("aria-current", "page");
  });
});
