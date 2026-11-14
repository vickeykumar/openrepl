// What is typed in the Preview's address bar (src/page/lib-address.mjs).
import test from "node:test";
import assert from "node:assert/strict";
import { parseAddress, isLocalHost, cleanRelative } from "../src/page/lib-address.mjs";

const H = "/tmp/home";
const p = (s, base) => parseAddress(s, H, base);

test("a name is a file counted from the home folder", () => {
  assert.deepEqual(p("web/index.html"), { kind: "f", path: "/tmp/home/web/index.html", alt: "" });
  assert.deepEqual(p("/web/index.html"), { kind: "f", path: "/tmp/home/web/index.html", alt: "" });
  assert.deepEqual(p("/tmp/home/notes.md"), { kind: "f", path: "/tmp/home/notes.md", alt: "" });
  assert.deepEqual(p("  ./a/../b/c.png?x=1#y "), { kind: "f", path: "/tmp/home/b/c.png", alt: "" });
});

test("a name also gets a second place to look: beside the page now shown", () => {
  assert.deepEqual(p("style.css", "/tmp/home/site"), { kind: "f", path: "/tmp/home/style.css", alt: "/tmp/home/site/style.css" });
  assert.equal(p("/style.css", "/tmp/home/site").alt, "");
  assert.equal(p("style.css", "/tmp/home").alt, "");
  assert.equal(p("style.css", "/elsewhere").alt, "");
});

test("a name cannot climb out of the home folder", () => {
  assert.deepEqual(p("../etc/passwd"), { error: "path" });
  assert.deepEqual(p("a/../../x.html"), { error: "path" });
  assert.deepEqual(p("/"), { error: "path" });
  assert.equal(cleanRelative("a/./b//c"), "a/b/c");
});

test("a port, with or without a path, a host or a scheme", () => {
  assert.deepEqual(p("5000"), { kind: "p", port: 5000, sub: "/" });
  assert.deepEqual(p("5000/api/users?x=1"), { kind: "p", port: 5000, sub: "/api/users?x=1" });
  assert.deepEqual(p("localhost:5000"), { kind: "p", port: 5000, sub: "/" });
  assert.deepEqual(p("127.0.0.1:8080/x"), { kind: "p", port: 8080, sub: "/x" });
  assert.deepEqual(p("http://127.5.0.1:3000/a"), { kind: "p", port: 3000, sub: "/a" });
  assert.deepEqual(p("https://10.1.2.3:9000"), { kind: "p", port: 9000, sub: "/" });
  assert.deepEqual(p("192.168.1.5:5000"), { kind: "p", port: 5000, sub: "/" });
  assert.deepEqual(p("http://[::1]:5000/z"), { kind: "p", port: 5000, sub: "/z" });
});

test("a port outside 1024 to 65535 is refused", () => {
  assert.deepEqual(p("80"), { kind: "f", path: "/tmp/home/80", alt: "" }); // two digits: a name, which the pane then says it cannot show
  assert.deepEqual(p("http://localhost:80"), { error: "port" });
  assert.deepEqual(p("70000"), { error: "port" });
  assert.deepEqual(p("localhost:99999"), { error: "port" });
});

test("another site or another machine is refused", () => {
  assert.deepEqual(p("https://example.com"), { error: "outside" });
  assert.deepEqual(p("http://8.8.8.8:5000"), { error: "outside" });
  assert.deepEqual(p("ftp://localhost:5000"), { error: "outside" });
  assert.deepEqual(p("example.com:8080"), { error: "outside" });
  assert.deepEqual(p("http://localhost"), { error: "outside" });
});

test("nothing typed", () => {
  assert.deepEqual(p("   "), { empty: true });
  assert.deepEqual(p(""), { empty: true });
});

test("the names of this machine", () => {
  assert.equal(isLocalHost("localhost"), true);
  assert.equal(isLocalHost("127.9.0.1"), true);
  assert.equal(isLocalHost("172.16.0.1"), true);
  assert.equal(isLocalHost("172.32.0.1"), false);
  assert.equal(isLocalHost("256.1.1.1"), false);
  assert.equal(isLocalHost("example.com"), false);
});
