// Genie's agent in the Preview (docs/lld/17-preview-pane.md "The agent in the preview", docs/lld/19-genie-agent.md): the bridge in the
// previewed page answers commands (read, click, type, ...), the pane passes them (window.Preview.call), and the agent uses them
// as preview_* actions. The page is a real page of the workspace in the real sandboxed frame; the model is made up at the
// network edge (what the real model says is not the question). GENIE_LOCAL_ASSETS=1 serves the page scripts of this checkout.
const { test, expect } = require("@playwright/test");
const fs = require("fs");
const path = require("path");
const { waitForTerminalReady } = require("./helpers");

const root = path.join(__dirname, "..", "..", "src");
const stamp = Date.now().toString(36);
const dir = `shop${stamp}`;

const PAGE = `<!doctype html><html><head><meta charset="utf-8"><title>Corner shop</title></head><body>
<h1>Corner shop</h1>
<p id="intro">Fresh bread every morning.</p>
<label for="email">Email</label><input id="email" type="text" placeholder="you@example.com">
<label for="pw">Password</label><input id="pw" type="password">
<label for="size">Size</label><select id="size"><option>S</option><option>M</option><option>L</option></select>
<label><input id="gift" type="checkbox"> Gift wrap</label>
<button id="add" type="button">Add to cart</button> <span id="count">0</span> in the cart
<a id="about" href="about.html">About us</a> <a id="out" href="https://example.com/x">Outside</a>
<form id="f" onsubmit="document.getElementById('sent').textContent='sent';return false"><input id="q" type="text" name="q"><button id="send" type="submit">Send</button></form>
<span id="sent"></span>
<div id="cover" style="position:fixed;left:0;top:0;width:100%;height:100%;display:none;background:rgba(0,0,0,.2)"></div>
<button id="hide" type="button" style="display:none">Hidden</button>
<script>
console.log("shop loaded");
document.getElementById("add").addEventListener("click", function(){ var n=document.getElementById("count"); n.textContent = String(+n.textContent + 1); });
fetch("missing.json").catch(function(){});
localStorage.setItem("cart","3");
setTimeout(function(){ console.error("boom"); }, 50);
document.getElementById("gift").addEventListener("change", function(){ console.log("gift " + this.checked); });
</script></body></html>`;

async function localAssets(page) {
  if (!process.env.GENIE_LOCAL_ASSETS) return;
  const serve = (file) => (route) => route.fulfill({ contentType: "application/javascript", body: fs.readFileSync(path.join(root, file)) });
  await page.route((u) => u.pathname.endsWith("/js/chat-widget.js"), serve("resources/chat-widget/dist/index.umd.js"));
  await page.route((u) => u.pathname.endsWith("/js/scribbler.js"), serve("js/dist/scribbler.js"));
}

async function ready(page) {
  await localAssets(page);
  await page.goto("/");
  await page.waitForFunction(() => window.FileBrowser && window.FileBrowser.ready() && window.Preview && window.EditorTabs);
  await expect(page.locator("#loader")).toBeHidden();
  await waitForTerminalReady(page);
}

async function makeSite(page) {
  const files = { "index.html": PAGE, "about.html": "<!doctype html><title>About</title><h1>About us</h1><a id='back' href='index.html'>Home</a>", "note.md": "# Notes\n\nhello" };
  await page.evaluate(async ([d, files]) => {
    await window.FileBrowser.create(d, "folder");
    for (const name of Object.keys(files)) await window.FileBrowser.create(d + "/" + name, "file");
    for (const [name, text] of Object.entries(files)) {
      await $.ajax({ url: preprocessurl("/ws_filebrowser?q=save&filepath=" + window.homedir + "/" + d + "/" + name), method: "POST", processData: false, data: btoa(text), contentType: "application/octet-stream" });
    }
  }, [dir, files]);
}

// opens the page in the pane and waits for its bridge to have told the pane about it
async function openShop(page, file = "index.html") {
  await page.evaluate(([d, f]) => window.Preview.openPath(window.homedir + "/" + d + "/" + f), [dir, file]);
  await expect(page.locator("#preview-pane")).toBeVisible();
  await expect(page.locator("#preview-address-text")).toHaveText(`${dir}/${file}`);
  await page.waitForFunction(() => window.Preview.state().title !== "" || window.Preview.state().outline.length > 0, null, { timeout: 15000 });
}

