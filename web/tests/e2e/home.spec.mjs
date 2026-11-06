import { expect, test } from "@playwright/test";

test("home renders the hero, rail, features, quick start, and foot", async ({
  page,
}) => {
  await page.goto("./");

  const h1 = page.locator("h1");
  await expect(h1).toContainText("Many agents.");
  await expect(h1).toContainText("One finished task.");

  const railLabels = page.locator(".hero .rail .lbl");
  await expect(railLabels).toHaveText([
    "Your conversation",
    "Process owner",
    "Written checkpoint",
  ]);

  const features = page.locator(".band .feature");
  await expect(features).toHaveCount(6);
  await expect(features.nth(5).locator(".n")).toHaveText("06");
  await expect(features.nth(5).locator(".t")).toHaveText(
    "Portable verification",
  );

  await expect(page.locator(".codeblock")).toContainText("codex --no-daemon");
  await expect(page.locator("main")).toContainText("Authenticated native acceptance remains unrun");
  await expect(page.locator("main")).not.toContainText("bin/sumctl");

  const foot = page.locator("footer.foot");
  await expect(foot).toContainText("Stands on");
  await expect(foot.locator('a[href="/sum/llms.txt"]')).toHaveText("llms.txt");
});

test("agents can discover and read documentation without JavaScript", async ({ browser, request, baseURL }) => {
  const context = await browser.newContext({ javaScriptEnabled: false });
  const page = await context.newPage();
  await page.goto(baseURL);
  await expect(page.locator("main")).toContainText("Sum is free under the MIT license");
  await expect(page.locator('pre code.language-sh')).toContainText("codex --no-daemon");
  await expect(page.locator('link[rel="canonical"]')).toHaveAttribute("href", "https://douglasjarquin.github.io/sum/");
  const metadata = JSON.parse(await page.locator('script[type="application/ld+json"]').textContent());
  expect(metadata.codeRepository).toBe("https://github.com/douglasjarquin/sum");
  expect(metadata.isAccessibleForFree).toBe(true);
  const index = await request.get(new URL("llms.txt", baseURL).href);
  expect(index.status()).toBe(200);
  expect(await index.text()).toContain("https://douglasjarquin.github.io/sum/install/");
  const sitemap = await request.get(new URL("sitemap-index.xml", baseURL).href);
  expect(sitemap.status()).toBe(200);
  const childURL = (await sitemap.text()).match(/<loc>([^<]+)<\/loc>/)?.[1];
  expect(childURL).toBe("https://douglasjarquin.github.io/sum/sitemap-0.xml");
  const routes = await request.get(new URL(new URL(childURL).pathname, baseURL).href);
  expect(routes.status()).toBe(200);
  const urls = [...(await routes.text()).matchAll(/<loc>([^<]+)<\/loc>/g)].map((match) => match[1]);
  expect(urls).toHaveLength(9);
  expect(urls).toContain("https://douglasjarquin.github.io/sum/install/");
  expect(urls.every((url) => url.startsWith("https://douglasjarquin.github.io/sum/"))).toBe(true);
  expect((await request.get(new URL("missing-agentready-audit/", baseURL).href)).status()).toBe(404);
  await context.close();
});

test("every internal link on home resolves under the /sum/ base", async ({
  page,
}) => {
  await page.goto("./");
  const hrefs = await page
    .locator("a[href]")
    .evaluateAll((anchors) => anchors.map((a) => a.getAttribute("href")));
  const internal = hrefs.filter((href) => href && href.startsWith("/"));
  expect(internal.length).toBeGreaterThan(0);
  for (const href of internal) {
    expect(href.startsWith("/sum/")).toBe(true);
  }
});
