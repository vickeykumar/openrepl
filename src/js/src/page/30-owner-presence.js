// A shared session and its owner (docs/lld/11-workspace-sync.md, "The owner is not there").
//
// Part of the page script. The owner's page says "I am here" in Firebase, next to the code and the language of the session
// (editor_values/<id>/owner); a collaborator's page watches it. When the owner has left, or nobody was there when the link
// was opened, or the session has ended, the collaborator is told once in a popup, and a bar stays above the editor.
// Nothing is stored about who the owner is: only whether the page is open, and when it last said so.

window.OwnerAway = { over: over, active: active, hostSession: hostSession, stopHosting: stopHosting, watch: watch, beforeInit: beforeInit, state: function () { return state; } };

var HEARTBEAT_MS = 25000; // the owner's page says it is here this often
var GRACE_MS = 8000; // offline this long before a collaborator is told (a reload of the owner's page is not announced)
var STALE_MS = 150000; // a page that is open but has not said so for this long (a hidden tab is slowed down by the browser)
var BACK_RELOAD_MS = 1500;

var state = { away: false, ended: false, kind: "", seenOnline: false, dismissed: false, blanked: false, mins: 0 };
var warned = false;

function active() { return state.away || state.ended; }
// the session is over (ended, expired, or gone): a collaborator's page then writes nothing
function over() { return !!state.ended; }
function say(text, type) { if (window.notify) window.notify(text, { type: type || "info", timeout: 4000 }); }
function stamp() { return firebase.database.ServerValue.TIMESTAMP; }

// ---- the owner's page -------------------------------------------------------------------------------------------------

var hosting = false;
var hostStopped = false;
var hostRef = null;
var hostTimer = 0;

// The owner's page stops saying it is here (the session was ended): Firebase is not asked to write "offline" any more, and the
// page does not write it when it goes, so that nothing of the session is left behind.
function stopHosting() {
  hostStopped = true;
  clearInterval(hostTimer);
  if (hostRef) { try { hostRef.onDisconnect().cancel(); } catch (e) { /* not essential */ } }
}

function hostSession(db, id) {
  if (hosting || !id) return;
  hosting = true;
  var ref = db.ref("editor_values/" + id + "/owner");
  hostRef = ref;
  var connected = false;
  // when the connection goes (a closed tab, a crash, a lost network), Firebase itself writes "offline"
  db.ref(".info/connected").on("value", function (snap) {
    connected = snap.val() === true;
    if (!connected || hostStopped) return;
    ref.onDisconnect().set({ online: false, at: stamp() }).then(function () {
      ref.set({ online: true, at: stamp() });
    }).catch(function (e) { console.error("owner presence:", e); });
  });
  hostTimer = setInterval(function () {
    if (connected && !hostStopped) ref.update({ online: true, at: stamp() }).catch(function () {});
  }, HEARTBEAT_MS);
  window.addEventListener("pagehide", function () {
    if (connected && !hostStopped) ref.set({ online: false, at: stamp() });
  });
}

// ---- a collaborator's page --------------------------------------------------------------------------------------------

var watching = false;
var shareInfo = null; // editor_values/<id>/share: {endsAt, mins, state}, written by the owner's page (31-share-session.js)
var graceTimer = 0;
var offsetMs = 0;
var last = null; // the record as it was last read

// What the link says before the editor is filled: a link that has ended or run out shows no code. cb is told the content to use.
function beforeInit(db, id, content, cb) {
  db.ref(".info/serverTimeOffset").once("value", function (o) {
    offsetMs = Number(o.val()) || 0;
    db.ref("editor_values/" + id + "/share").once("value", function (snap) {
      shareInfo = snap.val();
      var over = linkOver();
      if (over) {
        state.blanked = true;
        setOver(over);
      }
      watch(db, id, over ? null : content);
      cb(over ? "" : content);
    }, function () {
      watch(db, id, content);
      cb(content);
    });
  });
}

// "" while the link works, else why not: "stopped" (the owner ended it) or "expired" (the time ran out)
function linkOver() {
  if (!shareInfo) return "";
  if (shareInfo.state === "ended") return "stopped";
  if (shareInfo.state === "expired") return "expired";
  var at = Number(shareInfo.endsAt) || 0;
  if (at && Date.now() + offsetMs >= at) return "expired";
  return "";
}