const call = (page, cmd, args, ms) => page.evaluate(([c, a, m]) => window.Preview.call(c, a, m), [cmd, args || {}, ms]);
const partRef = (text, re) => {
  const m = text.split("\n").find((l) => re.test(l));
  return m ? m.split(" ")[0] : null;
};

test("the page is read as text and numbered parts, and the pane's state carries the title and the outline", async ({ page }) => {
  await ready(page);
  await makeSite(page);
  await openShop(page);
  let state = await page.evaluate(() => window.Preview.state());
  expect(state.title).toBe("Corner shop");
  await expect.poll(() => page.evaluate(() => window.Preview.state().outline.map((o) => o.text))).toContain("Corner shop"); // the outline comes a moment after the load
  expect(state.plugins).toEqual({ console: true, outline: true, viewport: true });
  const r = await call(page, "read");
  expect(r.ok).toBe(true);
  expect(r.title).toBe("Corner shop");
  expect(r.text).toContain("h1 Corner shop");
  expect(r.text).toContain("Fresh bread every morning.");
  expect(r.text).toMatch(/e\d+ button "Add to cart"/);
  expect(r.text).toMatch(/e\d+ field "Email" type=text value=""/);
  expect(r.text).toMatch(/e\d+ field "Password" type=password(?! value)/); // a password's value is never given
  expect(r.text).toMatch(/e\d+ select "Size" value="S" options="S\|M\|L"/);
  expect(r.text).toMatch(/e\d+ checkbox "Gift wrap" unchecked/);
  expect(r.text).toMatch(/e\d+ link "About us" -> about.html/);
  expect(r.text).not.toContain("Hidden"); // what is not shown is not read
  expect(r.more).toBe(false);
  // the console and the calls of the page were kept by the pane: an error, and the call that failed with its reason
  await expect.poll(() => page.evaluate(() => window.Preview.logs().map((l) => l.text).join("|"))).toContain("boom");
  const reqs = await page.evaluate(() => window.Preview.requests());
  expect(reqs.some((q) => /missing\.json/.test(q.url) && q.status === 404)).toBe(true);
});

test("click, type, press, scroll, select, hover and wait act in the page and say what changed", async ({ page }) => {
  await ready(page);
  await makeSite(page);
  await openShop(page);
  const frame = page.frameLocator("#preview-frame");
  const read = await call(page, "read");
  const add = partRef(read.text, /button "Add to cart"/);
  const email = partRef(read.text, /field "Email"/);
  const size = partRef(read.text, /select "Size"/);
  const gift = partRef(read.text, /checkbox "Gift wrap"/);
  // a click is a real click: the page's own handler ran
  const c = await call(page, "click", { ref: add });
  expect(c.ok).toBe(true);
  await expect(frame.locator("#count")).toHaveText("1");
  // typing sets the value and tells the page (input and change)
  const t = await call(page, "type", { ref: email, text: "ann@example.com" });
  expect(t.ok).toBe(true);
  await expect(frame.locator("#email")).toHaveValue("ann@example.com");
  expect((await call(page, "type", { ref: email, text: "!", append: true })).ok).toBe(true);
  await expect(frame.locator("#email")).toHaveValue("ann@example.com!");
  // a list and a box
  expect((await call(page, "select", { ref: size, value: "L" })).value).toBe("L");
  await expect(frame.locator("#size")).toHaveValue("L");
  expect((await call(page, "select", { ref: gift, checked: true })).checked).toBe(true);
  await expect(frame.locator("#gift")).toBeChecked();
  await expect.poll(() => page.evaluate(() => window.Preview.logs().map((l) => l.text).join("|"))).toContain("gift true");
  // keys and scrolling and hovering
  expect((await call(page, "press", { key: "Escape" })).ok).toBe(true);
  const sc = await call(page, "scroll", { to: "bottom" });
  expect(sc.ok).toBe(true);
  expect(sc.scroll).toBeTruthy();
  expect((await call(page, "hover", { ref: add })).ok).toBe(true);
  // a part that is no longer there, and a wait that does not come
  expect((await call(page, "click", { ref: "e999" })).ok).toBe(false);
  const w = await call(page, "wait", { text: "never shown", timeout: 1 });
  expect(w.ok).toBe(false);
  expect(w.timeout).toBe(true);
  expect((await call(page, "wait", { text: "Fresh bread" })).ok).toBe(true);
});

