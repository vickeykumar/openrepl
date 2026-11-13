// The "Workspace snapshots" card of the profile page (src/resources/js/profile-snapshots.js). The profile page itself is only
// served to a signed-in user, so the card's own markup (read from profile.html, so that the two cannot drift apart) and its
// script are put into a page of the site, and the server's answers are given by the test.
const { test, expect } = require("@playwright/test");
const fs = require("fs");
const path = require("path");

const res = (...p) => path.join(__dirname, "..", "..", "src", "resources", ...p);
const html = fs.readFileSync(res("profile.html"), "utf8");
const cardMarkup = html.slice(html.indexOf('<section class="profile-card profile-snap"'), html.indexOf("</section>", html.indexOf('id="profile-snapshots"')) + "</section>".length);
const script = fs.readFileSync(res("js", "profile-snapshots.js"), "utf8");
const css = fs.readFileSync(res("css", "ui-refresh.css"), "utf8");

const iso = (hoursAgo) => new Date(Date.now() - hoursAgo * 3600 * 1000).toISOString().replace(/\.\d+Z$/, "Z");
const LIST = {
  enabled: true, keep: 3, minutes: 5,
  snapshots: [
    { generation: 90, at: iso(1), size: 1200, newest: true, few: true, before: false },
    { generation: 89, at: iso(5), size: 8700000, newest: false, few: false, before: false },
    { generation: 88, at: iso(30), size: 8650000, newest: false, few: false, before: true },
  ],
};

async function open(page, answers) {
  const seen = { lists: 0, posts: [], zips: [] };
  await page.route("**/profile/snapshots**", async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    if (url.pathname.endsWith("/zip")) { seen.zips.push(url.searchParams.get("generation")); return route.fulfill({ status: 200, contentType: "application/zip", body: "PK" }); }
    if (url.pathname.endsWith("/restore")) {
      const body = JSON.parse(req.postData() || "{}");
      seen.posts.push(body);
      const a = answers.restore ? answers.restore(body) : { status: 200, body: { mode: body.mode, folder: "restored-2026-11-12-1802", files: 41 } };
      return route.fulfill({ status: a.status, contentType: "application/json", body: JSON.stringify(a.body) });
    }
    seen.lists++;
    const a = answers.list ? answers.list(seen.lists) : { status: 200, body: LIST };
    return route.fulfill({ status: a.status, contentType: "application/json", body: JSON.stringify(a.body) });
  });
  await page.goto("/privacy.html");
  await page.addStyleTag({ content: css });
  await page.evaluate((m) => { const main = document.createElement("main"); main.className = "profile"; main.innerHTML = m; document.body.prepend(main); }, cardMarkup);
  await page.addScriptTag({ content: script });
  return seen;
}

const card = (page) => page.locator("#profile-snapshots");
const rows = (page) => card(page).locator("[data-snap-row]");

test("the card lists the snapshots from the server, newest first, with what is worth a look", async ({ page }) => {
  const seen = await open(page, {});
  await expect(card(page)).toBeVisible();
  await expect(rows(page)).toHaveCount(3);
  await expect(card(page).locator("[data-snap-meta]")).toHaveText("3 of 3 kept");
  await expect(rows(page).nth(0)).toContainText("Newest");
  await expect(rows(page).nth(0)).toContainText("Few files");
  await expect(rows(page).nth(0)).toContainText("1.2 KB");
  await expect(rows(page).nth(1)).toContainText("8.3 MB");
  await expect(rows(page).nth(2)).toContainText("Before restore");
  await expect(rows(page).nth(0)).toContainText("generation 90");
  // Refresh asks the server again
  await card(page).getByRole("button", { name: "Refresh" }).click();
  await expect.poll(() => seen.lists).toBe(2);
});

