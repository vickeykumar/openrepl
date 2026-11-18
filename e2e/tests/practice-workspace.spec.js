// Practice kept in the workspace (js/practice-store.js, server/practice_ws.go): the list and the code written for a question go to /practice_ws
// when the visitor is signed in, so another browser opens the question with the code. The server is a stand-in here (the Go tests cover the
// folder); what is checked is what the page sends and what it does with the answers.
// GENIE_LOCAL_ASSETS=1 serves the page's scripts from this checkout, for a run before an image is built.
const { test, expect } = require("@playwright/test");
const fs = require("fs");
const path = require("path");
const root = path.join(__dirname, "..", "..", "src", "resources");

// the workspace of one person: shared by the two browsers of the test
function workspace(opts = {}) {
  const ws = { questions: {}, state: {}, code: {}, calls: [], fallbackCalls: 0 };
  ws.install = async (page) => {
    await page.route((u) => u.pathname.endsWith("/js/dsa.json"), (r) => r.fulfill({ contentType: "application/json", body: "[]" }));
    if (process.env.GENIE_LOCAL_ASSETS) {
      await page.route((u) => u.pathname.endsWith("/js/practice-store.js"), (r) => r.fulfill({ contentType: "application/javascript", body: fs.readFileSync(path.join(root, "js/practice-store.js")) }));
    }
    await page.route("**/practice/progress", (r) => { ws.fallbackCalls++; r.fulfill({ contentType: "application/json", body: JSON.stringify({ questions: {}, state: {}, signedIn: true }) }); });
    await page.route("**/practice_ws", (r) => {
      const body = r.request().postDataJSON();
      ws.calls.push(body.op);
      const json = (o, status = 200) => r.fulfill({ status, contentType: "application/json", body: JSON.stringify(o) });
      if (opts.off) return json({ error: { code: "off", message: "off" } }, 403);
      if (body.op === "doc") return json({ questions: ws.questions, state: ws.state, signedIn: true });
      if (body.op === "merge") {
        Object.assign(ws.questions, (body.doc || {}).questions || {});
        Object.assign(ws.state, (body.doc || {}).state || {});
        return json({ questions: ws.questions, state: ws.state, signedIn: true });
      }
      if (body.op === "code_put") {
        if (!ws.questions[body.id]) return json({ error: { code: "no_question", message: "x" } }, 409);
        (ws.code[body.id] = ws.code[body.id] || {})[body.language] = { code: body.code, t: body.t };
        return json({ ok: true });
      }
      if (body.op === "code_get") return json({ id: body.id, code: ws.code[body.id] || {} });
      return json({ error: { code: "bad_op" } }, 400);
    });
  };
  return ws;
}

const Q = { id: "q1", name: "Two Sum", nameHyphenated: "two-sum", topic: "Arrays", difficulty: "Easy", added: Date.now() - 1000, updated: Date.now() - 1000, description: "d", code_templates: {}, delimeter: "x" };

async function open(browser, ws, seed) {
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  await ws.install(page);
  if (seed) await page.addInitScript((q) => { try { localStorage.setItem("questions", JSON.stringify([q])); } catch (e) {} }, Q);
  await page.goto("/practice/dsa-questions");
  await page.waitForFunction(() => window.PracticeStore && PracticeStore.saveCode);
  await page.evaluate(() => PracticeStore.init());
  return { ctx, page };
}

test("the code written for a question is sent to the workspace after the typing stops, and another browser opens the question with it", async ({ browser }) => {
  const ws = workspace();
  const a = await open(browser, ws, true);
  // the list reached the workspace first (the question must be in it before code is kept for it)
  await expect.poll(() => Object.keys(ws.questions)).toEqual(["q1"]);
  await a.page.evaluate(() => PracticeStore.saveCode("q1", "Python", "def two_sum(nums, target):\n    return [0, 1]"));
  await a.page.evaluate(() => PracticeStore.saveCode("q1", "Go", "func twoSum() {}"));
  // not at once: a moment after the typing stops
  expect(ws.calls.filter((c) => c === "code_put")).toHaveLength(0);
  await expect.poll(() => Object.keys(ws.code.q1 || {}).sort(), { timeout: 8000 }).toEqual(["Go", "Python"]);
  // the same person in another browser: nothing there, the question and the code come from the workspace
  const b = await open(browser, ws, false);
  await expect.poll(() => b.page.evaluate(() => PracticeStore.all().map((q) => q.id))).toEqual(["q1"]);
  const codes = await b.page.evaluate(() => PracticeStore.loadCode("q1"));
  expect(codes.Python.code).toContain("return [0, 1]");
  expect(codes.Go.code).toBe("func twoSum() {}");
  await a.ctx.close();
  await b.ctx.close();
});

test("with the workspace copy switched off, the page uses the account's database as before and keeps the code in the browser", async ({ browser }) => {
  const ws = workspace({ off: true });
  const a = await open(browser, ws, true);
  await expect.poll(() => ws.fallbackCalls).toBeGreaterThan(0);
  await a.page.evaluate(() => PracticeStore.saveCode("q1", "Python", "x = 1"));
  const codes = await a.page.evaluate(() => PracticeStore.loadCode("q1"));
  expect(codes.Python.code).toBe("x = 1");
  await a.ctx.close();
});
