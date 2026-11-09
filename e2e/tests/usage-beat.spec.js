// Time on the site (js/usage-beat.js): the page tells the server about its active seconds, and says
// nothing for a browser that sends "Do Not Track". The beat is answered here, so no count is kept.
const { test, expect } = require("@playwright/test");

async function collectBeats(page) {
  const beats = [];
  await page.route("**/usage/beat", async (route) => {
    beats.push(JSON.parse(route.request().postData()));
    await route.fulfill({ status: 204 });
  });
  return beats;
}

test("a visit is reported once it has been in front for a few seconds", async ({ page }) => {
  const beats = await collectBeats(page);
  await page.goto("/python");
  await page.mouse.move(200, 200);
  await expect.poll(() => beats.length, { timeout: 20000 }).toBeGreaterThan(0);
  const first = beats[0];
  expect(first.vid).toMatch(/^[a-zA-Z0-9_-]{8,40}$/);
  expect(first.lang).toBe("python");
  expect(["desktop", "phone", "tablet"]).toContain(first.device);
  expect(first.secs).toBeLessThanOrEqual(65);
  // nothing but seconds and labels
  expect(Object.keys(first).sort()).toEqual(["device", "lang", "ref", "secs", "vid"]);
});

test("a browser that sends Do Not Track sends no beats", async ({ page }) => {
  await page.addInitScript(() => Object.defineProperty(navigator, "doNotTrack", { get: () => "1" }));
  const beats = await collectBeats(page);
  await page.goto("/python");
  await page.mouse.move(200, 200);
  await page.waitForTimeout(9000);
  expect(beats).toHaveLength(0);
});
