import { expect, test } from "@playwright/test";

test("favicon, touch icon, and og image resolve under the base", async ({
  page,
  request,
  baseURL,
}) => {
  await page.goto("./");
  const hrefs = await page.$$eval(
    'link[rel="icon"], link[rel="apple-touch-icon"]',
    (els) => els.map((el) => el.getAttribute("href")),
  );
  expect(hrefs).toEqual([
    "/sum/favicon-32.png",
    "/sum/apple-touch-icon-180.png",
  ]);
  for (const href of hrefs) {
    const res = await request.get(new URL(href, baseURL).toString());
    expect(res.status()).toBe(200);
  }
});

test("og:image is an absolute douglasjarquin.github.io URL", async ({
  page,
}) => {
  await page.goto("./");
  const og = await page
    .locator('meta[property="og:image"]')
    .getAttribute("content");
  expect(og).toBe(
    "https://douglasjarquin.github.io/sum/og-1200x630.png",
  );
});
