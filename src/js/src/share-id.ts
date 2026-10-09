// What in the address of a page is a share id.
//
// A shared session is opened as  https://openrepl.com/#<id>,  where <id> is the
// Firebase push key of the owner's page: 20 characters, the first of them "-"
// (8 of time and 12 random from the alphabet - 0-9 A-Z _ a-z; the SDK checks
// the length, and the first one stays "-" until the year 2109). The accepted
// length is a little wider than 20, so that a change of the SDK's key could not
// stop sharing.
//
// Anything else after the # is only an anchor of the page: #languages,
// #workspace, #request. It must not turn the visitor into a viewer of a session
// that does not exist, which is what "any hash is a share id" did.
//
// The same rule is in chat-widget/src/index.ts (its own bundle); a test there
// compares the two patterns. Callers read the id once, when their script loads,
// and keep it: clicking an anchor later changes the hash, not the session.

export const SHARE_ID_PATTERN = /^-[A-Za-z0-9_-]{12,30}$/;

// The share id in a location.hash ("#-OJVdrF-2UPpWUafqBoU"), or "" if there is none.
export function shareIdFrom(hash: string): string {
  const id = String(hash || "").replace(/^#/, "");
  return SHARE_ID_PATTERN.test(id) ? id : "";
}
