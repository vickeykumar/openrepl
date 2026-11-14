// A code link has an end (page/17-share-code.js, server/snippets.go): the calendar offers tomorrow up to the most the site
// allows, starts on a month ahead, and the server refuses a day outside that.
const { test, expect } = require("@playwright/test");
const { waitForTerminalReady } = require("./helpers");

const utcDay = (n) => {
  const d = new Date();
  d.setUTCDate(d.getUTCDate() + n);
  return d.toISOString().slice(0, 10);
};

test("a code link is made with an end date chosen in the calendar", async ({ page }) => {
  test.setTimeout(120 * 1000);
  await page.goto("/");
  await page.waitForFunction(() => window.EditorTabs && window.FileBrowser && window.FileBrowser.ready());
  await waitForTerminalReady(page);
  await page.locator("#share-btn-1").click();
  const date = page.locator("#code-link-until");
  await expect(date).toBeVisible();
  // from tomorrow to the most the site allows (a month by default), and a month is picked
  expect(await date.getAttribute("min")).toBe(utcDay(1));
  expect(await date.getAttribute("max")).toBe(utcDay(30));
  expect(await date.inputValue()).toBe(utcDay(30));
  await expect(page.locator("#code-link-until-hint")).toContainText("up to 30 days");

  // a day chosen: the link says when it works until
  await date.fill(utcDay(3));
  await page.evaluate(() => editor.env.editor.setValue("print('expiry')", -1));
  await page.locator("#create-code-link").click();
  await expect(page.locator("#code-link-ends")).toContainText("Works until", { timeout: 20 * 1000 });
  const id = (await page.locator("#code-link").inputValue()).split("/s/")[1];
  const got = await page.evaluate((i) => fetch("/snippet?id=" + i).then((r) => r.json()), id);
  const end = new Date(got.expires * 1000);
  expect(end.toISOString().slice(0, 10)).toBe(utcDay(3));
  expect(end.toISOString().slice(11, 19)).toBe("23:59:59");

  // the server keeps the limit whatever the page says
  const far = await page.evaluate((day) => fetch("/snippet", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ lang: "python", code: "x", expires: day }) }).then(async (r) => ({ status: r.status, body: await r.json() })), utcDay(31));
  expect(far.status).toBe(400);
  expect(far.body.error).toContain("at most 30 days");
  const past = await page.evaluate((day) => fetch("/snippet", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ lang: "python", code: "x", expires: day }) }).then((r) => r.status), utcDay(-1));
  expect(past).toBe(400);
});
