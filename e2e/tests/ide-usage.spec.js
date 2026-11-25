// The IDE data usage pages (canvas "IDE data usage and limits", boards 5 and 6): the chip and the notes of the IDE (js/ide-usage.js) and the
// "Data usage" section of the profile page (js/profile-usage.js). The server's answer of GET /ide_usage is given by the test; the counting itself
// is tested in Go (src/ideusage, src/server/ideusage_test.go). The profile page is only served to a signed-in user, so its section
// (read from profile.html, so that the two cannot drift apart) and its script are put into a page of the site.
const { test, expect } = require("@playwright/test");
const fs = require("fs");
const path = require("path");

const res = (...p) => path.join(__dirname, "..", "..", "src", "resources", ...p);
const html = fs.readFileSync(res("profile.html"), "utf8");
const sectionMarkup = html.slice(html.indexOf('<section class="profile-card profile-usage"'), html.indexOf("</section>", html.indexOf('id="profile-usage"')) + "</section>".length);
const script = fs.readFileSync(res("js", "profile-usage.js"), "utf8");
const css = fs.readFileSync(res("css", "ui-refresh.css"), "utf8");

const MB = 1048576;
const day = (n) => new Date(Date.now() - n * 86400 * 1000).toISOString().slice(0, 10);
function state(used, over) {
  const limit = 200 * MB;
  return Object.assign({
    used: used * MB, limit, pct: Math.min(100, Math.round(used * 100 / 200)), scope: "day", on: true, exempt: false, plan: "Free",
    warn: used >= 160 && used < 200, atLimit: used >= 200, warnPct: 80, termBytes: 20 * MB, termSecs: 10, reserve: 2 * MB,
    resetsAt: Date.now() + 6 * 3600 * 1000,
    parts: { terminal: Math.round(used * 0.62) * MB, genie: Math.round(used * 0.21) * MB, files: Math.round(used * 0.11) * MB, editor: Math.round(used * 0.05) * MB, preview: Math.round(used * 0.01) * MB },
  }, over || {});
}
const history = [20, 40, 5, 12, 55, 70, 84].map((m, i) => ({ day: day(6 - i), total: m * MB, parts: {} }));

async function answer(page, body) {
  await page.route("**/ide_usage**", (route) => route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify(body) }));
}

test("the profile shows today's data by part, the last 7 days and the plan", async ({ page }) => {
  await answer(page, { state: state(84), history, guest: false, signedIn: true });
  await page.goto("/privacy.html");
  await page.addStyleTag({ content: css });
  await page.evaluate((m) => { const main = document.createElement("main"); main.className = "profile"; main.innerHTML = m; document.body.prepend(main); }, sectionMarkup);
  await page.addScriptTag({ content: script });
  const sec = page.locator("#profile-usage");
  await expect(sec).toBeVisible();
  await expect(sec).toContainText("Free plan, today");
  await expect(sec.locator(".profile-usage__big")).toHaveText("84 MB");
  await expect(sec.locator(".profile-usage__of")).toContainText("of 200 MB");
  await expect(sec).toContainText(/Resets at .* in \d+ h \d+ min/);
  for (const part of ["Terminal", "Genie", "File browser", "Editor", "Preview"]) await expect(sec.locator(".profile-usage__legend")).toContainText(part);
  await expect(sec.locator(".profile-usage__bar")).toHaveAttribute("aria-label", /Terminal 52 MB/);
  await expect(sec.locator(".profile-usage__col")).toHaveCount(7);
  await expect(sec.locator(".profile-usage__colname").last()).toHaveText("Today");
  await expect(sec.locator(".profile-usage__limit")).toHaveCount(1);
  await expect(sec).toContainText("Terminal speed");
  await expect(sec).toContainText("20 MB / 10 s");
  await expect(sec).toContainText("Paid plans and credits");
  await expect(sec).toContainText("Coming later");
  await expect(sec).toContainText("Website pages and downloads from the site are not in this total");
});

test("the profile section stays hidden while metering is off, and says so at the limit", async ({ page }) => {
  await answer(page, { state: state(10, { on: false }), history, signedIn: true });
  await page.goto("/privacy.html");
  await page.evaluate((m) => { const main = document.createElement("main"); main.className = "profile"; main.innerHTML = m; document.body.prepend(main); }, sectionMarkup);
  await page.addScriptTag({ content: script });
  await page.waitForTimeout(500);
  await expect(page.locator("#profile-usage")).toBeHidden();

  await page.unroute("**/ide_usage**");
  await answer(page, { state: state(200), history, signedIn: true });
  await page.evaluate(() => { document.getElementById("profile-usage").remove(); });
  await page.evaluate((m) => { const main = document.createElement("main"); main.className = "profile"; main.innerHTML = m; document.body.prepend(main); }, sectionMarkup);
  await page.addScriptTag({ content: script });
  await expect(page.locator("#profile-usage .profile-usage__alert")).toContainText("reached today's limit");
});

