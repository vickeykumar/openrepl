// Tools on a part of a board (docs/lld/21-genie-canvas.md, "Tools on a part"; boards 11 to 15 of the canvas design): in Select part mode the board's
// helper script tells the page which part is picked; this file draws the floating toolbar over it (Look: fill, corner, text; Shape: layout,
// size; Guard: lock, note; More), the small panels with a live preview, and sends the changes back to the helper, which applies them as an
// inline style on that one element and reports the board's new HTML (37-canvas.js saves it as a version with the label made here).
//
// Part of the page script (T20), bundled into js/scribbler.js; 37-canvas.js creates it (createParts) and hands it the messages of the boards.

import { toHex, cornerValue, alignOffsets, distributeOffsets, partLabel, parseInline, SHADOWS, validStyle } from "./lib-canvas.mjs";

function el(tag, cls, text) { var e = document.createElement(tag); if (cls) e.className = cls; if (text !== undefined) e.textContent = text; return e; }
function svg(p, w) { w = w || 17; return '<svg width="' + w + '" height="' + w + '" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' + p + "</svg>"; }
var ICON = {
  fill: svg('<path d="M12 3s6 6.2 6 10.5a6 6 0 0 1-12 0C6 9.2 12 3 12 3z"/>'),
  corner: svg('<path d="M5 19V12a7 7 0 0 1 7-7h7"/>'),
  text: svg('<path d="M5 18l5-12 5 12 M7 14h6 M17 9h3 M18.5 9v9"/>'),
  stack: svg('<rect x="4" y="5" width="16" height="5" rx="2"/><rect x="4" y="14" width="16" height="5" rx="2"/>'),
  size: svg('<path d="M4 9V5h4 M20 9V5h-4 M4 15v4h4 M20 15v4h-4"/><rect x="8" y="8" width="8" height="8" rx="1.5"/>'),
  lock: svg('<rect x="5" y="11" width="14" height="9" rx="2"/><path d="M8 11V8a4 4 0 0 1 8 0v3"/>'),
  unlock: svg('<rect x="5" y="11" width="14" height="9" rx="2"/><path d="M8 11V8a4 4 0 0 1 7.5-2"/>'),
  note: svg('<path d="M20 12a8 8 0 0 1-11.6 7.1L4 20l1-4.2A8 8 0 1 1 20 12z"/>'),
  more: svg('<circle cx="5" cy="12" r="1.6" fill="currentColor"/><circle cx="12" cy="12" r="1.6" fill="currentColor"/><circle cx="19" cy="12" r="1.6" fill="currentColor"/>'),
  align: svg('<path d="M4 4v16 M8 7h12 M8 13h8 M8 19h10"/>'),
};
var CORAL = "#FE6A6B", BLUE = "#7FB4FF", LIME = "#B7E34B", AMBER = "#FFB86B";
var FONTS = [["System", "system-ui, sans-serif"], ["Serif", "Georgia, serif"], ["Mono", "ui-monospace, Menlo, monospace"], ["Arial", "Arial, sans-serif"], ["Verdana", "Verdana, sans-serif"], ["Trebuchet", "'Trebuchet MS', sans-serif"], ["Times", "'Times New Roman', serif"]];

// The parts of a board that were locked (data-locked), as the page reads them from its HTML: where each is, what it is called and what it is.
export function lockedParts(html) {
  var out = [];
  try {
    var doc = new DOMParser().parseFromString(String(html || ""), "text/html");
    var all = doc.querySelectorAll("[data-locked]");
    for (var i = 0; i < all.length && out.length < 20; i++) out.push({ path: pathOf(all[i], doc), name: nameOf(all[i]), outer: all[i].outerHTML });
  } catch (e) {
    // no parts then
  }
  return out;
}
function nameOf(e) { var s = e.tagName.toLowerCase(); if (e.id) s += "#" + e.id; else { var c = (e.getAttribute("class") || "").trim().split(/\s+/).filter(Boolean)[0]; if (c) s += "." + c; } var t = (e.textContent || "").replace(/\s+/g, " ").trim().slice(0, 30); return s + (t ? ' "' + t + '"' : ""); }
function pathOf(e, doc) { var p = []; for (var n = e; n && n !== doc.documentElement; n = n.parentNode) { if (n === doc.body) { p.unshift("body"); break; } var i = 1, s = n; while ((s = s.previousElementSibling)) i++; p.unshift(n.tagName.toLowerCase() + ":nth-child(" + i + ")"); } return p.join(" > "); }

// Does a new version of a board change or remove a part that was locked? Returns the name of the first one it does, or "".
export function lockedViolation(oldHtml, newHtml) {
  var locks = lockedParts(oldHtml);
  if (!locks.length) return "";
  try {
    var doc = new DOMParser().parseFromString(String(newHtml || ""), "text/html");
    for (var i = 0; i < locks.length; i++) {
      var now = doc.querySelector(locks[i].path);
      if (!now || now.outerHTML !== locks[i].outer) return locks[i].name;
    }
  } catch (e) {
    return "";
  }
  return "";
}

