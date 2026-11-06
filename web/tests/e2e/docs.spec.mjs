import { expect, test } from "@playwright/test";

const pages = [
  { route: "docs/", h1: "Documentation" },
  { route: "install/", h1: "Start" },
  { route: "architecture/", h1: "Architecture" },
  { route: "verification/", h1: "Verification" },
  { route: "skills/", h1: "Skills" },
  { route: "configuration/", h1: "Configuration" },
  { route: "herdr/", h1: "HC terminal runtime" },
  { route: "update/", h1: "Adoption & updates" },
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
    "https://github.com/douglasjarquin/sum/blob/main/docs/native-hc.md",
    "https://github.com/douglasjarquin/sum/blob/main/docs/ACCEPTANCE.md",
    "https://github.com/douglasjarquin/sum/blob/main/docs/legacy.md",
  ]);
});

test("skills lists the four procedures under their canonical names", async ({
  page,
}) => {
  await page.goto("skills/");
  await expect(page.locator(".kv .k")).toHaveText([
    "/sum-work",
    "/sum-review",
    "/sum-finish",
    "/sum-resume",
  ]);
  await expect(page.locator('a[href$="/skills/sum-develop/SKILL.md"]')).toHaveText("sum-develop");
  await expect(page.locator("main")).toContainText("HC identity is required only for actual HC operations and native acceptance");
});