test("an empty list, a server with no snapshots and an error are each said in words", async ({ page }) => {
  await open(page, { list: () => ({ status: 200, body: { enabled: true, keep: 3, minutes: 5, snapshots: [] } }) });
  await expect(card(page)).toContainText("No snapshots yet");
  const p2 = await page.context().newPage();
  await open(p2, { list: () => ({ status: 200, body: { enabled: false, keep: 3, minutes: 5, snapshots: [] } }) });
  await expect(card(p2)).toContainText("keeps no workspace snapshots");
  await expect(card(p2).getByRole("button", { name: "Refresh" })).toBeHidden();
  const p3 = await page.context().newPage();
  await open(p3, { list: () => ({ status: 502, body: { error: "The object store did not answer. Try again in a minute." } }) });
  await expect(card(p3)).toContainText("The object store did not answer");
});

test("Get back into a new folder sends the generation and the way, says what was made and reads the list again", async ({ page }) => {
  const seen = await open(page, {});
  await rows(page).nth(1).getByRole("button", { name: /Get back/ }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog).toContainText("Get back the snapshot of");
  await expect(dialog.getByRole("radio", { name: /Put them in a new folder/ })).toBeChecked();
  await dialog.getByRole("button", { name: "Continue" }).click();
  await expect(dialog).toBeHidden();
  expect(seen.posts).toEqual([{ generation: 89, mode: "folder", confirm: "" }]);
  await expect(card(page).locator("[data-snap-status]")).toContainText("41 files are in the folder restored-2026-11-12-1802");
  await expect.poll(() => seen.lists).toBe(2);
});

test("a zip is a download of that generation and adds nothing to the workspace", async ({ page }) => {
  const seen = await open(page, {});
  await rows(page).nth(1).getByRole("button", { name: /Get back/ }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByRole("radio", { name: /Download as a zip/ }).check();
  const download = page.waitForEvent("download");
  await dialog.getByRole("button", { name: "Continue" }).click();
  await download;
  expect(seen.zips).toEqual(["89"]);
  expect(seen.posts).toEqual([]);
});

test("Replace asks for the word, stays off until it is typed in full, and then sends it", async ({ page }) => {
  const seen = await open(page, {});
  await rows(page).nth(1).getByRole("button", { name: /Get back/ }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByRole("radio", { name: /Replace my workspace/ }).check();
  await dialog.getByRole("button", { name: "Continue" }).click();
  await expect(dialog).toContainText("Replace your whole workspace?");
  await expect(dialog).toContainText("a copy of what you have now is saved as a snapshot");
  const replace = dialog.getByRole("button", { name: "Replace", exact: true });
  await expect(replace).toBeDisabled();
  await dialog.getByRole("textbox").fill("REPLA");
  await expect(replace).toBeDisabled();
  await dialog.getByRole("textbox").fill("replace");
  await expect(replace).toBeDisabled(); // the word is typed as it is shown
  await dialog.getByRole("textbox").fill("REPLACE");
  await expect(replace).toBeEnabled();
  await replace.click();
  await expect(dialog).toBeHidden();
  expect(seen.posts).toEqual([{ generation: 89, mode: "replace", confirm: "REPLACE" }]);
  await expect(card(page).locator("[data-snap-status]")).toContainText("your workspace is the snapshot of");
});

test("Cancel and Escape close the question and send nothing; an error from the server is said and nothing is claimed", async ({ page }) => {
  const seen = await open(page, { restore: () => ({ status: 429, body: null }) });
  await rows(page).nth(0).getByRole("button", { name: /Get back/ }).click();
  const dialog = page.getByRole("dialog");
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
  await rows(page).nth(0).getByRole("button", { name: /Get back/ }).click();
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await expect(dialog).toBeHidden();
  expect(seen.posts).toEqual([]);
  // an error
  await rows(page).nth(0).getByRole("button", { name: /Get back/ }).click();
  await dialog.getByRole("button", { name: "Continue" }).click();
  await expect(dialog).toBeHidden();
  await expect(card(page).locator("[data-snap-status]")).toContainText("Nothing was changed");
  await expect(card(page).locator("[data-snap-status]")).not.toContainText("Done");
});