function setOver(kind) {
  if (state.ended) return;
  state.ended = true;
  state.away = false;
  state.kind = kind;
  state.mins = Number(shareInfo && shareInfo.mins) || 0;
  clearTimeout(graceTimer);
  graceTimer = 0;
  state.dismissed = false;
  lockPage();
  drawBar();
  showDialog();
  // the owner's page takes the records away when it can; this page does it too a little later, so that nothing of a link that
  // is over stays in Firebase because its owner has gone
  var wipeId = window.dbpath;
  setTimeout(function () { if (window.ShareSession && window.ShareSession.wipe) window.ShareSession.wipe(wipeId); }, 15000);
}

function watch(db, id, content) {
  if (watching || !id) return;
  watching = true;
  var ref = db.ref("editor_values/" + id + "/owner");
  db.ref(".info/serverTimeOffset").on("value", function (s) { offsetMs = Number(s.val()) || 0; });
  // the end of the link: the owner ended it, or the time runs out
  db.ref("editor_values/" + id + "/share").on("value", function (snap) {
    shareInfo = snap.val();
    var over = linkOver();
    if (over) setOver(over);
    else checkSoon();
  });
  // nothing is kept under this id any more: the session has ended
  if (content === null || content === undefined) {
    ref.once("value", function (snap) {
      if (!snap.exists()) showEnded();
    });
  }
  ref.on("value", function (snap) {
    last = snap.val();
    evaluate();
  });
  setInterval(evaluate, 15000);
  installRunGuard();
}

// five minutes before the end, once
function checkSoon() {
  if (!shareInfo || warned || state.ended) return;
  var ms = (Number(shareInfo.endsAt) || 0) - (Date.now() + offsetMs);
  if (ms > 0 && ms <= 5 * 60000) {
    warned = true;
    say("This session ends in " + Math.ceil(ms / 60000) + " minutes.", "info");
  }
}

function ownerOnline() {
  if (!last || last.online !== true) return false;
  var at = Number(last.at) || 0;
  return !at || Date.now() + offsetMs - at < STALE_MS;
}

function evaluate() {
  if (state.ended) return;
  var over = linkOver();
  if (over) { setOver(over); return; }
  checkSoon();
  // an owner page of an older version writes nothing: a missing record says nothing, and nothing is shown
  if (last === null) return;
  if (ownerOnline()) {
    clearTimeout(graceTimer);
    graceTimer = 0;
    state.seenOnline = true;
    if (state.away) back();
    return;
  }
  if (state.away || graceTimer) return;
  graceTimer = setTimeout(function () {
    graceTimer = 0;
    if (!ownerOnline()) away();
  }, GRACE_MS);
}

// ---- what the collaborator sees ---------------------------------------------------------------------------------------

var dialog = null;
var bar = null;

function away() {
  state.away = true;
  state.kind = state.seenOnline ? "left" : "nobody";
  state.dismissed = false;
  drawBar();
  showDialog();
}

function back() {
  state.away = false;
  state.dismissed = false;
  closeDialog();
  drawBar();
  say("The owner is back. Reconnecting…", "success");
  // the page loads again, so the code, the files and the terminal are the owner's, as they are now
  setTimeout(function () { location.reload(); }, BACK_RELOAD_MS);
}

function showEnded() {
  if (state.ended) return;
  state.ended = true;
  state.kind = "ended";
  lockPage();
  drawBar();
  showDialog();
}

function words() {
  if (state.kind === "left") {
    return {
      title: "The owner has left this session",
      body: "You are looking at the code as they left it. Changes you make are not shared any more, and the terminal and the files no longer follow them.",
      hint: "If the owner comes back, this notice goes away by itself."
    };
  }
  if (state.kind === "nobody") {
    return {
      title: "Nobody is hosting this session",
      body: "The owner is not online right now, so you see the last code they shared. You can keep reading it, or copy it to your own editor.",
      hint: "The link keeps working: when the owner opens their editor again you are connected."
    };
  }
  if (state.kind === "stopped") {
    return {
      title: "The owner ended this session",
      body: state.blanked ? "There is nothing to show: the code is not kept after the end. Ask the owner for a new link, or start an editor of your own." : "You see the code as it was when they ended it. Your changes stay in this page and are not shared.",
      hint: ""
    };
  }
  if (state.kind === "expired") {
    var mins = state.mins ? "It was set to work for " + (state.mins % 1440 === 0 ? (state.mins === 1440 ? "24 hours" : (state.mins / 1440) + " days") : state.mins < 60 ? state.mins + " minutes" : state.mins === 60 ? "1 hour" : state.mins % 60 === 0 ? (state.mins / 60) + " hours" : state.mins + " minutes") + ". " : "";
    return {
      title: "This link has expired",
      body: mins + (state.blanked ? "The code is not kept after the end. Ask the owner for a new link, or start an editor of your own." : "Ask the owner for a new link, or copy the code you see to your own editor."),
      hint: ""
    };
  }
  return {
    title: "This shared session has ended",
    body: "Its code is no longer kept, so there is nothing to show. Ask the owner for a new link, or start an editor of your own.",
    hint: ""
  };
}

