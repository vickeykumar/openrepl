// When the page should put a sign-in right with the server (05-auth.js). The session of the browser is in a cookie, and the
// server keeps a record of it; `GET /login` says "not logged in" when the record is not there, though the cookie names the
// account and the browser's own sign-in (Firebase) is still valid. The page then posts the sign-in again, once.

export const RECONCILE_EVERY_MS = 10 * 60 * 1000; // not more often than this, so that a server that keeps refusing cannot make a loop

// answer: the JSON of GET /login; lastMs: when the page last tried (0: never); nowMs: now.
export function reconcileWanted(answer, lastMs, nowMs) {
  if (!answer || answer.loggedIn === true) return false;
  if (!answer.uid || !answer.reason) return false; // no session came with the cookie: a visitor, nothing to put right
  if (answer.reason === "blocked" || answer.reason === "none") return false; // the account is blocked, or there is nothing to post
  return !(lastMs > 0 && nowMs - lastMs < RECONCILE_EVERY_MS);
}
