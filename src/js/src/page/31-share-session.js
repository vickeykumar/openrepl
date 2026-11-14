// A share link with a time (docs/lld/06-frontend.md, "Share with a time").
//
// Part of the page script. The owner's Share popover shows how long the link works, a countdown, "Add time" and "End session".
// The link is the id of the page's editor (src/js/src/page/01-session.js, getExampleRef): it is saved in this browser with its
// end, so a reload gives the same link until the end. The end is also written to Firebase beside the code
// (editor_values/<id>/share), where a collaborator's page reads it (30-owner-presence.js).
//
// The time is for honest use and for tidying up. Nothing checks it on a server: it is not a lock.

var KEY = "openrepl-share"; // {id, endsAt, mins}
var MINS_KEY = "openrepl-share-mins"; // the time chosen last
var TAB_KEY = "openrepl-share-tab"; // {tab, at}: the page that holds the saved link says so, so that a second tab does not take it too
var GUEST_CHOICES = [30, 60];
var USER_CHOICES = [30, 60, 240, 1440, 10080];
var HEARTBEAT_MS = 5000;
var WARN_MS = 5 * 60000;
var END_PAUSE_MS = 2500;
var tabId = Math.random().toString(36).slice(2);
var offsetMs = 0;
var current = null; // {id, endsAt, mins} of this page's session, when there is one
var warned = false;
var ui = null;

window.ShareSession = { start: start, active: function () { return !!current; }, end: end, wipe: wipe, record: function () { return current; } };

function isGuest() { return String(window.homedir || "").indexOf("guest-") >= 0; }
function settings() { return window.site_settings || {}; }
function maxMinutes() {
  var v = Number(isGuest() ? settings().shareMaxGuestMinutes : settings().shareMaxUserMinutes);
  return v > 0 ? v : (isGuest() ? 60 : 10080);
}
function choices() {
  var base = isGuest() ? GUEST_CHOICES : USER_CHOICES;
  var max = maxMinutes();
  var out = base.filter(function (m) { return m <= max; });
  return out.length ? out : [max];
}
function label(m) {
  if (m % 1440 === 0) return m === 1440 ? "24 hours" : (m / 1440) + " days";
  return m < 60 ? m + " minutes" : m === 60 ? "1 hour" : m % 60 === 0 ? (m / 60) + " hours" : m + " minutes";
}
function now() { return Date.now() + offsetMs; }
function say(text, type) { if (window.notify) window.notify(text, { type: type || "info", timeout: 5000 }); }
function db() { return firebase.database(); }
function shareRef(id) { return db().ref("editor_values/" + id + "/share"); }

// ---- what is saved in this browser ------------------------------------------------------------------------------------

function read() {
  try {
    var v = JSON.parse(localStorage.getItem(KEY));
    return v && v.id && Number(v.endsAt) ? v : null;
  } catch (e) { return null; }
}
function write(v) {
  try { if (v) localStorage.setItem(KEY, JSON.stringify(v)); else localStorage.removeItem(KEY); } catch (e) { /* kept in the page only */ }
}
function lastMins() {
  try { var m = Number(localStorage.getItem(MINS_KEY)); if (m > 0) return m; } catch (e) { /* the default */ }
  return 60;
}
function startMins() {
  var list = choices();
  var m = lastMins();
  return list.indexOf(m) >= 0 ? m : list[Math.min(1, list.length - 1)];
}

// ---- starting, changing and ending -----------------------------------------------------------------------------------

function start(mins) {
  var id = window.dbpath;
  if (!id || current) return;
  try { localStorage.setItem(MINS_KEY, String(mins)); } catch (e) { /* not essential */ }
  current = { id: id, endsAt: now() + mins * 60000, mins: mins };
  persist();
  draw();
}

// the record, in this browser and in Firebase, for the end that is set now
function persist() {
  write(current);
  holdLink();
  shareRef(current.id).set({ endsAt: current.endsAt, mins: current.mins, state: "live" }).catch(function (e) { console.error("share:", e); });
}