function buildDialog() {
  if (dialog) return dialog;
  dialog = document.createElement("div");
  dialog.className = "unsaved-backdrop";
  dialog.id = "owner-away";
  dialog.hidden = true;
  dialog.innerHTML =
    '<div class="unsaved-dialog" role="alertdialog" aria-modal="true" aria-labelledby="owner-away-title" aria-describedby="owner-away-body">' +
    '<h2 id="owner-away-title"></h2><p id="owner-away-body"></p><div class="unsaved-dialog__buttons" id="owner-away-buttons"></div>' +
    '<p class="unsaved-dialog__hint" id="owner-away-hint"></p></div>';
  document.body.appendChild(dialog);
  dialog.addEventListener("keydown", function (e) {
    if (e.key === "Escape") { e.preventDefault(); dismiss(); }
  });
  return dialog;
}

function dlgButton(label, primary, onClick) {
  var b = document.createElement("button");
  b.type = "button";
  b.className = "unsaved-dialog__btn" + (primary ? " is-primary" : "");
  b.textContent = label;
  b.addEventListener("click", onClick);
  return b;
}

function showDialog() {
  if (state.dismissed) return;
  var d = buildDialog();
  var w = words();
  d.querySelector("#owner-away-title").textContent = w.title;
  d.querySelector("#owner-away-body").textContent = w.body;
  d.querySelector("#owner-away-hint").textContent = w.hint;
  var buttons = d.querySelector("#owner-away-buttons");
  buttons.textContent = "";
  var first;
  if (state.kind === "ended" || state.blanked) {
    first = dlgButton("Start my own editor", true, function () { window.open("/", "_blank", "noopener"); dismiss(); });
    buttons.appendChild(first);
    buttons.appendChild(dlgButton("Close", false, dismiss));
  } else if (state.kind === "stopped" || state.kind === "expired") {
    first = dlgButton("Copy it to my own editor", true, copyToMyEditor);
    buttons.appendChild(first);
    buttons.appendChild(dlgButton("Close", false, dismiss));
  } else {
    first = dlgButton("Copy it to my own editor", true, copyToMyEditor);
    buttons.appendChild(first);
    buttons.appendChild(dlgButton("Stay and wait for them", false, dismiss));
    buttons.appendChild(dlgButton("Close", false, dismiss));
  }
  var returnFocus = document.activeElement;
  d.hidden = false;
  d._returnFocus = returnFocus;
  first.focus();
}

function closeDialog() {
  if (!dialog) return;
  dialog.hidden = true;
}

function dismiss() {
  state.dismissed = true;
  closeDialog();
  var f = dialog && dialog._returnFocus;
  if (f && typeof f.focus === "function") { try { f.focus(); } catch (e) { /* the focus is not essential */ } }
}

// The code and the language that are on the page, in the visitor's own editor and workspace: a code link (the same as
// "Create code link" in the Share popover) opened in a new tab. The owner's workspace and files are not touched.
function copyToMyEditor() {
  var ed = document.getElementById("editor");
  var code = ed && ed.env && ed.env.editor ? ed.env.editor.getValue() : "";
  if (!code.trim()) { say("There is no code on the page to copy.", "info"); return; }
  // the tab is opened now, by the click, and told where to go when the link is made (a popup blocker would stop a later one)
  var tab = window.open("", "_blank");
  fetch("/snippet", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ lang: $("#optionlist").val(), code: code })
  })
    .then(function (r) { return r.json().then(function (body) { return { ok: r.ok, body: body }; }); })
    .then(function (res) {
      if (!res.ok || !res.body.url) throw new Error(res.body && res.body.error ? res.body.error : "no link");
      if (tab) tab.location.href = res.body.url; else window.open(res.body.url, "_blank");
      dismiss();
    })
    .catch(function () {
      if (tab) tab.close();
      say("Could not copy it right now. Try again in a moment.", "error");
    });
}

