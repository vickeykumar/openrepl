// Records the frames of the animated pictures of the Genie guide (tools/record-guide-gifs.spec.js; tools/make-gif.py puts them together).
// Not part of the test run: `BASE_URL=http://localhost:8080 GENIE_LOCAL_ASSETS=1 npx playwright test -c tools/playwright.gifs.config.js`
const { defineConfig, devices } = require("@playwright/test");

module.exports = defineConfig({
  testDir: ".",
  testMatch: /record-(guide|skills)-gifs\.spec\.js/,
  timeout: 120 * 1000,
  expect: { timeout: 15 * 1000 },
  workers: 1,
  reporter: [["list"]],
  use: { baseURL: process.env.BASE_URL || "http://localhost:8080", colorScheme: "light" },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"], viewport: { width: 1280, height: 860 } } }],
});
