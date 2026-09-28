import { expect, test } from "@playwright/test";

test("remainder renders the hero, feature cells, exit codes, and foot", async ({
  page,
}) => {
  await page.goto("remainder/");

  await expect(page.locator(".solo .eyebrow")).toHaveText(
    "remainder · one-shot quota CLI",
  );
  const h1 = page.locator("h1");
  await expect(h1).toContainText("Local evidence,");
  await expect(h1).toContainText("not a dashboard.");

  const features = page.locator(".band .feature");
  await expect(features).toHaveCount(6);
  await expect(features.locator(".t")).toHaveText([
    "Selected providers",
    "--all",
    "Compact, JSON, TOON, scalar",
    "Pace",
    "Observation cache",
    "Exit codes",
  ]);

  const exitCodes = features.nth(5).locator("code");
  await expect(exitCodes).toHaveText(["0", "1", "2", "3", "130"]);

  await expect(page.locator("footer.foot")).toContainText(
    "Pinchos and Sum remain independent consumers.",
  );
});