function drawBar() {
  if (!active()) {
    if (bar) bar.hidden = true;
    return;
  }
  if (state.ended) { if (bar) bar.hidden = true; return; }
  if (!bar) {
    var ide = document.getElementById("ide");
    var header = ide && ide.querySelector(".editor-header");
    if (!ide || !header) return;
    bar = document.createElement("div");
    bar.className = "unsaved-bar";
    bar.id = "owner-away-bar";
    bar.setAttribute("role", "status");
    ide.insertBefore(bar, header);
  }
  bar.textContent = "";
  var row = document.createElement("div");
  row.className = "unsaved-bar__row";
  var dot = document.createElement("span");
  dot.className = "unsaved-bar__dot";
  dot.setAttribute("aria-hidden", "true");
  var text = document.createElement("span");
  text.className = "unsaved-bar__text";
  var b = document.createElement("strong");
  b.textContent = state.kind === "left" ? "The owner has left. " : "Nobody is hosting this session. ";
  text.appendChild(b);
  text.appendChild(document.createTextNode("You see the code as they left it; your changes are not shared."));
  row.appendChild(dot);
  row.appendChild(text);
  row.appendChild(dlgButtonBar("Copy it to my own editor", copyToMyEditor));
  bar.appendChild(row);
  bar.hidden = false;
}

function dlgButtonBar(label, onClick) {
  var b = document.createElement("button");
  b.type = "button";
  b.className = "unsaved-bar__btn is-primary";
  b.textContent = label;
  b.addEventListener("click", onClick);
  return b;
}

// When the session is over the collaborator's page can only read. The editor is read-only; the terminal and the peer chat send
// nothing (firetty.ts and the chat widget ask over()); the owner's files cannot be reached (below); the page says so to the eye.
function lockPage() {
  document.body.classList.add("session-over");
  var ed = document.getElementById("editor");
  if (ed && ed.env && ed.env.editor) ed.env.editor.setReadOnly(true);
  installWriteGuard();
}

var guarded = false;
function blockedUrl(u) {
  var s = typeof u === "string" ? u : (u && u.url) || "";
  return /\/(ws_filebrowser|upload_file)(\?|$)/.test(s);
}
// A collaborator's page reaches the owner's files through the file browser (and uploads): not any more once the session is over.
function installWriteGuard() {
  if (guarded) return;
  guarded = true;
  var f = window.fetch;
  if (f) {
    window.fetch = function (input) {
      if (over() && blockedUrl(input)) return Promise.reject(new TypeError("This session has ended."));
      return f.apply(this, arguments);
    };
  }
  // and every write to Firebase from this page (the editor, the terminal, the files and tabs, the chat): the wipe of the session
  // alone is let through
  try {
    var R = firebase.database.Reference.prototype;
    ["set", "update", "push", "remove", "transaction", "setWithPriority"].forEach(function (name) {
      var orig = R[name];
      if (typeof orig !== "function") return;
      R[name] = function () {
        if (over() && !window.__sessionWipe) return name === "push" ? this : Promise.resolve();
        return orig.apply(this, arguments);
      };
    });
  } catch (e) { /* the page's own checks above still hold */ }
  var open = XMLHttpRequest.prototype.open;
  var send = XMLHttpRequest.prototype.send;
  XMLHttpRequest.prototype.open = function (method, url) {
    this.__sessionOverBlocked = blockedUrl(url);
    return open.apply(this, arguments);
  };
  XMLHttpRequest.prototype.send = function () {
    if (over() && this.__sessionOverBlocked) {
      var x = this;
      setTimeout(function () { x.dispatchEvent(new Event("error")); }, 0);
      return undefined;
    }
    return send.apply(this, arguments);
  };
}

// Run is off for a collaborator while the owner is away: the terminal it would run in is the owner's.
var runGuarded = false;
function installRunGuard() {
  if (runGuarded) return;
  runGuarded = true;
  ["CompileandRun", "RunandDebug"].forEach(function (name) {
    var original = window[name];
    if (typeof original !== "function") return;
    window[name] = function () {
      if (active()) {
        say("The owner is not here to run it. Copy it to your own editor to run it yourself.", "info");
        return undefined;
      }
      return original.apply(this, arguments);
    };
  });
}
