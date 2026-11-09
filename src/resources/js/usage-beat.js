/*
 * Time on the site. While the page is in front and the visitor is really using it
 * (typing, clicking, scrolling or moving a pointer in the last minute), it adds up
 * the seconds and sends them to /usage/beat every 30 seconds, and once more when
 * the page is hidden or left. The server keeps seconds and labels only: the
 * language picked, the kind of device, the country Cloudflare saw (not the
 * address) and the site the visitor came from. Never the code or the terminal.
 *
 * A browser that sends "Do Not Track" sends nothing. A tab that is in the
 * background, or left alone for a minute, adds nothing.
 */
(function () {
  "use strict";
  if (navigator.doNotTrack === "1" || window.doNotTrack === "1" || navigator.msDoNotTrack === "1") return;

  var URL_BEAT = "/usage/beat";
  var TICK_MS = 5000;        // how often the page looks whether the visitor is here
  var SEND_EVERY_MS = 30000; // how often a beat is sent
  var IDLE_MS = 60000;       // no input for this long: not active
  var FIRST_BEAT_MS = 5000;  // the visit is counted after this long in front

  function visitId() {
    var id = "";
    try { id = sessionStorage.getItem("or-visit") || ""; } catch (e) { /* storage blocked: the id lives in this page only */ }
    if (/^[a-zA-Z0-9_-]{8,40}$/.test(id)) return id;
    var bytes = [];
    try {
      var a = new Uint8Array(12);
      (window.crypto || window.msCrypto).getRandomValues(a);
      for (var i = 0; i < a.length; i++) bytes.push(a[i]);
    } catch (e) {
      for (var j = 0; j < 12; j++) bytes.push(Math.floor(Math.random() * 256));
    }
    id = bytes.map(function (b) { return ("0" + b.toString(16)).slice(-2); }).join("");
    try { sessionStorage.setItem("or-visit", id); } catch (e) { /* see above */ }
    return id;
  }

  // phone, tablet or desktop, by the short side of the screen and whether it is touched
  function deviceClass() {
    var side = Math.min(screen.width || 0, screen.height || 0);
    var touch = (window.matchMedia && window.matchMedia("(pointer: coarse)").matches) || navigator.maxTouchPoints > 1;
    if (side && side < 600) return "phone";
    if (touch && side && side < 1100) return "tablet";
    return "desktop";
  }

  function language() {
    var el = document.getElementById("optionlist");
    var v = el && el.value ? String(el.value).toLowerCase() : "";
    return /^[a-z0-9][a-z0-9_+.-]{0,19}$/.test(v) ? v : "";
  }

  // the host the visitor came from; the server decides whether it is the site itself
  function referrerHost() {
    try { return document.referrer ? new URL(document.referrer).hostname : ""; } catch (e) { return ""; }
  }

  var vid = visitId();
  var pending = 0;       // active seconds not yet sent
  var lastInput = Date.now();
  var lastTick = Date.now();
  var lastSent = 0;
  var opened = false;    // the first beat (which names where the visit came from) has gone
  var shownSince = 0;    // when the page came to the front

  function bump() { lastInput = Date.now(); }
  ["keydown", "pointerdown", "wheel", "touchstart", "scroll", "mousemove"].forEach(function (name) {
    window.addEventListener(name, bump, { passive: true, capture: true });
  });

  function visible() { return document.visibilityState !== "hidden"; }

  function tick() {
    var now = Date.now();
    var dt = Math.min(now - lastTick, TICK_MS * 2); // a sleeping laptop adds nothing
    lastTick = now;
    if (visible() && now - lastInput < IDLE_MS) pending += dt / 1000;
    if (visible() && !shownSince) shownSince = now;
    if (!visible()) shownSince = 0;

    if (!opened) {
      if (shownSince && now - shownSince >= FIRST_BEAT_MS) send(false);
    } else if (now - lastSent >= SEND_EVERY_MS && pending >= 1) {
      send(false);
    }
  }

  function body() {
    var secs = Math.min(65, Math.round(pending));
    var b = { vid: vid, secs: secs, lang: language(), device: deviceClass() };
    if (!opened) b.ref = referrerHost();
    return { text: JSON.stringify(b), secs: secs };
  }

  function send(leaving) {
    var b = body();
    if (leaving && b.secs < 3 && opened) return; // nothing worth a request
    pending -= b.secs;
    if (pending < 0) pending = 0;
    opened = true;
    lastSent = Date.now();
    try {
      if (leaving && navigator.sendBeacon) {
        navigator.sendBeacon(URL_BEAT, new Blob([b.text], { type: "application/json" }));
      } else {
        fetch(URL_BEAT, { method: "POST", headers: { "Content-Type": "application/json" }, body: b.text, keepalive: true, credentials: "same-origin" })
          .catch(function () { /* a lost beat is lost */ });
      }
    } catch (e) { /* not worth reporting */ }
  }

  setInterval(tick, TICK_MS);
  document.addEventListener("visibilitychange", function () {
    lastTick = Date.now();
    if (!visible()) { tick(); if (opened) send(true); }
  });
  window.addEventListener("pagehide", function () { if (opened) send(true); });
  // for the tests and for anyone who wants to look: nothing here is secret
  window.__openreplBeat = { state: function () { return { vid: vid, pending: pending, opened: opened }; }, tick: tick };
})();
