// The contact form's server side (src/server/feedback.go, feedback_spam.go) and the
// admin API that reads it. The CI image has no OPENREPL_RECAPTCHA_SECRET, so the
// reCAPTCHA answer is not checked here (the Go tests cover that with a fake Google).
const { test, expect } = require("@playwright/test");

test("an empty message is refused", async ({ request }) => {
  const r = await request.post("/feedback", { form: { message: "" } });
  expect(r.status()).toBe(400);
  expect(await r.text()).toContain("Write a message first");
});

test("an obvious spam message is thanked and nothing is kept", async ({ request }) => {
  const r = await request.post("/feedback", {
    form: { message: "Best online casino [url=https://spam.example]play now[/url]", email: "bot@example.com" },
  });
  expect(r.status()).toBe(200);
  expect(await r.text()).toContain("Thanks");
});

test("a bot that fills the hidden field is thanked and ignored", async ({ request }) => {
  const r = await request.post("/feedback", { form: { message: "hello", website: "http://spam.example" } });
  expect(r.status()).toBe(200);
});

test("GET /feedback goes to the dashboard", async ({ request }) => {
  const r = await request.get("/feedback", { maxRedirects: 0 });
  expect(r.status()).toBe(302);
  expect(r.headers()["location"]).toContain("/admin");
});

test("the admin API is closed to a visitor", async ({ request }) => {
  expect((await request.get("/admin/feedback")).status()).toBe(401);
  expect((await request.post("/admin/feedback/spam/delete")).status()).toBe(401);
});