function setMinutes(mins) {
  if (!current) return;
  current.mins = mins;
  current.endsAt = now() + mins * 60000;
  warned = false;
  try { localStorage.setItem(MINS_KEY, String(mins)); } catch (e) { /* not essential */ }
  persist();
  draw();
}

function addTime() {
  if (!current) return;
  var limit = now() + maxMinutes() * 60000;
  var next = current.endsAt + current.mins * 60000;
  if (next > limit) {
    next = limit;
    say("That is the longest a link can work here.", "info");
  }
  current.endsAt = next;
  warned = false;
  persist();
  draw();
}

// Everything of a session that is kept in Firebase: the code, the language, the queue of changes, the tabs and the terminal
// records, and the end record itself. Whoever sees that a session is over may do this (the owner's page, a collaborator's, and
// the owner's page again when it loads and finds its old link ran out): it does nothing a second time.
function wipe(id) {
  if (!id) return Promise.resolve();
  var d = db();
  var jobs;
  window.__sessionWipe = true; // a collaborator's page writes nothing once the session is over, except this (30-owner-presence.js)
  try {
    jobs = [d.ref("editor_values/" + id).remove(), d.ref("file-browser/" + id).remove(), d.ref("chat-list/" + id).remove()];
    if (window.getrepl_firebasedbref) jobs.push(window.getrepl_firebasedbref().child(id).remove());
  } catch (e) {
    jobs = jobs || [];
  } finally {
    window.__sessionWipe = false;
  }
  return Promise.all(jobs.map(function (p) { return p.catch(function () {}); }));
}

// state: "ended" (the owner pressed End session) or "expired" (the time ran out). The collaborators are told through the record;
// after a short wait everything of the session is taken out of Firebase, and the page loads again, so that the next Share
// makes a new link. The owner's code is in the page's own drafts.
function end(state) {
  if (!current) return;
  var id = current.id;
  var info = current;
  current = null;
  write(null);
  releaseLink();
  if (window.OwnerAway && window.OwnerAway.stopHosting) window.OwnerAway.stopHosting();
  shareRef(id).update({ state: state, endsAt: Math.min(info.endsAt, now()) }).catch(function () {});
  say(state === "expired" ? "The link ran out of time. Press Share to start a new session." : "Session ended. Press Share to start a new one.", "success");
  draw();
  setTimeout(function () { wipe(id).then(function () { location.reload(); }); }, END_PAUSE_MS);
}

// ---- a second tab of this browser must not take the same link ------------------------------------------------------

var beat = 0;
function holdLink() {
  if (beat) return;
  var tick = function () { try { localStorage.setItem(TAB_KEY, JSON.stringify({ tab: tabId, at: Date.now() })); } catch (e) { /* not essential */ } };
  tick();
  beat = setInterval(tick, HEARTBEAT_MS);
  window.addEventListener("pagehide", releaseLink);
}
function releaseLink() {
  if (beat) { clearInterval(beat); beat = 0; }
  try {
    var v = JSON.parse(localStorage.getItem(TAB_KEY));
    if (v && v.tab === tabId) localStorage.removeItem(TAB_KEY);
  } catch (e) { /* not essential */ }
}

// ---- the popover ---------------------------------------------------------------------------------------------------------

function buildUI() {
  if (ui) return ui;
  var host = document.getElementById("share-session");
  if (!host) return null;
  host.innerHTML =
    '<div class="share-session__row"><label for="share-mins">Link works for</label><select id="share-mins" class="share-session__select"></select></div>' +
    '<p class="share-session__left" id="share-left" role="status"></p>' +
    '<div class="share-session__buttons"><button type="button" class="popover__btn" id="share-add">Add time</button>' +
    '<button type="button" class="popover__btn popover__btn--danger" id="share-end">End session</button></div>';
  ui = { host: host, select: host.querySelector("#share-mins"), left: host.querySelector("#share-left") };
  host.querySelector("#share-add").addEventListener("click", addTime);
  host.querySelector("#share-end").addEventListener("click", askEnd);
  ui.select.addEventListener("change", function () { setMinutes(Number(ui.select.value)); });
  return ui;
}