test("a click that goes to another page says so; a page that is left ends the command", async ({ page }) => {
  await ready(page);
  await makeSite(page);
  await openShop(page);
  const read = await call(page, "read");
  const about = partRef(read.text, /link "About us"/);
  const r = await call(page, "click", { ref: about });
  expect(r, JSON.stringify(r)).toMatchObject({ ok: true });
  expect(r.navigated).toBe(true);
  expect(r.address).toContain("about.html");
  await expect(page.locator("#preview-address-text")).toHaveText(`${dir}/about.html`);
  const back = await page.evaluate(() => window.Preview.go("back"));
  expect(back.ok).toBe(true);
  expect(back.address).toContain("index.html");
});

test("what Genie must not do: a password field, a link that leaves the workspace, a covered part", async ({ page }) => {
  await ready(page);
  await makeSite(page);
  await openShop(page);
  const read = await call(page, "read");
  const pw = partRef(read.text, /field "Password"/);
  const out = partRef(read.text, /link "Outside"/);
  const add = partRef(read.text, /button "Add to cart"/);
  const send = partRef(read.text, /button "Send"/);
  expect((await call(page, "target", { ref: pw })).sensitive).toBe(true);
  const typed = await call(page, "type", { ref: pw, text: "secret" });
  expect(typed.ok).toBe(false);
  expect(typed.error).toMatch(/password or card field/);
  const t = await call(page, "target", { ref: out });
  expect(t.leaves).toBe(true);
  const refused = await call(page, "click", { ref: out });
  expect(refused.ok).toBe(false);
  expect(refused.leaves).toBe(true);
  expect((await call(page, "target", { ref: send })).submits).toBe(true);
  // something covers the button: the click is refused and says what covers it
  await call(page, "eval", { code: "document.getElementById('cover').style.display='block'" });
  const covered = await call(page, "click", { ref: add });
  expect(covered.ok).toBe(false);
  expect(covered.error).toMatch(/covered by div#cover/);
  const i = await call(page, "inspect", { ref: add });
  expect(i.coveredBy).toBe("div#cover");
  expect(i.visible).toBe(true);
});

test("eval, storage, inspect, resize and the downloads and windows the page tried", async ({ page }) => {
  await ready(page);
  await makeSite(page);
  await openShop(page);
  const e = await call(page, "eval", { code: "document.title + ' / ' + document.querySelectorAll('button').length" });
  expect(e.ok).toBe(true);
  expect(JSON.parse(e.value)).toBe("Corner shop / 3");
  expect((await call(page, "eval", { code: "(" })).ok).toBe(false); // a syntax error comes back as an answer
  expect((await call(page, "eval", { code: "undefinedVariable.x" })).ok).toBe(false);
  expect(JSON.parse((await call(page, "eval", { code: "Promise.resolve(7)" })).value)).toBe(7);
  // the page's own storage (the preview gives the page a working one)
  const s = await call(page, "storage", { area: "local", op: "get", key: "cart" });
  expect(s.ok).toBe(true);
  expect(s.value).toBe("3");
  expect((await call(page, "storage", { area: "local", op: "set", key: "k", value: "v" })).ok).toBe(true);
  expect((await call(page, "storage", { area: "local", op: "get" })).value).toMatchObject({ cart: "3", k: "v" });
  // a part: where it is and its style
  const read = await call(page, "read");
  const add = partRef(read.text, /button "Add to cart"/);
  const i = await call(page, "inspect", { ref: add });
  expect(i.rect.w).toBeGreaterThan(10);
  expect(i.styles.display).toBeTruthy();
  // the pane's size
  const r = await page.evaluate(() => window.Preview.resize("phone"));
  expect(r).toMatchObject({ ok: true, size: "phone", width: 390 });
  expect((await page.evaluate(() => window.Preview.resize("huge"))).ok).toBe(false);
  await page.evaluate(() => window.Preview.resize("fit"));
  // a popup and a download that the page tries are reported to the pane
  await call(page, "eval", { code: "(window.open('about.html'), 1)" });
  await call(page, "eval", { code: "(function(){var b=new Blob(['a,b\\n1,2'],{type:'text/csv'});var a=document.createElement('a');a.href=URL.createObjectURL(b);a.download='report.csv';document.body.appendChild(a);a.click();return 1})()" });
  await expect.poll(() => page.evaluate(() => (window.Preview.state().events || []).map((x) => x.kind + ":" + x.text).join("|"))).toMatch(/popup:about\.html.*download:report\.csv|download:report\.csv.*popup:about\.html/);
  const ev = await page.evaluate(() => window.Preview.state().events);
  expect(ev.find((x) => x.kind === "download").body).toBe("a,b\n1,2");
});

test("a page with no script has no bridge: Markdown says so at once", async ({ page }) => {
  await ready(page);
  await makeSite(page);
  await page.evaluate(([d]) => window.Preview.openPath(window.homedir + "/" + d + "/note.md"), [dir]);
  await expect(page.locator("#preview-pane")).toBeVisible();
  const r = await call(page, "read");
  expect(r.ok).toBe(false);
  expect(r.error).toMatch(/Markdown is shown as it is|no script/);
  const closed = await page.evaluate(() => { window.Preview.close(); return window.Preview.call("read", {}); });
  expect(closed.error).toMatch(/not open/);
});

test("the user's plugin switches stay in force: with Console off, the state leaves the console out", async ({ page }) => {
  await ready(page);
  await makeSite(page);
  await openShop(page);
  await expect.poll(() => page.evaluate(() => window.Preview.state().console.length)).toBeGreaterThan(0);
  await page.evaluate(() => {
    // the pane's plugin state is the one the App Store keeps (window.PreviewPlugins): turn Console off the way the user does
    window.__plug = window.PreviewPlugins.state();
    window.__plug.console = false;
  });
  const s = await page.evaluate(() => window.Preview.state());
  expect(s.plugins.console).toBe(false);
  expect(s.console).toBeUndefined();
  expect(await page.evaluate(() => window.Preview.logs())).toEqual([]);
});

// ---- the agent and the chat, with a made-up model ----------------------------------------------------------------------------

const step = (o) => JSON.stringify(o);

// A signed-in page whose model answers from `script`: a string is a chat answer, an object the JSON of an agent step.
async function openGenie(page, script) {
  const seen = { requests: [] };
  await page.route((u) => u.pathname.endsWith("/chat/usage"), (route) =>
    route.fulfill({ contentType: "application/json", body: JSON.stringify({ left: 18, cap: 20, perMinute: 0.33, signedIn: true, agent: { used: 1, perHour: 20, nextFreeSeconds: 100, maxSteps: 8 } }) })
  );
  await page.route((u) => u.pathname.endsWith("/chat/agent-outcome"), (route) => route.fulfill({ status: 204 }));
  await page.route((u) => u.pathname.endsWith("/chat/completions"), (route) => {
    const body = JSON.parse(route.request().postData() || "{}");
    seen.requests.push(body);
    const next = script.shift();
    if (next === undefined) return route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: { message: "the script is over" } }) });
    const headers = { "X-OpenREPL-Agent-Task": "task-1", "X-OpenREPL-Agent-Step": seen.requests.length + "/8", "Access-Control-Expose-Headers": "X-OpenREPL-Agent-Task, X-OpenREPL-Agent-Step, X-OpenREPL-Usage" };
    const content = typeof next === "string" ? next : step(next);
    route.fulfill({ status: 200, headers, contentType: "application/json", body: JSON.stringify({ choices: [{ message: { role: "assistant", content }, finish_reason: "stop" }], usage: { prompt_tokens: 1000, completion_tokens: 200 } }) });
  });
  await ready(page);
  await makeSite(page);
  await page.evaluate(() => {
    window.site_settings = Object.assign(window.site_settings || {}, { agentEnabled: true });
    document.body.classList.add("is-signed-in");
  });
  await page.evaluate(() => window.ChatWidget.open());
  return seen;
}