// host: { root, vp, getView, getBoard, frameOf, post, say, noteAt, askGenie, leave, accent }
export function createParts(host) {
  var cur = null;          // { board, items: [info of each picked part], hid }
  var bar = null, panel = null, panelKind = "", draft = null, clip = "", palette = {}; // palette: board -> the colours it uses
  var pathsKey = "";

  function items() { return cur ? cur.items : []; }
  function first() { return items()[0]; }
  function allLocked() { return items().length > 0 && items().every(function (i) { return i.locked; }); }
  function descr() { var it = items(); return it.length > 1 ? it.length + " parts" : it.length ? it[0].n : "a part"; }
  function label(kind, detail) { return partLabel(kind, descr(), detail); }
  function send(msg) { if (cur) host.post(cur.board, msg); }
  function cmd(op, extra, kind, detail) { var m = { c: "cmd", op: op, label: label(kind || op, detail) }; for (var k in extra || {}) m[k] = extra[k]; send(m); }

  // ---- changes -------------------------------------------------------------------------------------------------------------

  function propsFor(p) { return typeof p === "function" ? function (it) { return p(it); } : function () { return p; }; }
  function mapItems(p) { var f = propsFor(p); return items().map(function (it) { return { p: it.p, props: f(it) }; }); }
  var liveTimer = 0, pending = null;
  function live(props) {
    if (!cur) return;
    draft = draft || {};
    Object.keys(typeof props === "function" ? {} : props).forEach(function (k) { draft[k] = props[k]; });
    pending = props;
    clearTimeout(liveTimer);
    liveTimer = setTimeout(function () { send({ c: "apply", live: true, items: mapItems(pending) }); }, 30);
  }
  function keep(kind, detail) {
    clearTimeout(liveTimer);
    if (!cur) { closePanel(false); return; }
    if (draft && Object.keys(draft).length) send({ c: "apply", live: false, label: label(kind, detail), items: mapItems(draft) });
    draft = null;
    closePanel(false);
  }
  function cancel() { clearTimeout(liveTimer); if (draft && Object.keys(draft).length) send({ c: "cancel" }); draft = null; }
  function closePanel(revert) {
    if (revert !== false) cancel();
    if (panel) { panel.remove(); panel = null; }
    panelKind = "";
  }

  // ---- the toolbar ---------------------------------------------------------------------------------------------------------

  function tbtn(icon, text, tint, on, off) {
    var b = el("button", "tk"); b.type = "button"; b.disabled = !!off;
    var i = el("span", "tki"); i.style.color = tint; i.innerHTML = icon; b.appendChild(i); b.appendChild(el("span", "tkl", text));
    b.title = text; b.addEventListener("click", function (e) { e.stopPropagation(); on(); });
    return b;
  }
  function group(name, color, btns) {
    var g = el("div", "tg"); var n = el("div", "tgn", name); n.style.color = color || ""; g.appendChild(n);
    var r = el("div", "tgr"); btns.forEach(function (b) { r.appendChild(b); }); g.appendChild(r); return g;
  }

  function renderBar() {
    var it = items();
    if (!cur || !it.length) { hideBar(); return; }
    if (!bar) { bar = el("div", "nb cv-parts"); bar.setAttribute("role", "toolbar"); bar.setAttribute("aria-label", "Tools for the selected part"); host.vp.appendChild(bar); }
    bar.textContent = "";
    var f = it[0], many = it.length > 1, locked = allLocked();
    var chip = el("div", "npart");
    var l1 = el("div", ""); var dot = el("span", "ndot"); l1.appendChild(dot); l1.appendChild(document.createTextNode(many ? it.length + " parts" : f.n + (f.locked ? " 🔒" : ""))); chip.appendChild(l1);
    chip.appendChild(el("span", "nsz", many ? "Shift-click adds or removes" : Math.round(f.r.w) + "×" + Math.round(f.r.h)));
    bar.appendChild(chip);
    bar.appendChild(group("Look", CORAL, [tbtn(ICON.fill, "Fill", CORAL, function () { togglePanel("fill"); }, locked), tbtn(ICON.corner, "Corner", CORAL, function () { togglePanel("corner"); }, locked), tbtn(ICON.text, "Text", CORAL, function () { togglePanel("text"); }, locked)]));
    bar.appendChild(group("Shape", BLUE, [tbtn(ICON.stack, "Layout", BLUE, function () { togglePanel("layout"); }, locked), tbtn(ICON.size, "Size", BLUE, function () { togglePanel("size"); }, locked)]));
    var own = it.every(function (x) { return x.own; });
    bar.appendChild(group("Guard", LIME, [
      tbtn(own ? ICON.unlock : ICON.lock, own ? "Unlock" : "Lock", LIME, function () { cmd(own ? "unlock" : "lock", null, own ? "unlock" : "lock"); }, false),
      tbtn(ICON.note, "Note", LIME, noteOnPart, false)]));
    if (many) bar.appendChild(group("Line up", AMBER, [tbtn(ICON.align, "Align", AMBER, function () { togglePanel("align"); }, locked)]));
    var mg = el("div", "tg"); mg.appendChild(el("div", "tgn", " ")); var mr = el("div", "tgr"); mr.appendChild(tbtn(ICON.more, "More", "#E6E4EC", function () { togglePanel("more"); }, false)); mg.appendChild(mr); bar.appendChild(mg);
    bar.appendChild(el("div", "ntail"));
    bar.hidden = false;
    reposition();
  }
  function hideBar() { if (bar) bar.hidden = true; }

  // where a part is on the screen of the canvas view
  function screenRect() {
    var it = items(); if (!it.length) return null;
    var b = host.getBoard(cur.board), v = host.getView(); if (!b) return null;
    var x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity;
    it.forEach(function (i) { x0 = Math.min(x0, i.r.x); y0 = Math.min(y0, i.r.y); x1 = Math.max(x1, i.r.x + i.r.w); y1 = Math.max(y1, i.r.y + i.r.h); });
    return { l: (b.x + x0) * v.z + v.x, t: (b.y + y0) * v.z + v.y, r: (b.x + x1) * v.z + v.x, b: (b.y + y1) * v.z + v.y };
  }
  // the part of the canvas view that is on the screen: the pane can be taller than the window, and the toolbar must stay where it can be seen
  function visible() {
    var vr = host.vp.getBoundingClientRect(), vh = host.vp.clientHeight;
    var win = host.vp.ownerDocument.defaultView || window;
    var top = Math.max(0, -vr.top), bot = Math.min(vh, (win.innerHeight || vh) - vr.top);
    var ctx = host.vp.querySelector(".cv-ctx");
    var floor = ctx && !ctx.hidden ? ctx.offsetTop + ctx.offsetHeight + 6 : 0;
    return { top: Math.max(top + 6, floor), bot: Math.max(bot - 6, top + 60) };
  }
  function reposition() {
    var r = screenRect();
    if (!bar || bar.hidden || !r) return;
    var vw = host.vp.clientWidth, tw = bar.offsetWidth, th = bar.offsetHeight, vis = visible();
    var left = Math.max(8, Math.min((r.l + r.r) / 2 - tw / 2, vw - tw - 8));
    var above = r.t - th - 14, below = r.b + 16, top, isBelow = false;
    if (above >= vis.top) top = above;
    else if (below + th <= vis.bot) { top = below; isBelow = true; }
    else top = Math.max(vis.top, Math.min(above, vis.bot - th)); // no room on either side: over the top of the part, on the screen
    top = Math.max(vis.top, Math.min(top, vis.bot - th));
    bar.classList.toggle("is-below", isBelow);
    bar.style.left = left + "px"; bar.style.top = top + "px";
    var tail = bar.querySelector(".ntail"); if (tail) tail.style.display = top === above || isBelow ? "" : "none";
    if (tail) tail.style.left = Math.max(14, Math.min((r.l + r.r) / 2 - left - 6, tw - 26)) + "px";
    if (panel) placePanel();
  }
  function placePanel() {
    if (!panel || !bar) return;
    var vw = host.vp.clientWidth, pw = panel.offsetWidth, ph = panel.offsetHeight, vis = visible();
    var bt = bar.offsetTop, bb = bt + bar.offsetHeight;
    var left = Math.max(8, Math.min(bar.offsetLeft, vw - pw - 8));
    var top = bb + 8;
    if (top + ph > vis.bot) top = bt - ph - 8;           // not enough room under the bar: over it
    if (top < vis.top) top = Math.max(vis.top, vis.bot - ph - 24); // not over it either: as high as it fits on the screen
    panel.style.left = left + "px"; panel.style.top = Math.max(0, top) + "px";
  }
  window.addEventListener("scroll", function () { if (bar && !bar.hidden) reposition(); }, { passive: true });
  window.addEventListener("resize", function () { if (bar && !bar.hidden) reposition(); });

  function noteOnPart() {
    var f = first(), b = host.getBoard(cur.board); if (!f || !b) return;
    var fx = Math.max(0, Math.min(1, (f.r.x + f.r.w / 2) / (cur.doc.w || b.w))), fy = Math.max(0, Math.min(1, (f.r.y + f.r.h / 2) / (cur.doc.h || b.h)));
    host.noteAt(cur.board, fx, fy, f.d);
  }

  // ---- panels -----------------------------------------------------------------------------------------------------------------

  function togglePanel(kind) {
    if (panelKind === kind) { closePanel(true); return; }
    closePanel(true);
    panelKind = kind; draft = null;
    panel = el("div", "fp cv-parts"); panel.setAttribute("role", "dialog");
    var builders = { fill: pFill, corner: pCorner, text: pText, layout: pLayout, size: pSize, spacing: pSpacing, border: pBorder, opacity: pOpacity, image: pImage, style: pStyle, align: pAlign, more: pMore };
    var built = builders[kind]();
    var head = el("div", "fp__h"); head.appendChild(el("b", "", built.title)); head.appendChild(el("span", "fp__d", " · " + descr())); panel.appendChild(head);
    panel.appendChild(built.body);
    if (!built.bare) {
      var foot = el("div", "fp__f");
      var esc = el("button", "fp__b", "Esc"); esc.type = "button"; esc.title = "Drop the change"; esc.addEventListener("click", function () { closePanel(true); });
      var ok = el("button", "fp__b fp__ok", "Keep"); ok.type = "button"; ok.title = "Save the change as a new version"; ok.addEventListener("click", function () { if (built.keep) built.keep(); else keep(built.kind || kind, built.detail ? built.detail() : ""); });
      foot.appendChild(esc); foot.appendChild(ok); panel.appendChild(foot);
    }
    panel.addEventListener("keydown", function (e) { if (e.key === "Escape") { e.stopPropagation(); closePanel(true); } if (e.key === "Enter" && e.target.tagName !== "TEXTAREA" && !built.bare) { e.preventDefault(); if (built.keep) built.keep(); else keep(built.kind || kind, built.detail ? built.detail() : ""); } });
    host.vp.appendChild(panel);
    placePanel();
    var f1 = panel.querySelector("input,select,button.fp__o"); if (f1) try { f1.focus({ preventScroll: true }); } catch (e) {}
  }

  function row(text) { var r = el("div", "fp__r"); if (text) r.appendChild(el("label", "fp__l", text)); for (var i = 1; i < arguments.length; i++) r.appendChild(arguments[i]); return r; }
  function num(value, min, max, step, on, unit) {
    var w = el("span", "fp__n"), i = el("input"); i.type = "number"; i.min = min; i.max = max; i.step = step || 1; i.value = value; i.setAttribute("aria-label", unit || "value");
    i.addEventListener("input", function () { if (i.value !== "") on(+i.value); });
    w.appendChild(i); if (unit) w.appendChild(el("span", "fp__u", unit)); w.input = i; return w;
  }
  function range(value, min, max, step, on) { var i = el("input", "fp__rg"); i.type = "range"; i.min = min; i.max = max; i.step = step || 1; i.value = value; i.addEventListener("input", function () { on(+i.value); }); return i; }
  function seg(options, value, on) {
    var w = el("span", "fp__seg"), btns = [];
    options.forEach(function (o) { var b = el("button", "fp__o" + (o[0] === value ? " is-on" : ""), o[1]); b.type = "button"; b.addEventListener("click", function () { btns.forEach(function (x) { x.classList.remove("is-on"); }); b.classList.add("is-on"); on(o[0]); }); btns.push(b); w.appendChild(b); });
    return w;
  }
  function select(options, value, on) { var s = el("select", "fp__s"); options.forEach(function (o) { var op = el("option", "", o[1]); op.value = o[0]; if (o[0] === value) op.selected = true; s.appendChild(op); }); s.addEventListener("change", function () { on(s.value); }); return s; }
  function px(v) { var n = parseFloat(v); return isNaN(n) ? 0 : Math.round(n * 10) / 10; }

  // a colour: a box for the hex, the system picker, the colours of the board, the site's accent, an eyedropper
  function colourField(value, on) {
    var w = el("div", "fp__c"), top = el("div", "fp__ct");
    var hexIn = el("input", "fp__hex"); hexIn.type = "text"; hexIn.value = value || ""; hexIn.placeholder = "#rrggbb"; hexIn.setAttribute("aria-label", "Colour as hex"); hexIn.spellcheck = false;
    var pick = el("input", "fp__pick"); pick.type = "color"; pick.value = /^#[0-9a-f]{6}$/i.test(value || "") ? value : "#888888"; pick.setAttribute("aria-label", "Pick a colour");
    function set(c, fromHex) { if (!fromHex) hexIn.value = c; if (/^#[0-9a-f]{6}$/i.test(c)) pick.value = c; if (validStyle("color", c) !== null) on(c); }
    hexIn.addEventListener("input", function () { var v = hexIn.value.trim(); if (/^#[0-9a-f]{3}$|^#[0-9a-f]{6}$/i.test(v)) set(toHex(v), true); });
    pick.addEventListener("input", function () { set(pick.value, false); });
    top.appendChild(pick); top.appendChild(hexIn);
    if (window.EyeDropper) { var ey = el("button", "fp__b", "Eyedropper"); ey.type = "button"; ey.title = "Pick a colour from anywhere on the screen"; ey.addEventListener("click", function () { new window.EyeDropper().open().then(function (r) { set(toHex(r.sRGBHex) || r.sRGBHex, false); }, function () {}); }); top.appendChild(ey); }
    w.appendChild(top);
    var chips = el("div", "fp__chips"); w.appendChild(chips);
    function chipSet(title, list) {
      if (!list.length) return;
      chips.appendChild(el("span", "fp__ct2", title));
      list.forEach(function (c) { var b = el("button", "fp__chip"); b.type = "button"; b.style.background = c; b.title = c; b.setAttribute("aria-label", title + " " + c); b.addEventListener("click", function () { set(c, false); }); chips.appendChild(b); });
    }
    function fill() {
      chips.textContent = "";
      chipSet("This board's colours", (palette[cur ? cur.board : ""] || []).slice(0, 10));
      var ac = host.accent(); chipSet("The site's accent", ac ? [ac] : []);
      var clear = el("button", "fp__b", "Clear"); clear.type = "button"; clear.title = "No colour (transparent)"; clear.addEventListener("click", function () { hexIn.value = ""; on("transparent"); }); chips.appendChild(clear);
    }
    fill(); w.refill = fill; w.hex = hexIn; refillers.push(fill);
    if (cur) send({ c: "cmd", op: "colors" });
    return w;
  }
  var refillers = [];

  function pFill() {
    var f = first(), target = f && !f.fill && f.text ? "color" : "background-color"; // a part with a background changes that; bare text changes its colour
    var body = el("div", ""), holder = el("div", "");
    function build() {
      holder.textContent = "";
      var cur0 = target === "color" ? f.ink : f.fill;
      holder.appendChild(colourField(cur0 || "", function (c) { var p = {}; p[target] = c; live(p); detailV = c; }));
    }
    var detailV = "";
    body.appendChild(row("", seg([["background-color", "Background"], ["color", "Text"]], target, function (v) { cancel(); draft = null; target = v; build(); })));
    body.appendChild(holder); build();
    return { title: "Fill", body: body, detail: function () { return detailV; }, kind: "fill", keep: function () { keep(target === "color" ? "textcolor" : "fill", detailV); } };
  }
  function pCorner() {
    var f = first(), r0 = px(f.st.borderTopLeftRadius), mode = "all", cv = { all: r0, tl: r0, tr: px(f.st.borderTopRightRadius), br: px(f.st.borderBottomRightRadius), bl: px(f.st.borderBottomLeftRadius) };
    var body = el("div", ""), area = el("div", "");
    function apply() { var v = cornerValue(mode === "all" ? { all: cv.all } : cv); live({ "border-radius": v }); detailV = v; }
    var detailV = r0 + "px";
    function build() {
      area.textContent = "";
      if (mode === "all") {
        var n = num(cv.all, 0, 200, 1, function (v) { cv.all = v; rg.value = Math.min(48, v); apply(); }, "px");
        var rg = range(Math.min(48, cv.all), 0, 48, 1, function (v) { cv.all = v; n.input.value = v; apply(); });
        area.appendChild(row("Radius", rg, n));
      } else {
        [["tl", "Top left"], ["tr", "Top right"], ["bl", "Bottom left"], ["br", "Bottom right"]].forEach(function (k) { area.appendChild(row(k[1], num(cv[k[0]], 0, 200, 1, function (v) { cv[k[0]] = v; apply(); }, "px"))); });
      }
    }
    body.appendChild(row("", seg([["all", "All corners"], ["each", "Each corner"]], mode, function (v) { mode = v; build(); })));
    body.appendChild(area); build();
    var pre = row("", (function () { var a = el("button", "fp__b", "Square"), b = el("button", "fp__b", "Pill"); a.type = b.type = "button"; a.addEventListener("click", function () { mode = "all"; cv.all = 0; build(); apply(); }); b.addEventListener("click", function () { live({ "border-radius": "9999px" }); detailV = "pill"; }); var s = el("span", "fp__seg"); s.appendChild(a); s.appendChild(b); return s; })());
    body.appendChild(pre);
    return { title: "Corner", body: body, detail: function () { return detailV; }, kind: "corner" };
  }
  function pText() {
    var f = first(), st = f.st, o = { size: px(st.fontSize), weight: String(Math.round(+st.fontWeight / 100) * 100 || 400), align: st.textAlign === "start" ? "left" : st.textAlign, lh: st.lineHeight === "normal" ? "" : px(st.lineHeight) / (px(st.fontSize) || 16), ls: st.letterSpacing === "normal" ? 0 : px(st.letterSpacing) };
    var body = el("div", "");
    body.appendChild(row("Size", num(o.size, 6, 200, 1, function (v) { live({ "font-size": v + "px" }); }, "px")));
    body.appendChild(row("Weight", select([["400", "Regular"], ["500", "Medium"], ["600", "Semi-bold"], ["700", "Bold"]], o.weight, function (v) { live({ "font-weight": v }); })));
    body.appendChild(row("Align", seg([["left", "Left"], ["center", "Centre"], ["right", "Right"]], o.align, function (v) { live({ "text-align": v }); })));
    body.appendChild(row("Font", select(FONTS.map(function (x) { return [x[1], x[0]]; }), "", function (v) { live({ "font-family": v }); })));
    body.appendChild(row("Line height", num(o.lh ? Math.round(o.lh * 10) / 10 : 1.4, 0.8, 3, 0.1, function (v) { live({ "line-height": String(v) }); })));
    body.appendChild(row("Letter spacing", num(o.ls, -5, 20, 0.5, function (v) { live({ "letter-spacing": v + "px" }); }, "px")));
    body.appendChild(row("Colour", colourField(f.ink || "", function (c) { live({ color: c }); })));
    return { title: "Text", body: body, kind: "text", detail: function () { return ""; } };
  }
  function pLayout() {
    var f = first(), st = f.st, flex = /flex/.test(st.display);
    var o = { dir: st.flexDirection === "column" ? "column" : "row", gap: px(st.gap), ai: st.alignItems === "normal" ? "stretch" : st.alignItems, jc: st.justifyContent === "normal" ? "flex-start" : st.justifyContent, wrap: st.flexWrap === "wrap" };
    var body = el("div", "");
    function flexOn(extra) { var p = { display: "flex" }; for (var k in extra) p[k] = extra[k]; live(p); }
    var presets = el("span", "fp__seg");
    [["Stack", { "flex-direction": "column" }], ["Row", { "flex-direction": "row" }], ["Grid of 2", { display: "grid", "grid-template-columns": "repeat(2, 1fr)" }], ["Grid of 3", { display: "grid", "grid-template-columns": "repeat(3, 1fr)" }]].forEach(function (pr) {
      var b = el("button", "fp__o", pr[0]); b.type = "button"; b.addEventListener("click", function () { var p = { display: "flex" }; for (var k in pr[1]) p[k] = pr[1][k]; live(p); }); presets.appendChild(b);
    });
    body.appendChild(row("", presets));
    body.appendChild(row("Direction", seg([["row", "Row"], ["column", "Column"]], o.dir, function (v) { flexOn({ "flex-direction": v }); })));
    body.appendChild(row("Gap", num(o.gap, 0, 200, 1, function (v) { live({ gap: v + "px" }); }, "px")));
    body.appendChild(row("Align", select([["stretch", "Stretch"], ["flex-start", "Start"], ["center", "Centre"], ["flex-end", "End"], ["baseline", "Baseline"]], o.ai, function (v) { flexOn({ "align-items": v }); })));
    body.appendChild(row("Justify", select([["flex-start", "Start"], ["center", "Centre"], ["flex-end", "End"], ["space-between", "Space between"], ["space-around", "Space around"], ["space-evenly", "Space evenly"]], o.jc, function (v) { flexOn({ "justify-content": v }); })));
    var wr = el("input"); wr.type = "checkbox"; wr.checked = o.wrap; wr.addEventListener("change", function () { flexOn({ "flex-wrap": wr.checked ? "wrap" : "nowrap" }); });
    var wl = el("label", "fp__chk"); wl.appendChild(wr); wl.appendChild(document.createTextNode(" Wrap")); body.appendChild(row("", wl));
    if (!f.cont) body.appendChild(el("div", "fp__hint", "This part has nothing inside it: layout is for a part that holds others."));
    return { title: "Layout", body: body, kind: "layout", detail: function () { return ""; } };
  }
  function pSize() {
    var f = first(), body = el("div", ""), r = f.r, ratio = false;
    var sizeOf = function (axis) { return Math.round(axis === "w" ? r.w : r.h); };
    function dim(axis, name) {
      var prop = axis === "w" ? "width" : "height", mode = "fixed", n;
      var sel = select([["fixed", "Fixed"], ["fill", "Fill"], ["hug", "Hug content"]], "fixed", function (v) { mode = v; send1(); });
      n = num(sizeOf(axis), 1, 4000, 1, function () { mode = "fixed"; sel.value = "fixed"; send1(); }, "px");
      function send1() { var v = mode === "fill" ? "100%" : mode === "hug" ? (axis === "w" ? "fit-content" : "auto") : n.input.value + "px"; var p = {}; p[prop] = v; if (ratio && axis === "w" && mode === "fixed") { p.height = "auto"; p["aspect-ratio"] = Math.round(r.w) + " / " + Math.round(r.h); } live(p); }
      return row(name, sel, n);
    }
    body.appendChild(dim("w", "Width")); body.appendChild(dim("h", "Height"));
    var kr = el("input"); kr.type = "checkbox"; kr.addEventListener("change", function () { ratio = kr.checked; if (ratio) live({ height: "auto", "aspect-ratio": Math.round(r.w) + " / " + Math.round(r.h) }); else live({ "aspect-ratio": "auto" }); });
    var kl = el("label", "fp__chk"); kl.appendChild(kr); kl.appendChild(document.createTextNode(" Keep the ratio")); body.appendChild(row("", kl));
    return { title: "Size", body: body, kind: "size", detail: function () { var d = draft || {}; return d.width || d.height || ""; } };
  }
  function pSpacing() {
    var f = first(), st = f.st, linked = false, body = el("div", ""), inputs = {};
    function box(title, prefix, vals) {
      var wrap = el("div", "fp__box"); wrap.appendChild(el("div", "fp__bt", title));
      [["Top", "Top"], ["Right", "Right"], ["Bottom", "Bottom"], ["Left", "Left"]].forEach(function (s, i) {
        var key = prefix + "-" + s[0].toLowerCase();
        var n = num(px(vals[prefix + s[0]]), prefix === "margin" ? -200 : 0, 400, 1, function (v) {
          if (linked) { ["top", "right", "bottom", "left"].forEach(function (x) { var k = prefix + "-" + x; inputs[k].input.value = v; }); live((function () { var p = {}; p[prefix] = v + "px"; return p; })()); }
          else { var p = {}; p[key] = v + "px"; live(p); }
        }, "px");
        inputs[key] = n; var r = row(s[1], n); wrap.appendChild(r);
      });
      return wrap;
    }
    var lk = el("input"); lk.type = "checkbox"; lk.addEventListener("change", function () { linked = lk.checked; });
    var ll = el("label", "fp__chk"); ll.appendChild(lk); ll.appendChild(document.createTextNode(" Same on all four sides")); body.appendChild(row("", ll));
    var two = el("div", "fp__two"); two.appendChild(box("Padding", "padding", { paddingTop: st.paddingTop, paddingRight: st.paddingRight, paddingBottom: st.paddingBottom, paddingLeft: st.paddingLeft })); two.appendChild(box("Margin", "margin", { marginTop: st.marginTop, marginRight: st.marginRight, marginBottom: st.marginBottom, marginLeft: st.marginLeft })); body.appendChild(two);
    body.appendChild(row("Gap", num(px(st.gap), 0, 200, 1, function (v) { live({ gap: v + "px" }); }, "px")));
    return { title: "Spacing", body: body, kind: "spacing", detail: function () { return ""; } };
  }
  function pBorder() {
    var f = first(), st = f.st, body = el("div", "");
    var bw = px(st.borderTopWidth), bs = st.borderTopStyle === "none" ? "solid" : st.borderTopStyle;
    body.appendChild(row("Width", num(bw, 0, 40, 1, function (v) { live({ "border-width": v + "px", "border-style": bs }); }, "px")));
    body.appendChild(row("Style", select([["solid", "Solid"], ["dashed", "Dashed"], ["dotted", "Dotted"], ["double", "Double"], ["none", "None"]], bs, function (v) { bs = v; live({ "border-style": v, "border-width": (bw || 1) + "px" }); })));
    body.appendChild(row("Colour", colourField(toHex(st.borderTopColor) || "", function (c) { live({ "border-color": c, "border-style": bs, "border-width": (bw || 1) + "px" }); })));
    var custom = el("input", "fp__hex"); custom.type = "text"; custom.placeholder = "0 4px 12px rgba(0,0,0,0.2)"; custom.setAttribute("aria-label", "Custom shadow");
    custom.addEventListener("input", function () { if (validStyle("box-shadow", custom.value) !== null) live({ "box-shadow": custom.value.trim() }); });
    body.appendChild(row("Shadow", seg([["none", "None"], ["soft", "Soft"], ["lifted", "Lifted"]], "", function (v) { live({ "box-shadow": SHADOWS[v] }); })));
    body.appendChild(row("Custom", custom));
    return { title: "Border and shadow", body: body, kind: "border", detail: function () { return ""; } };
  }
  function pOpacity() {
    var f = first(), v0 = Math.round((+f.st.opacity || 1) * 100), body = el("div", "");
    var n = num(v0, 0, 100, 1, function (v) { rg.value = v; live({ opacity: String(v / 100) }); }, "%");
    var rg = range(v0, 0, 100, 1, function (v) { n.input.value = v; live({ opacity: String(v / 100) }); });
    body.appendChild(row("Opacity", rg, n));
    return { title: "Opacity", body: body, kind: "opacity", detail: function () { var d = draft && draft.opacity; return d ? Math.round(d * 100) + "%" : ""; } };
  }
  function pImage() {
    var f = first(), body = el("div", ""), data = "", fit = "cover", alt = "";
    var file = el("input"); file.type = "file"; file.accept = "image/png,image/jpeg,image/gif,image/webp"; file.setAttribute("aria-label", "Choose a picture");
    var msg = el("div", "fp__hint", "A PNG, JPEG, GIF or WebP of up to 200 KB. It is kept inside the board, as data.");
    file.addEventListener("change", function () {
      var fl = file.files && file.files[0]; if (!fl) return;
      if (!/^image\/(png|jpeg|gif|webp)$/.test(fl.type)) { msg.textContent = "That is not a PNG, JPEG, GIF or WebP picture."; data = ""; return; }
      if (fl.size > 200 * 1024) { msg.textContent = "That picture is " + Math.round(fl.size / 1024) + " KB: the limit is 200 KB. Shrink it first."; data = ""; return; }
      var rd = new FileReader(); rd.onload = function () { data = String(rd.result); msg.textContent = fl.name + " is ready: press Keep."; }; rd.readAsDataURL(fl);
    });
    body.appendChild(row("Picture", file)); body.appendChild(msg);
    body.appendChild(row("Fit", seg([["cover", "Cover"], ["contain", "Contain"]], fit, function (v) { fit = v; })));
    if (f.img) { var a = el("input", "fp__hex"); a.type = "text"; a.maxLength = 120; a.placeholder = "Describe the picture"; a.value = ""; a.addEventListener("input", function () { alt = a.value; }); body.appendChild(row("Alt text", a)); }
    return { title: "Picture", body: body, keep: function () { if (!data) { msg.textContent = "Choose a picture first."; return; } var m = { p: f.p, data: data, fit: fit }; if (f.img) m.alt = alt; cmd("image", m, "image", ""); closePanel(false); } };
  }
  function pStyle() {
    var f = first(), body = el("div", ""), prop = "background-color", name = (f.n.replace(/[^a-z0-9]+/gi, "-").replace(/^-+|-+$/g, "").toLowerCase() || "style").slice(0, 20);
    if (!/^[a-z]/.test(name)) name = "s-" + name;
    var cur0 = function () { return prop === "background-color" ? f.fill : prop === "color" ? f.ink : prop === "font-size" ? f.st.fontSize : f.st.borderTopLeftRadius; };
    var show = el("div", "fp__hint", "");
    function sh() { show.textContent = "Now: " + (cur0() || "no colour") + ". One change to the style then updates every part with that value."; }
    body.appendChild(row("Make a style of", select([["background-color", "Fill"], ["color", "Text colour"], ["font-size", "Font size"], ["border-radius", "Corner radius"]], prop, function (v) { prop = v; sh(); })));
    var nm = el("input", "fp__hex"); nm.type = "text"; nm.value = name; nm.maxLength = 24; nm.setAttribute("aria-label", "Name of the style"); nm.addEventListener("input", function () { name = nm.value.toLowerCase().replace(/[^a-z0-9-]/g, "-"); });
    body.appendChild(row("Name", nm)); body.appendChild(show); sh();
    return { title: "Make it a style", body: body, keep: function () { if (!/^[a-z][a-z0-9-]{0,24}$/.test(name)) { show.textContent = "A name is lower case letters, digits and hyphens, starting with a letter."; return; } cmd("makevar", { p: f.p, prop: prop, name: name }, "style", "--" + name); closePanel(false); } };
  }
  function pAlign() {
    var body = el("div", ""), it = items();
    function rects() { return it.map(function (x) { return { p: x.p, x: x.r.x, y: x.r.y, w: x.r.w, h: x.r.h }; }); }
    function doShift(offs, kind) { cmd("shift", { items: offs.filter(function (o) { return o.dx || o.dy; }) }, kind, ""); closePanel(false); }
    var al = el("div", "fp__list");
    [["left", "Align left edges"], ["hcenter", "Align centres, across"], ["right", "Align right edges"], ["top", "Align top edges"], ["vcenter", "Align centres, down"], ["bottom", "Align bottom edges"]].forEach(function (a) { var b = el("button", "fp__item", a[1]); b.type = "button"; b.addEventListener("click", function () { doShift(alignOffsets(rects(), a[0]), "align"); }); al.appendChild(b); });
    var d1 = el("button", "fp__item", "Spread evenly, across"), d2 = el("button", "fp__item", "Spread evenly, down");
    d1.type = d2.type = "button"; d1.disabled = d2.disabled = it.length < 3; d1.title = d2.title = it.length < 3 ? "Needs three or more parts" : "";
    d1.addEventListener("click", function () { doShift(distributeOffsets(rects(), "h"), "distribute"); }); d2.addEventListener("click", function () { doShift(distributeOffsets(rects(), "v"), "distribute"); });
    al.appendChild(d1); al.appendChild(d2);
    var w1 = el("button", "fp__item", "Same width as the first"), h1 = el("button", "fp__item", "Same height as the first");
    w1.type = h1.type = "button";
    w1.addEventListener("click", function () { var v = Math.round(it[0].r.w) + "px"; send({ c: "apply", live: false, label: label("size", "same width"), items: it.map(function (x) { return { p: x.p, props: { width: v } }; }) }); closePanel(false); });
    h1.addEventListener("click", function () { var v = Math.round(it[0].r.h) + "px"; send({ c: "apply", live: false, label: label("size", "same height"), items: it.map(function (x) { return { p: x.p, props: { height: v } }; }) }); closePanel(false); });
    al.appendChild(w1); al.appendChild(h1);
    body.appendChild(al);
    return { title: "Line up", body: body, bare: true };
  }
  function pMore() {
    var f = first(), body = el("div", "fp__list"), many = items().length > 1;
    function item(text, on, off, hint) { var b = el("button", "fp__item", text); b.type = "button"; b.disabled = !!off; if (hint) b.title = hint; b.addEventListener("click", function () { closePanel(true); on(); }); body.appendChild(b); return b; }
    var lk = allLocked();
    item("Duplicate part", function () { cmd("duplicate"); }, lk);
    item("Delete part", function () { if (window.confirm("Delete " + descr() + " from the board? Undo brings it back.")) cmd("delete"); }, lk);
    item("Wrap in a container", function () { cmd("wrap"); }, lk);
    item("Copy style", function () { send({ c: "cmd", op: "copystyle" }); }, many, many ? "Pick one part" : "");
    item("Paste style", function () { var props = parseInline(clip); if (!Object.keys(props).length) { host.say("Copy the style of a part first.", 3000); return; } send({ c: "apply", live: false, label: label("paste", ""), items: mapItems(props) }); }, lk);
    item("Select parent", function () { cmd("parent"); }, many);
    item("Spacing…", function () { togglePanel("spacing"); }, lk);
    item("Border and shadow…", function () { togglePanel("border"); }, lk);
    item("Opacity…", function () { togglePanel("opacity"); }, lk);
    item("Picture…", function () { togglePanel("image"); }, lk || many);
    item("Bring forward", function () { cmd("order", { dir: "forward" }, "order", "forward"); }, lk);
    item("Send back", function () { cmd("order", { dir: "backward" }, "order", "back"); }, lk);
    item("To the front", function () { cmd("order", { dir: "front" }, "order", "front"); }, lk);
    item("To the back", function () { cmd("order", { dir: "back" }, "order", "back"); }, lk);
    item("Make it a style…", function () { togglePanel("style"); }, lk || many);
    item("Hide part", function () { cmd("hide"); }, lk);
    item("Show all hidden parts" + (cur && cur.hid ? " (" + cur.hid + ")" : ""), function () { send({ c: "cmd", op: "showall", label: label("show", "") }); }, !(cur && cur.hid));
    item("Ask Genie about this part", function () { host.askGenie(); });
    item("Copy selector", function () { try { navigator.clipboard.writeText(f.p); host.say("Copied: " + f.p, 2500); } catch (e) { host.say(f.p, 6000); } });
    body.appendChild(el("div", "fp__hint", "Measure: hold Alt and point at another part to see the distance."));
    return { title: "More", body: body, bare: true };
  }

  // ---- what the boards say -----------------------------------------------------------------------------------------------

  function onMessage(id, d) {
    if (d.c === "part") {
      var key = (d.items || []).map(function (i) { return i.p; }).join("|");
      cur = { board: id, items: d.items || [], hid: d.hid || 0, doc: d.doc || { w: 0, h: 0 } };
      if (key !== pathsKey) { if (panel && panelKind !== "more") closePanel(true); else if (panel) closePanel(false); pathsKey = key; }
      renderBar();
      return true;
    }
    if (d.c === "refused") {
      host.say(d.why === "locked" ? "That part is locked. Unlock it first." : d.why === "image" ? "The picture must be a PNG, JPEG, GIF or WebP of at most 200 KB." : "That value is not allowed.", 5000);
      return true;
    }
    if (d.c === "clip") { clip = String(d.style || ""); host.say(clip ? "Style copied." : "That part has no style of its own to copy.", 2500); return true; }
    if (d.c === "colors") { palette[id] = (d.list || []).map(function (c) { return String(c); }); refillers.forEach(function (fn) { try { fn(); } catch (e) {} }); return true; }
    return false;
  }

  return {
    onMessage: onMessage,
    reposition: reposition,
    current: function () { return cur; },
    // leaving Select part mode, or another board: the toolbar goes
    clear: function () { closePanel(true); cur = null; pathsKey = ""; hideBar(); },
    // the paths picked, to pick them again after the board reloaded
    paths: function () { return items().map(function (i) { return i.p; }); },
    partName: function () { var it = items(); return it.length ? it.slice(0, 3).map(function (i) { return i.d; }).join(" + ") : ""; },
    shortName: function () { var it = items(); return it.length ? (it.length > 1 ? it.length + " parts" : it[0].n) : ""; },
    keyEsc: function () { if (panel) { closePanel(true); return true; } return false; },
  };
}