test("the IDE shows a chip and one note at 80%, a red chip and a note at the limit", async ({ page }) => {
  await page.addInitScript(() => { try { localStorage.clear(); } catch (e) { /* storage off */ } });
  await answer(page, { state: state(160), signedIn: true });
  await page.goto("/python");
  const chip = page.locator(".ide-data-chip");
  await expect(chip).toBeVisible();
  await expect(chip).toHaveText("160 of 200 MB today");
  await expect(chip).toHaveClass(/is-warn/);
  await expect(page.locator(".ide-data-note")).toContainText("You have used 80% of today's data");
  await expect(page.locator(".ide-data-note a", { hasText: "See usage" })).toHaveAttribute("href", "./profile");

  await page.unroute("**/ide_usage**");
  await answer(page, { state: state(200), signedIn: true });
  await page.evaluate(() => document.dispatchEvent(new Event("visibilitychange")));
  await expect(chip).toHaveText("200 of 200 MB used");
  await expect(chip).toHaveClass(/is-limit/);
  const note = page.locator(".ide-data-note");
  await expect(note).toContainText("Daily data limit reached (200 MB)");
  await expect(note.getByRole("button", { name: "Add credits, coming later" })).toBeDisabled();

  await page.unroute("**/ide_usage**");
  await answer(page, { state: state(0), signedIn: true });
  await page.evaluate(() => document.dispatchEvent(new Event("visibilitychange")));
  await expect(chip).toHaveClass(/is-ok/);
  await expect(page.locator(".ide-data-note")).toContainText("Data limit is back to full");
});

test("a guest at the limit is asked to sign in, and sees nothing below the warning", async ({ page }) => {
  await page.addInitScript(() => { try { localStorage.clear(); } catch (e) { /* storage off */ } });
  const guest = (used) => state(used, { scope: "session", signInLimit: 200 * MB, limit: 50 * MB, used: used * MB, pct: Math.round(used * 2), warn: used >= 40 && used < 50, atLimit: used >= 50, resetsAt: 0 });
  await answer(page, { state: guest(10), signedIn: false });
  await page.goto("/python");
  await page.waitForTimeout(800);
  await expect(page.locator(".ide-data-chip")).toBeHidden();
  await expect(page.locator(".ide-data-note")).toHaveCount(0);

  await page.unroute("**/ide_usage**");
  await answer(page, { state: guest(50), signedIn: false });
  await page.evaluate(() => document.dispatchEvent(new Event("visibilitychange")));
  await expect(page.locator(".ide-data-chip")).toHaveText("50 of 50 MB used");
  const note = page.locator(".ide-data-note");
  await expect(note).toContainText("You have used this session's 50 MB");
  await expect(note).toContainText("Guests get 50 MB per session (1 hour). Sign in free to get 200 MB a day.");
  await expect(note.getByRole("button", { name: "Sign in" })).toBeVisible();
  await expect(note.getByRole("button", { name: "Maybe later" })).toBeVisible();
});

test("an admin who is exempt sees no chip", async ({ page }) => {
  await answer(page, { state: state(500, { exempt: true }), signedIn: true });
  await page.goto("/python");
  await page.waitForTimeout(800);
  await expect(page.locator(".ide-data-chip")).toBeHidden();
});

test("the note stays on top of a maximized workspace", async ({ page }) => {
  await page.addInitScript(() => { try { localStorage.clear(); } catch (e) { /* storage off */ } });
  await answer(page, { state: state(200), signedIn: true });
  await page.goto("/python");
  await expect(page.locator(".ide-data-note")).toBeVisible();
  await page.evaluate(() => { document.querySelector(".ide-shell").classList.add("is-maximized"); document.body.classList.add("ide-maximized"); });
  const note = page.locator(".ide-data-note");
  await expect(note).toBeVisible();
  // the note is the element on top at its own centre, not the maximized shell
  const onTop = await note.evaluate((n) => { const r = n.getBoundingClientRect(); const e = document.elementFromPoint(r.left + r.width / 2, r.top + 10); return n.contains(e); });
  expect(onTop).toBe(true);
});
