// What was typed in the Preview's address bar (27-preview.js): a file of the workspace, or a program of the workspace by its port.
// This is the part that needs no page. Nothing outside the workspace is ever loaded: an address of another site, or of another
// machine, is refused (the pane's server only reaches the ports of this workspace).

export const MIN_PORT = 1024;
export const MAX_PORT = 65535;

// 127.x.x.x, localhost, the private ranges and ::1: the names a person gives to "this machine, its own network"
export function isLocalHost(host) {
  const h = String(host || "").toLowerCase().replace(/^\[|\]$/g, "");
  if (h === "localhost" || h === "::1" || h.endsWith(".localhost")) return true;
  const m = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/.exec(h);
  if (!m) return false;
  const [a, b] = [Number(m[1]), Number(m[2])];
  if ([m[1], m[2], m[3], m[4]].some((x) => Number(x) > 255)) return false;
  return a === 127 || a === 10 || (a === 192 && b === 168) || (a === 172 && b >= 16 && b <= 31);
}

function portOk(n) { return Number.isInteger(n) && n >= MIN_PORT && n <= MAX_PORT; }

// "a/../b/./c.html" -> "b/c.html"; null when it climbs out of the folder
export function cleanRelative(rel) {
  const out = [];
  for (const seg of String(rel).split("/")) {
    if (seg === "" || seg === ".") continue;
    if (seg === "..") { if (!out.length) return null; out.pop(); continue; }
    out.push(seg);
  }
  return out.join("/");
}

// -> { empty: true } | { kind: "p", port, sub } | { kind: "f", path, alt } | { error: "outside" | "port" | "path" }
// home: the workspace's folder, base: the folder of the page now shown (a second place to look for a name), both without a trailing "/"
export function parseAddress(raw, home, base) {
  let text = String(raw || "").trim();
  if (!text) return { empty: true };
  const scheme = /^([a-z][a-z0-9+.-]*):\/\/(.*)$/i.exec(text);
  if (scheme) {
    if (!/^https?$/i.test(scheme[1])) return { error: "outside" };
    text = scheme[2];
    return hostAddress(text, true);
  }
  if (/^\d{4,5}(?:[/?#].*)?$/.test(text)) return hostAddress(text, false);
  // host:port/path without a scheme (localhost:5000, 127.0.0.1:5000/x), a name that cannot be a file of the workspace's tree
  const hp = /^(\[[^\]]+\]|[^/:?#\s]+):(\d+)(?:[/?#].*)?$/.exec(text);
  if (hp && isLocalHost(hp[1])) return hostAddress(text, false);
  if (hp && /^\d+$/.test(hp[2]) && !/^[./~]/.test(text) && /^[a-z0-9.-]+$/i.test(hp[1]) && hp[1].indexOf(".") > 0) return { error: "outside" };
  return fileAddress(text, home, base);
}

function hostAddress(text, hadScheme) {
  // text: [host:]port[/path][?q][#h]
  const m = /^(?:(\[[^\]]+\]|[^/:?#\s]+):)?(\d+)((?:[/?#]).*)?$/.exec(text);
  if (!m) return { error: hadScheme ? "outside" : "path" };
  if (m[1] && !isLocalHost(m[1])) return { error: "outside" };
  if (hadScheme && !m[1]) return { error: "outside" };
  const port = Number(m[2]);
  if (!portOk(port)) return { error: "port" };
  let sub = m[3] || "/";
  if (sub.charAt(0) !== "/") sub = "/" + sub;
  return { kind: "p", port, sub };
}

function fileAddress(text, home, base) {
  let t = text.replace(/[?#].*$/, "");
  const h = String(home || "").replace(/\/+$/, "");
  let rel;
  if (h && (t === h || t.indexOf(h + "/") === 0)) rel = t.slice(h.length);
  else rel = t;
  rel = cleanRelative(rel);
  if (!rel) return { error: "path" };
  const path = h + "/" + rel;
  // a name that is not found from the home folder is looked for beside the page now shown (the older way)
  const b = String(base || "").replace(/\/+$/, "");
  const alt = b && b !== h && b.indexOf(h + "/") === 0 && !/^\//.test(text) ? b + "/" + rel : "";
  return { kind: "f", path, alt };
}
