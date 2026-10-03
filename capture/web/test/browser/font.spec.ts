import { expect, test } from "@playwright/test";

test("self-hosts Inter for the capture component", async ({ page }) => {
  await page.goto("/index.html");

  await expect.poll(() => page.evaluate(() => document.fonts.check("16px Inter"))).toBe(true);

  const fontFamily = await page.locator("idenqa-capture").evaluate((element) => {
    return getComputedStyle(element).fontFamily;
  });
  expect(fontFamily).toContain("Inter");

  const fontRequests = await page.evaluate(() =>
    performance
      .getEntriesByType("resource")
      .map((entry) => entry.name)
      .filter((name) => name.includes("inter-latin-wght-normal")),
  );
  expect(fontRequests.some((name) => name.endsWith("inter-latin-wght-normal.woff2"))).toBe(true);
});
