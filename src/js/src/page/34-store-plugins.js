// The editor plugins of the App Store (docs/lld/18-app-store.md, "Editor plugins"). A plugin that is on for the signed-in user runs in a
// sandboxed frame of this page (sandbox="allow-scripts" only: no access to the page, the account, the cookies or the network of this
// site). Through a small api it can do two things: ask for the text of the editor, and show one line of text in the status bar.
// The script comes from the server's own catalog (appstore/plugins.go); an admin installs it and the user turns it on.

var frames = {};   // plugin id -> { frame, span, count }
var timer = 0;

function el(tag, cls, text) { var n = document.createElement(tag); if (cls) n.className = cls; if (text !== undefined) n.textContent = text; return n; }
function signedIn() { return document.body.classList.contains("is-signed-in"); }
function url(path) { return typeof window.preprocessurl === "function" ? window.preprocessurl(path) : path; }

function editorText() {
  try {
    var e = window["editor"];
    var ace = e && e.env && e.env.editor;
    var t = ace ? ace.getValue() : "";
    return String(t).slice(0, 200000);
  } catch (x) { return ""; }
}

// what runs in the frame: the api, then the plugin's script, then its entry point
function frameDoc(code) {
  var bridge = "(function(){var cb={},n=0;window.addEventListener('message',function(e){var d=e.data;if(d&&d.reply&&cb[d.reply]){cb[d.reply](d.text);delete cb[d.reply];}});" +
    "window.__api={status:function(t){parent.postMessage({openreplPlugin:'status',text:String(t).slice(0,200)},'*');}," +
    "text:function(){return new Promise(function(r){var id=++n;cb[id]=r;parent.postMessage({openreplPlugin:'text',id:id},'*');});}," +
    "every:function(ms,f){setInterval(f,Math.max(500,ms));f();}};})();";
  var safe = String(code).replace(/<\/script/gi, "<\\/script");
  return "<!doctype html><meta charset=\"utf-8\"><script>" + bridge + "<\/script><script>try{" + safe + "\n;if(typeof openreplPlugin==='function')openreplPlugin(window.__api);}catch(e){}<\/script>";
}

function bar() {
  var b = document.getElementById("appstore-plugins");
  if (b) return b;
  var host = document.getElementById("controls-buttons");
  if (!host) return null;
  b = el("span", "appstore__plugins");
  b.id = "appstore-plugins";
  host.appendChild(b);
  return b;
}

function stop(id) {
  var f = frames[id];
  if (!f) return;
  if (f.frame.parentNode) f.frame.parentNode.removeChild(f.frame);
  if (f.span.parentNode) f.span.parentNode.removeChild(f.span);
  delete frames[id];
}

function start(p) {
  var host = bar();
  if (!host || frames[p.id]) return;
  var frame = document.createElement("iframe");
  frame.setAttribute("sandbox", "allow-scripts"); // no allow-same-origin: the frame has no origin of its own
  frame.setAttribute("referrerpolicy", "no-referrer");
  frame.setAttribute("aria-hidden", "true");
  frame.tabIndex = -1;
  frame.title = "Plugin " + p.name;
  frame.style.cssText = "position:absolute;width:0;height:0;border:0;visibility:hidden";
  var span = el("span", "appstore__plugin", "");
  span.title = "Plugin " + p.name;
  host.appendChild(span);
  frames[p.id] = { frame: frame, span: span, count: 0, window: null };
  frame.srcdoc = frameDoc(p.code);
  document.body.appendChild(frame);
  frames[p.id].window = frame.contentWindow;
}

window.addEventListener("message", function (e) {
  var d = e.data;
  if (!d || typeof d !== "object" || !d.openreplPlugin) return;
  var id = null;
  Object.keys(frames).forEach(function (k) { if (frames[k].frame.contentWindow === e.source) id = k; });
  if (!id) return; // only the frames this module made
  var f = frames[id];
  if (++f.count > 200) return; // a plugin that talks too much is ignored for the rest of that second

  if (d.openreplPlugin === "status") f.span.textContent = String(d.text || "").slice(0, 200);
  else if (d.openreplPlugin === "text" && typeof d.id === "number") f.frame.contentWindow.postMessage({ reply: d.id, text: editorText() }, "*");
});
setInterval(function () { Object.keys(frames).forEach(function (k) { frames[k].count = 0; }); }, 1000);

function refresh() {
  if (!signedIn()) { Object.keys(frames).forEach(stop); return Promise.resolve(); }
  return fetch(url("/store/api/plugins"), { credentials: "same-origin", cache: "no-store" }).then(function (res) { return res.ok ? res.json() : { plugins: [] }; }).then(function (j) {
    var want = {};
    (j.plugins || []).forEach(function (p) { want[p.id] = true; start(p); });
    Object.keys(frames).forEach(function (id) { if (!want[id]) stop(id); });
  }, function () {});
}

function schedule() { clearTimeout(timer); timer = setTimeout(function () { refresh().then(schedule); }, 30000); }

window.AppStorePlugins = { refresh: refresh, running: function () { return Object.keys(frames); } };
setTimeout(function () { refresh().then(schedule); }, 2500);

export {};
