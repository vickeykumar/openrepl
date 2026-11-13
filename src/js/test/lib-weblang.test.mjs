import test from "node:test";
import assert from "node:assert/strict";
import { WEB_STARTERS, freeName, isStarter, planRun } from "../src/page/lib-weblang.mjs";

const files = (...names) => (p) => names.includes(p);

test("the starters are real pages, and an empty editor or another starter may be replaced", () => {
  assert.match(WEB_STARTERS.html, /<!DOCTYPE html>/);
  assert.match(WEB_STARTERS.css, /body \{/);
  assert.equal(isStarter(""), true);
  assert.equal(isStarter("  \n"), true);
  assert.equal(isStarter(WEB_STARTERS.html), true);
  assert.equal(isStarter(WEB_STARTERS.css + "\n"), true);
  assert.equal(isStarter("print(1)"), false);
});

test("a free name is the first that does not exist", () => {
  assert.equal(freeName("/h", "index", ".html", files()), "/h/index.html");
  assert.equal(freeName("/h", "index", ".html", files("/h/index.html")), "/h/index-2.html");
  assert.equal(freeName("/h", "index", ".html", files("/h/index.html", "/h/index-2.html")), "/h/index-3.html");
  assert.equal(freeName("", "style", ".css", files()), "style.css");
});

test("HTML: an open .html page is saved and shown; an untitled editor makes index.html (never over another file) and shows it", () => {
  assert.deepEqual(planRun("html", "/h/site/a.html", "/h", files()), { save: "/h/site/a.html", preview: "/h/site/a.html" });
  assert.deepEqual(planRun("html", "/h/b.HTM", "/h", files()), { save: "/h/b.HTM", preview: "/h/b.HTM" });
  assert.deepEqual(planRun("html", "", "/h", files()), { create: "/h/index.html", preview: "/h/index.html" });
  assert.deepEqual(planRun("html", "", "/h", files("/h/index.html")), { create: "/h/index-2.html", preview: "/h/index-2.html" });
  assert.match(planRun("html", "/h/main.py", "/h", files()).message, /not an HTML page/);
});

test("CSS: it is shown on the index.html beside it, and says what to do when there is none", () => {
  assert.deepEqual(planRun("css", "/h/site/style.css", "/h", files("/h/site/index.html")), { save: "/h/site/style.css", preview: "/h/site/index.html" });
  const none = planRun("css", "/h/site/style.css", "/h", files());
  assert.equal(none.save, "/h/site/style.css");
  assert.equal(none.preview, undefined);
  assert.match(none.message, /make an index\.html that links to it/);
  assert.match(none.message, /href="style\.css"/);
  const fresh = planRun("css", "", "/h", files("/h/index.html"));
  assert.deepEqual(fresh, { create: "/h/style.css", preview: "/h/index.html" });
  assert.equal(planRun("css", "", "/h", files("/h/style.css")).create, "/h/style-2.css");
  assert.match(planRun("css", "/h/a.py", "/h", files()).message, /not a stylesheet/);
});
