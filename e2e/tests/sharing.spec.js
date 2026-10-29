// A collaboration link shows the master's files in the collaborator's file browser: the collaborator's
// page names the master's workspace with ?homedir=<path>&hsig=<signature>, and the signature is what the
// server needs (the master's tree carries it). The Firebase sync of the terminal is not part of this: these
// tests speak to the file browser of the server the way the page does, from three browsers (cookie jars).
const { test, expect } = require("@playwright/test");

const CREATE = 1, REMOVE = 4, RENAME = 8;

async function tree(request, extra = "") {
  const res = await request.get("/ws_filebrowser?command=c" + extra);
  expect(res.status(), "the file browser answered").toBe(200);
  return res.json();
}

function names(root) {
  return (root.children || []).map((c) => c.text);
}

test("a collaborator works in the master's files, a stranger who knows the path does not", async ({ browser }) => {
  const master = await browser.newContext();
  const collaborator = await browser.newContext();
  const stranger = await browser.newContext();
  try {
    const m = master.request, c = collaborator.request, s = stranger.request;

    // the master's tree: its root is the workspace, with the signature that goes into the collaboration link
    const root = await tree(m);
    expect(root.hsig, "the root of the master's tree carries a signature").toBeTruthy();
    const home = root.id;
    const shared = `&homedir=${encodeURIComponent(home)}&hsig=${root.hsig}`;
    const folder = `${home}/shared-by-master-${Date.now()}`;

    // the collaborator, with the signature, sees the master's workspace and works in it
    expect((await tree(c, shared)).id).toBe(home);
    const post = (req, body) => req.post(`/ws_filebrowser?command=c${shared}`, { data: JSON.stringify(body), headers: { "Content-Type": "application/json" } });
    expect((await post(c, { Op: CREATE, Name: folder, type: "folder" })).ok(), "create").toBeTruthy();
    expect(names(await tree(m)), "the master sees what the collaborator made").toContain(folder.split("/").pop());
    expect((await post(c, { Op: RENAME, Name: folder, NewName: folder + "-renamed", type: "folder" })).ok(), "rename").toBeTruthy();
    expect(names(await tree(m))).toContain(folder.split("/").pop() + "-renamed");
    expect((await post(c, { Op: REMOVE, Name: folder + "-renamed", type: "folder" })).ok(), "remove").toBeTruthy();
    expect(names(await tree(m))).not.toContain(folder.split("/").pop() + "-renamed");
    const zip = await c.get(`/ws_filebrowser?q=zip&filepath=${encodeURIComponent(home)}${shared}`);
    expect(zip.ok(), "zip of the master's workspace").toBeTruthy();

    // a stranger who has the path but not the signature gets their own workspace
    for (const bad of [`&homedir=${encodeURIComponent(home)}`, `&homedir=${encodeURIComponent(home)}&hsig=00ff`]) {
      expect((await tree(s, bad)).id, "the stranger was given the master's workspace").not.toBe(home);
    }
    // and cannot reach a path in it either
    const peek = await s.get(`/ws_filebrowser?q=zip&filepath=${encodeURIComponent(home)}&homedir=${encodeURIComponent(home)}`);
    expect(peek.ok(), "a stranger downloaded the master's workspace").toBeFalsy();

    // a signature is for one workspace only
    const other = await tree(s);
    const lent = `&homedir=${encodeURIComponent(other.id)}&hsig=${root.hsig}`;
    expect((await tree(s, lent)).id, "a signature worked for another workspace").toBe(other.id);
  } finally {
    await master.close(); await collaborator.close(); await stranger.close();
  }
});
