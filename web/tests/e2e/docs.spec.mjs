import { expect, test } from "@playwright/test";

const pages = [
  { route: "docs/", h1: "Documentation" },
  { route: "install/", h1: "Install" },
  { route: "architecture/", h1: "Architecture" },
  { route: "verification/", h1: "Verification" },
  { route: "skills/", h1: "Skills" },
  { route: "configuration/", h1: "Configuration" },
  { route: "herdr/", h1: "Herdr backend" },
  { route: "update/", h1: "Update & rollback" },
];

for (const { route, h1 } of pages) {
  test(`${route} renders its heading`, async ({ page }) => {
    const response = await page.goto(route);
    expect(response?.status()).toBe(200);
    await expect(page.locator("h1")).toHaveText(h1);
  });

  test(`${route} marks its own sidebar entry active`, async ({ page }) => {
    await page.goto(route);
    const active = page.locator(".side a.active");
    await expect(active).toHaveCount(1);
    await expect(active).toHaveAttribute("href", `/sum/${route}`);
    await expect(active).toHaveAttribute("aria-current", "page");
  });

  test(`${route} keeps internal links under the /sum/ base`, async ({
    page,
  }) => {
    await page.goto(route);
    const hrefs = await page
      .locator('a[href^="/"]')
      .evaluateAll((els) => els.map((el) => el.getAttribute("href")));
    for (const href of hrefs) {
      expect(href).toMatch(/^\/sum\//);
    }
  });
}

test("docs index links the seven sections and the docs/*.md sources", async ({
  page,
}) => {
  const response = await page.goto("docs/");
  expect(response?.status()).toBe(200);
  const cards = page.locator(".cells a");
  await expect(cards).toHaveCount(7);
  const hrefs = await cards.evaluateAll((els) =>
    els.map((el) => el.getAttribute("href")),
  );
  expect(hrefs).toEqual([
    "/sum/install/",
    "/sum/architecture/",
    "/sum/verification/",
    "/sum/skills/",
    "/sum/configuration/",
    "/sum/herdr/",
    "/sum/update/",
  ]);
  const docLinks = page.locator(
    '.kv a[href*="github.com/douglasjarquin/sum/blob/main/docs/"]',
  );
  const docHrefs = await docLinks.evaluateAll((els) =>
    els.map((el) => el.getAttribute("href")),
  );
  expect(docHrefs).toEqual([
    "https://github.com/douglasjarquin/sum/blob/main/docs/repairs.md",
    "https://github.com/douglasjarquin/sum/blob/main/docs/recovery.md",
    "https://github.com/douglasjarquin/sum/blob/main/docs/terminology.md",
  ]);
});

test("skills lists the six bundled skills under their canonical names", async ({
  page,
}) => {
  await page.goto("skills/");
  await expect(page.locator(".kv .k")).toHaveText([
    "/sum-dispatch",
    "/sum-work",
    "/sum-deliver",
    "/sum-status",
    "/sum-develop",
    "/sum-update",
  ]);
});
