// When the page posts a sign-in again (src/page/lib-signin.mjs).
import test from "node:test";
import assert from "node:assert/strict";
import { reconcileWanted, RECONCILE_EVERY_MS } from "../src/page/lib-signin.mjs";

const lost = { uid: "u1", loggedIn: false, reason: "not_found" };

test("a session the server does not know is posted again, once in a while", () => {
  assert.equal(reconcileWanted(lost, 0, 1000), true);
  assert.equal(reconcileWanted({ ...lost, reason: "expired" }, 0, 1000), true);
  assert.equal(reconcileWanted({ ...lost, reason: "cookie_logged_out" }, 0, 1000), true);
  assert.equal(reconcileWanted(lost, 1000, 1000 + RECONCILE_EVERY_MS - 1), false, "just tried");
  assert.equal(reconcileWanted(lost, 1000, 1000 + RECONCILE_EVERY_MS), true);
});

test("nothing is posted for a visitor, a blocked account or a good session", () => {
  assert.equal(reconcileWanted({ loggedIn: false, uid: "" }, 0, 1), false);
  assert.equal(reconcileWanted({ loggedIn: false, uid: "u1" }, 0, 1), false, "no reason: no session came");
  assert.equal(reconcileWanted({ ...lost, reason: "blocked" }, 0, 1), false);
  assert.equal(reconcileWanted({ ...lost, reason: "none" }, 0, 1), false);
  assert.equal(reconcileWanted({ uid: "u1", loggedIn: true }, 0, 1), false);
  assert.equal(reconcileWanted(null, 0, 1), false);
});
