// The "Genie chats" card of the profile page (js/profile-chats.js). The server of the chats is a stand-in; the page is the real one.
const { test, expect } = require("@playwright/test");
const fs = require("fs");
const path = require("path");
const PROFILE = path.join(__dirname, "..", "..", "src", "resources");

async function open(page, state) {
  const calls = [];
  const json = (r, o) => r.fulfill({ contentType: "application/json", body: JSON.stringify(o) });
  await page.route((u) => u.pathname.endsWith("/genie_chats"), (r) => {
    const b = JSON.parse(r.request().postData() || "{}");
    calls.push(b);
    if (state.refuse) return r.fulfill({ status: 403, contentType: "application/json", body: JSON.stringify({ error: { code: state.refuse } }) });
    if (b.op === "state") return json(r, { enabled: true, off: state.off, open: ["a", "b"], chats: state.chats, bytes: 1468006, limits: { chats: 50, tabs: 8, bytes: 204800 } });
    if (b.op === "off") { state.off = b.off; return json(r, { ok: true, off: b.off }); }
    if (b.op === "deleteAll") { state.chats = []; return json(r, { ok: true }); }
    if (b.op === "exportAll") return json(r, { markdown: "# one\n\n---\n\n# two" });
    return json(r, { ok: true });
  });
  // the profile page is for a signed-in user: the card and its script are the real ones, served inside a minimal page
  const script = fs.readFileSync(path.join(PROFILE, "js", "profile-chats.js"), "utf8");
  const html = fs.readFileSync(path.join(PROFILE, "profile.html"), "utf8");
  const section = html.slice(html.indexOf('<section class="profile-card profile-snap" id="profile-chats"'), html.indexOf('<script src="./js/profile-chats.js">'));
  await page.route("**/profile-card-test", (r) => r.fulfill({ contentType: "text/html", body: "<!doctype html><html><head><meta charset=\"utf-8\"></head><body><main>" + section + "<script>" + script + "</script></main></body></html>" }));
  await page.goto("/profile-card-test");
  return calls;
}

const twelve = Array.from({ length: 12 }, (_, i) => ({ id: "c" + i, title: "chat " + i, updated: 1, messages: 2, size: 100 }));

test("the card shows the chats of the workspace, and the switch, download and delete work", async ({ page }) => {
  const state = { off: false, chats: twelve };
  const calls = await open(page, state);
  const card = page.locator("#profile-chats");
  await expect(card).toBeVisible();
  await expect(card.locator("[data-chats-meta]")).toHaveText("12 chats · 1.4 MB · 2 open tabs");
  await expect(card).toContainText("an admin included");
  const save = card.locator("[data-chats-save]");
  await expect(save).toBeChecked();
  await save.uncheck();
  await expect(card.locator("[data-chats-status]")).toContainText("not saved any more");
  expect(calls.some((c) => c.op === "off" && c.off === true)).toBe(true);
  const [download] = await Promise.all([page.waitForEvent("download"), card.locator("[data-chats-download]").click()]);
  expect(download.suggestedFilename()).toBe("genie-chats.md");
  // delete asks once: the first press only arms it
  await card.locator("[data-chats-delete]").click();
  await expect(card.locator("[data-chats-delete]")).toHaveText("Delete all chats for good?");
  expect(calls.some((c) => c.op === "deleteAll")).toBe(false);
  await card.locator("[data-chats-delete]").click();
  await expect(card.locator("[data-chats-status]")).toContainText("All chats were deleted");
  await expect(card.locator("[data-chats-meta]")).toContainText("0 chats");
  await expect(card.locator("[data-chats-delete]")).toBeDisabled();
});

test("the card is hidden where chats are not kept", async ({ page }) => {
  await open(page, { off: false, chats: [], refuse: "off" });
  await expect(page.locator("#profile-chats")).toBeHidden();
});