const answerCards = (page) =>
  page.evaluate(() => {
    window.__answerer = setInterval(() => {
      document.querySelectorAll(".cw-agent__ask button").forEach((b) => {
        if (/Allow for this session|Allow once|Send it/.test(b.textContent)) b.click();
      });
    }, 100);
  });

const askGenie = async (page, text) => {
  await page.locator("#chat-widget__input").fill(text);
  await page.locator("#chat-widget__submit").click();
};

test("an agent task in the Preview: open, read, type, click (asked first), read the console, and the page really changed", async ({ page }) => {
  const seen = await openGenie(page, [
    { say: "Opening it.", actions: [{ type: "preview_open", path: `${dir}/index.html` }], done: false },
    { say: "Reading.", actions: [{ type: "preview_read" }, { type: "preview_console" }], done: false },
    { say: "Trying.", actions: [{ type: "preview_type", ref: "e1", text: "ann@example.com" }, { type: "preview_click", ref: "e5" }], done: false },
    { say: "The cart has one item, and the console shows one error that I would fix.", actions: [], done: true },
  ]);
  await page.locator('#chat-widget__mode button[data-mode="agent"]').click();
  await answerCards(page);
  await askGenie(page, "test my page");
  await expect(page.locator(".cw-agent--ended")).toBeVisible({ timeout: 60000 });
  const frame = page.frameLocator("#preview-frame");
  await expect(frame.locator("#count")).toHaveText("1"); // the click was real
  await expect(frame.locator("#email")).toHaveValue("ann@example.com");
  // what the model was sent after the read: the page as parts, and the console
  const afterRead = JSON.stringify(seen.requests[2].messages).replace(/\\/g, ""); // (the results go back as JSON inside JSON)
  expect(afterRead).toContain("Corner shop");
  expect(afterRead).toMatch(/e\d+ button "Add to cart"/);
  expect(afterRead).toContain("boom");
  // the click and the typing asked once (one kind of card)
  await expect(page.locator(".cw-agent__step", { hasText: "Clicking e5 in the Preview page" })).toBeVisible();
});

