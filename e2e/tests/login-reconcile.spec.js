// A session the server does not know (GET /login says "not logged in" with a reason) while the browser is still signed in with
// Firebase: the page posts the sign-in again, once (src/js/src/page/05-auth.js, lib-signin.mjs). Firebase and the /login
// answers are made up at the network edge and in the page; the page script is the real one.
//
// GENIE_LOCAL_ASSETS=1 serves the page script from this checkout's build (src/js/dist) for a run before an image is built.
const { test, expect } = require("@playwright/test");
const fs = require("fs");
const path = require("path");

async function setup(page, answer) {
  if (process.env.GENIE_LOCAL_ASSETS) {
    await page.route((u) => u.pathname.endsWith("/js/scribbler.js"), (r) => r.fulfill({ contentType: "application/javascript", body: fs.readFileSync(path.join(__dirname, "..", "..", "src", "js", "dist", "scribbler.js")) }));
  }
  let release;
  const gate = new Promise((r) => (release = r));
  const posts = [];
  await page.route((u) => u.pathname === "/login", async (route) => {
    const req = route.request();
    if (req.method() === "POST") {
      posts.push(req.postDataJSON());
      return route.fulfill({ status: 200, contentType: "application/json", body: "{}" });
    }
    await gate; // until the test has made the browser's Firebase user
    return route.fulfill({ contentType: "application/json", body: JSON.stringify(answer) });
  });
  await page.goto("/");
  await page.waitForFunction(() => window.firebase && window.firebase.auth);
  await page.evaluate(() => {
    const fake = {
      uid: "u-test",
      emailVerified: true,
      toJSON() {
        return { uid: "u-test", email: "t@example.com", displayName: "T", stsTokenManager: { accessToken: "tok", expirationTime: Date.now() + 3600000 } };
      },
    };
    const real = window.firebase.auth;
    const patched = function () {
      return Object.assign(Object.create(real.apply(window.firebase, [])), {
        onAuthStateChanged(cb) {
          setTimeout(() => cb(fake), 0);
          return () => {};
        },
      });
    };
    window.firebase.auth = Object.assign(patched, real);
  });
  release();
  return posts;
}

test("a session the server does not know is posted again, once", async ({ page }) => {
  const posts = await setup(page, { uid: "u-test", loggedIn: false, reason: "not_found", sessionID: "x", expirationTime: Date.now() + 1e9 });
  await expect.poll(() => posts.length, { timeout: 15000 }).toBe(1);
  expect(posts[0].user.uid).toBe("u-test");
  // the page reloads after the post and is told the same: it does not post again (a refusing server cannot make a loop)
  await page.waitForLoadState("load");
  await page.waitForTimeout(1500);
  expect(posts.length).toBe(1);
});

test("a blocked account is not posted again", async ({ page }) => {
  const posts = await setup(page, { uid: "u-test", loggedIn: false, reason: "blocked" });
  await page.waitForTimeout(2500);
  expect(posts.length).toBe(0);
});

test("a visitor, with no session in the cookie, is not signed in behind their back", async ({ page }) => {
  const posts = await setup(page, { uid: "", loggedIn: false });
  await page.waitForTimeout(2500);
  expect(posts.length).toBe(0);
});
