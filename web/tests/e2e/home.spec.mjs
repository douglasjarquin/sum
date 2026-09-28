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
    "Coordinator",
    "Process owner",
    "Records",
  ]);

  const features = page.locator(".band .feature");
  await expect(features).toHaveCount(6);
  await expect(features.nth(5).locator(".n")).toHaveText("06");
  await expect(features.nth(5).locator(".t")).toHaveText(
    "Portable verification",
  );

  await expect(page.locator(".codeblock")).toContainText("mise run setup");

  const foot = page.locator("footer.foot");
  await expect(foot).toContainText("Sister project:");
  await expect(foot).toContainText("Stands on");
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