function pad(n) { return n < 10 ? "0" + n : String(n); }
function leftText() {
  var ms = current.endsAt - now();
  if (ms <= 0) return "The time is up.";
  var mins = Math.ceil(ms / 60000);
  var at = new Date(Date.now() + ms);
  var text = mins >= 120 ? "Ends in " + Math.round(mins / 60) + " hours" : mins >= 60 ? "Ends in 1 hour " + (mins - 60) + " min" : "Ends in " + mins + (mins === 1 ? " minute" : " minutes");
  return text + " (at " + pad(at.getHours()) + ":" + pad(at.getMinutes()) + ")";
}

function draw() {
  var u = buildUI();
  if (!u) return;
  u.host.hidden = !current;
  if (!current) return;
  var list = choices();
  if (u.select.options.length !== list.length) {
    u.select.textContent = "";
    list.forEach(function (m) {
      var o = document.createElement("option");
      o.value = String(m);
      o.textContent = label(m);
      u.select.appendChild(o);
    });
  }
  u.select.value = String(current.mins);
  if (u.select.value !== String(current.mins)) {
    var extra = document.createElement("option");
    extra.value = String(current.mins);
    extra.textContent = label(current.mins);
    u.select.appendChild(extra);
    u.select.value = String(current.mins);
  }
  u.left.textContent = leftText();
  u.left.classList.toggle("is-soon", current.endsAt - now() <= WARN_MS);
}

function askEnd() {
  var d = document.createElement("div");
  d.className = "unsaved-backdrop";
  d.innerHTML =
    '<div class="unsaved-dialog" role="alertdialog" aria-modal="true" aria-labelledby="end-title" aria-describedby="end-body">' +
    '<h2 id="end-title">End this session?</h2><p id="end-body"></p><div class="unsaved-dialog__buttons"></div></div>';
  d.querySelector("#end-body").textContent =
    "The link stops working now. The people on it are told it ended and can copy the code to their own editor. Your editor and your files stay as they are.";
  var buttons = d.querySelector(".unsaved-dialog__buttons");
  var close = function () { d.remove(); };
  var keep = document.createElement("button");
  keep.type = "button";
  keep.className = "unsaved-dialog__btn";
  keep.textContent = "Keep it going";
  keep.addEventListener("click", close);
  var stop = document.createElement("button");
  stop.type = "button";
  stop.className = "unsaved-dialog__btn is-primary";
  stop.textContent = "End session";
  stop.addEventListener("click", function () { close(); end("ended"); });
  buttons.appendChild(stop);
  buttons.appendChild(keep);
  d.addEventListener("keydown", function (e) { if (e.key === "Escape") { e.preventDefault(); close(); } });
  document.body.appendChild(d);
  keep.focus();
}

// ---- the clock -----------------------------------------------------------------------------------------------------------

function tick() {
  if (!current) return;
  var ms = current.endsAt - now();
  if (ms <= 0) { end("expired"); return; }
  if (ms <= WARN_MS && !warned) {
    warned = true;
    say("This session ends in " + Math.ceil(ms / 60000) + " minutes. Open Share and press Add time to keep it going.", "info");
  }
  draw();
}

$(function () {
  if (typeof isMaster === "function" && !isMaster()) return;
  firebase.database().ref(".info/serverTimeOffset").on("value", function (s) { offsetMs = Number(s.val()) || 0; });
  // a session that was started before this reload goes on with the same link
  var saved = read();
  if (saved && saved.id === window.dbpath && saved.endsAt > Date.now()) {
    current = saved;
    holdLink();
  } else if (saved && saved.endsAt <= Date.now()) {
    // its time ran out while this page was closed: nothing of it stays in Firebase
    write(null);
    wipe(saved.id);
  }
  buildUI();
  draw();
  setInterval(tick, 15000);
  // opening Share starts the session, with the time chosen last
  var dd = document.getElementById("myDropdown");
  if (dd && window.MutationObserver) {
    new MutationObserver(function () {
      if (dd.classList.contains("show")) {
        if (!current) start(startMins());
        else draw();
      }
    }).observe(dd, { attributes: true, attributeFilter: ["class"] });
  }
});
