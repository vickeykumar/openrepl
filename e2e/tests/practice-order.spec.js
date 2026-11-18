// The order of the practice list (src/resources/js/scribbler-misc.js): a question made in this visit is the first row whatever the page,
// the filters and the sort; a ticked question keeps its place for a second, then goes to the Done group, the one ticked last first.
// GENIE_LOCAL_ASSETS=1 serves the page's scripts and styles from this checkout, for a run before an image is built.
const { test, expect } = require("@playwright/test");
const fs = require("fs");
const path = require("path");
const root = path.join(__dirname, "..", "..", "src", "resources");

const DAY = 86400000;
const NOW = Date.now();
const make = (n, topic, level, ageDays) => ({
  id: "q" + n, name: "Question " + n, nameHyphenated: "question-" + n, topic, difficulty: level, added: NOW - ageDays * DAY, updated: NOW - ageDays * DAY,
  description: "d", code_templates: {}, delimeter: "x",
});
// 30 questions of the topic "Arrays", the 3 newest of them done (q1 ticked last), and one of another topic
const QUESTIONS = Array.from({ length: 30 }, (_, i) => make(i + 1, "Arrays", "Easy", i + 1));
const STATE = { q1: { done: true, t: NOW - 1000 }, q2: { done: true, t: NOW - 5000 }, q3: { done: true, t: NOW - 3000 } };

async function open(page) {
  await page.addInitScript(([q, s]) => { try { localStorage.setItem("questions", JSON.stringify(q)); localStorage.setItem("practiceState", JSON.stringify(s)); } catch (e) {} }, [QUESTIONS, STATE]);
  await page.route((u) => u.pathname.endsWith("/js/dsa.json"), (r) => r.fulfill({ contentType: "application/json", body: "[]" }));
  if (process.env.GENIE_LOCAL_ASSETS) {
    const serve = (type, file) => (r) => r.fulfill({ contentType: type, body: fs.readFileSync(path.join(root, file)) });
    await page.route((u) => u.pathname.endsWith("/js/scribbler-misc.js"), serve("application/javascript", "js/scribbler-misc.js"));
    await page.route((u) => u.pathname.endsWith("/js/practice-store.js"), serve("application/javascript", "js/practice-store.js"));
    await page.route((u) => u.pathname.endsWith("/css/ui-refresh.css"), serve("text/css", "css/ui-refresh.css"));
  }
  await page.goto("/practice/dsa-questions");
  await expect(page.locator(".q-row").first()).toBeVisible();
}

const names = (page) => page.locator("#q-body .q-row .q-link").evaluateAll((els) => els.map((e) => e.firstChild.textContent));

test("done questions go below the others in a Done group, the one ticked last first", async ({ page }) => {
  await open(page);
  await page.locator("#f-size").selectOption("100");
  const rows = await names(page);
  // the to-do ones by date, newest first; the done ones last, by when they were ticked: q1, q3, q2
  expect(rows.slice(0, 3)).toEqual(["Question 4", "Question 5", "Question 6"]);
  expect(rows.slice(-3)).toEqual(["Question 1", "Question 3", "Question 2"]);
  await expect(page.locator(".q-divider")).toHaveText("Done · 3");
});

test("a tick keeps the row in place for a second, then moves it to Done, and Undo brings it back", async ({ page }) => {
  await open(page);
  await page.locator("#f-size").selectOption("100");
  await page.locator("#done-q4").check();
  // at once: still the first row
  expect((await names(page))[0]).toBe("Question 4");
  await expect.poll(async () => (await names(page))[0], { timeout: 4000 }).toBe("Question 5");
  expect((await names(page))[0]).toBe("Question 5");
  const rows = await names(page);
  expect(rows.slice(-4)[0]).toBe("Question 4"); // the one ticked last is the first of Done
  await expect(page.locator(".q-divider")).toHaveText("Done · 4");
  await page.locator(".notice__action", { hasText: "Undo" }).click();
  await expect.poll(async () => (await names(page))[0], { timeout: 4000 }).toBe("Question 4");
  await expect(page.locator(".q-divider")).toHaveText("Done · 3");
});

test("a question that was just made is the first row, on page 1, and a filter that would hide it is cleared", async ({ page }) => {
  await open(page);
  await page.evaluate(() => {
    // the model is not asked: the new question is made here
    window.generateNewQuestion = async () => ({ id: "new1", name: "Brand New Graph", nameHyphenated: "brand-new-graph", topic: "Graphs", difficulty: "Hard", added: Date.now(), updated: Date.now(), description: "d", code_templates: {}, delimeter: "x" });
  });
  await page.locator("#f-size").selectOption("10");
  await page.locator("#f-topic").selectOption("Arrays");
  await page.locator("#f-sort").selectOption("old"); // a sort that would put a new question last
  await page.locator(".q-page", { hasText: "2" }).first().click(); // page 2, in the Arrays topic
  await page.locator("#newQuestionBtn").click();
  await page.locator("#topic").fill("Graphs");
  await page.locator("#difficulty").selectOption("Hard");
  await page.locator("#qsubmit-btn").click();
  const first = page.locator("#q-body .q-row").first();
  await expect(first).toContainText("Brand New Graph");
  await expect(first).toContainText("New");
  await expect(page.locator("#f-topic")).toHaveValue("");
  await expect(page.locator(".notice")).toContainText("cleared: Topic");
  // the next change of the list ends the pin: the badge goes, the row is where the sort puts it
  await page.locator("#f-sort").selectOption("new");
  await expect(page.locator(".q-badge")).toHaveCount(0);
});