test("in Chat the Preview's page is in the context with a chip, and the chip turns it off", async ({ page }) => {
  const seen = await openGenie(page, ["It shows a shop.", "Now you did not tell me."]);
  await page.evaluate(([d]) => window.Preview.openPath(window.homedir + "/" + d + "/index.html"), [dir]);
  await page.waitForFunction(() => window.Preview.state().title === "Corner shop", null, { timeout: 15000 });
  await page.waitForFunction(() => window.Preview.state().errors > 0, null, { timeout: 15000 });
  const chip = page.locator("#chat-widget__preview-chip");
  await expect(chip).toBeVisible();
  await expect(chip).toContainText("the preview");
  await askGenie(page, "what does my page show");
  await expect(page.locator(".chat-widget__message--system", { hasText: "It shows a shop." })).toBeVisible({ timeout: 20000 });
  const sent = JSON.stringify(seen.requests[0].messages);
  expect(sent).toContain("--- Preview (the page the Preview pane shows");
  expect(sent).toContain("Corner shop");
  expect(sent).toContain("boom");
  // the chip is the switch
  await chip.click();
  await expect(chip).toContainText("not read");
  await expect(page.locator("#chat-widget__submit")).toBeEnabled({ timeout: 15000 });
  await askGenie(page, "and now");
  await expect(page.locator(".chat-widget__message--system", { hasText: "Now you did not tell me." })).toBeVisible({ timeout: 20000 });
  expect(JSON.stringify(seen.requests[1].messages)).not.toContain("--- Preview (the page the Preview pane shows");
  // with the pane closed there is no chip
  await page.evaluate(() => window.Preview.close());
  await expect(chip).toHaveCount(0);
  await page.evaluate(() => localStorage.removeItem("genie-read-preview"));
});

test("the ring and the label show where Genie acts, and the pane says it is in use", async ({ page }) => {
  await ready(page);
  await makeSite(page);
  await openShop(page);
  const frame = page.frameLocator("#preview-frame");
  const read = await call(page, "read");
  const add = partRef(read.text, /button "Add to cart"/);
  await page.evaluate(() => window.Preview.busy(true));
  await expect(page.locator("#preview-status-text")).toHaveText("Genie is using this page");
  const clicking = call(page, "click", { ref: add });
  // a cursor moves to the part, with its label, then clicks (a ripple), and the ring is on the part
  await expect(frame.locator("#openrepl-cursor")).toBeVisible();
  await expect(frame.locator("#openrepl-cursor")).toContainText(`${add} - click`);
  await expect(frame.locator("#openrepl-ring")).toBeVisible();
  expect((await clicking).ok).toBe(true);
  await expect(frame.locator("#count")).toHaveText("1");
  // the cursor and the ring are not part of the page that is read
  const again = await call(page, "read");
  expect(again.text).not.toContain("openrepl");
  await page.evaluate(() => window.Preview.busy(false));
  await expect(page.locator("#preview-status-text")).not.toHaveText("Genie is using this page");
  await expect(frame.locator("#openrepl-ring")).toBeHidden({ timeout: 4000 }); // gone after a moment
});
