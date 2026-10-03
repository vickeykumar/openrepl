/*
 * Practice questions and progress (T17), shared by the practice list
 * (/practice/dsa-questions) and the editor (/practice?name=...).
 *
 * localStorage keeps, as before:
 *   questions       the generated questions (array)
 *   bookmarkedRows  ids of the questions marked done (kept for older code)
 * and now also:
 *   practiceState   {id: {done, t}} or {id: {del: true, t}} for a deleted
 *                   question; t is the time of the change, the newer one wins
 *
 * When the visitor is signed in, PracticeStore.init() merges this with their
 * account through /practice/progress, and later changes are sent a moment
 * after they happen, so the list follows them to other browsers. Signed out,
 * everything stays in this browser as it always has.
 *
 * The bottom of the file handles the "New question" dialog on both pages.
 */
(function () {
  "use strict";

  var QKEY = "questions", SKEY = "practiceState", LEGACY = "bookmarkedRows";
  var MAX_QUESTIONS = 100;   // kept in the browser, newest first
  var MAX_STATES = 1000;     // done and deleted marks
  var URL = "/practice/progress";

  var listeners = [];
  var status = "local";      // local | syncing | synced | signed-out | offline
  var timer = null, inflight = null, again = false, started = false;

  function now() { return Date.now(); }
  function read(key, fallback) {
    try {
      var v = JSON.parse(localStorage.getItem(key));
      return v == null ? fallback : v;
    } catch (e) { return fallback; }
  }
  function write(key, value) {
    try { localStorage.setItem(key, JSON.stringify(value)); } catch (e) { console.warn("practice: couldn't save", key, e); }
  }
  function version(q) { return Math.max(+q.updated || 0, +q.added || 0); }

  function rawQuestions() {
    var q = read(QKEY, []);
    return Array.isArray(q) ? q.filter(function (x) { return x && x.id; }) : [];
  }
  function rawState() {
    var s = read(SKEY, null);
    if (!s || typeof s !== "object" || Array.isArray(s)) {
      // first run: take the done marks from the older list
      s = {};
      var legacy = read(LEGACY, []);
      if (Array.isArray(legacy)) legacy.forEach(function (id) { s[id] = { done: true, t: 1 }; });
      write(SKEY, s);
    }
    return s;
  }

  function save(qs, st) {
    var live = {};
    qs.forEach(function (q) { live[q.id] = true; });
    var ids = Object.keys(st);
    if (ids.length > MAX_STATES) {
      // forget the oldest tombstones and marks of questions that are gone
      ids.filter(function (id) { return !live[id]; })
        .sort(function (a, b) { return (st[b].del ? 1 : 0) - (st[a].del ? 1 : 0) || st[a].t - st[b].t; })
        .slice(0, ids.length - MAX_STATES)
        .forEach(function (id) { delete st[id]; });
    }
    write(QKEY, qs);
    write(SKEY, st);
    write(LEGACY, Object.keys(st).filter(function (id) { return st[id].done && !st[id].del && live[id]; }));
  }

  function emit(reason) {
    listeners.forEach(function (fn) {
      try { fn(reason); } catch (e) { console.error(e); }
    });
  }

  function changed(reason) {
    emit(reason);
    if (started && status !== "signed-out") schedule();
  }

  // ---- sync ---------------------------------------------------------------

  function localDoc() {
    var questions = {};
    rawQuestions().forEach(function (q) { questions[q.id] = q; });
    return { questions: questions, state: rawState() };
  }

  // Merge a document from the server into the browser's copy.
  function applyRemote(doc) {
    if (!doc || typeof doc !== "object") return false;
    var st = rawState(), byId = {}, before = JSON.stringify([rawQuestions(), st]);
    rawQuestions().forEach(function (q) { byId[q.id] = q; });
    var rs = doc.state || {}, rq = doc.questions || {};
    Object.keys(rs).forEach(function (id) {
      var r = rs[id], l = st[id];
      if (r && typeof r.t === "number" && (!l || r.t > l.t)) st[id] = r;
    });
    Object.keys(rq).forEach(function (id) {
      var r = rq[id], l = byId[id];
      if (r && r.id === id && (!l || version(r) > version(l))) byId[id] = r;
    });
    var qs = Object.keys(byId).map(function (id) { return byId[id]; })
      .filter(function (q) { return !(st[q.id] && st[q.id].del); })
      .sort(function (a, b) { return (+a.added || 0) - (+b.added || 0); });
    if (qs.length > MAX_QUESTIONS) {
      qs.slice(0, qs.length - MAX_QUESTIONS).forEach(function (q) { st[q.id] = { del: true, t: now() }; });
      qs = qs.slice(-MAX_QUESTIONS);
    }
    save(qs, st);
    return JSON.stringify([qs, st]) !== before;
  }

  function setStatus(s) {
    if (status === s) return;
    status = s;
    emit("status");
  }

  function request(method, body) {
    var opts = { method: method, credentials: "same-origin", headers: { "Accept": "application/json" } };
    if (body) {
      opts.headers["Content-Type"] = "application/json";
      opts.body = body;
    }
    return fetch(URL, opts).then(function (res) {
      if (res.status === 401) { setStatus("signed-out"); return null; }
      if (!res.ok) throw new Error("HTTP " + res.status);
      return res.json();
    }).then(function (doc) {
      if (doc && doc.signedIn === false) { setStatus("signed-out"); return null; }
      return doc;
    });
  }

  function push() {
    if (inflight) { again = true; return inflight; }
    setStatus("syncing");
    inflight = request("PUT", JSON.stringify(localDoc()))
      .then(function (doc) {
        if (!doc) return;
        if (applyRemote(doc)) emit("remote");
        setStatus("synced");
      })
      .catch(function () { setStatus("offline"); })
      .then(function () {
        inflight = null;
        if (again) { again = false; schedule(); }
      });
    return inflight;
  }

  function schedule() {
    clearTimeout(timer);
    timer = setTimeout(push, 1500);
  }

  // First sync of the page: read the account's copy (signed out, nothing
  // more is sent), then send ours to be merged into it.
  function init() {
    if (started) return inflight || Promise.resolve();
    started = true;
    if (!window.fetch) return Promise.resolve();
    setStatus("syncing");
    inflight = request("GET")
      .then(function (doc) {
        if (!doc) return null;
        if (applyRemote(doc)) emit("remote");
        inflight = null;
        return push();
      })
      .catch(function () { setStatus("offline"); })
      .then(function () { inflight = null; });
    return inflight;
  }

  // A change left waiting when the page closes is sent with keepalive when it
  // is small enough; otherwise the next visit sends it.
  window.addEventListener("pagehide", function () {
    if (!timer || status === "signed-out" || !started) return;
    clearTimeout(timer);
    timer = null;
    var body = JSON.stringify(localDoc());
    if (body.length > 60000 || !window.fetch) return;
    try {
      fetch(URL, { method: "PUT", credentials: "same-origin", keepalive: true, headers: { "Content-Type": "application/json" }, body: body });
    } catch (e) {}
  });

  // another tab changed the list
  window.addEventListener("storage", function (e) {
    if (e.key === QKEY || e.key === SKEY) emit("external");
  });

  // ---- public API -------------------------------------------------------------

  window.PracticeStore = {
    init: init,
    sync: push,
    status: function () { return status; },
    onChange: function (fn) { listeners.push(fn); },

    // questions not deleted, oldest first
    all: function () {
      var st = rawState();
      return rawQuestions().filter(function (q) { return !(st[q.id] && st[q.id].del); });
    },
    find: function (key) {
      var list = this.all();
      for (var i = 0; i < list.length; i++) {
        if (list[i].id === key || list[i].nameHyphenated === key) return list[i];
      }
      return null;
    },
    isDone: function (id) {
      var s = rawState()[id];
      return !!(s && s.done && !s.del);
    },
    setDone: function (id, done) {
      var st = rawState();
      st[id] = { done: !!done, t: now() };
      save(rawQuestions(), st);
      changed("done");
    },
    // adds a new question; returns {error, storedQuestions} like saveNewQuestions
    add: function (q) {
      var st = rawState();
      var qs = this.all();
      for (var i = 0; i < qs.length; i++) {
        if (qs[i].nameHyphenated === q.nameHyphenated) return { error: "This question already exists.", storedQuestions: qs };
      }
      q.updated = q.updated || q.added || now();
      qs.push(q);
      if (qs.length > MAX_QUESTIONS) {
        // the oldest ones go, marked deleted so a sync doesn't bring them back
        qs.slice(0, qs.length - MAX_QUESTIONS).forEach(function (old) { st[old.id] = { del: true, t: now() }; });
        qs = qs.slice(-MAX_QUESTIONS);
      }
      save(qs, st);
      changed("add");
      return { error: null, storedQuestions: qs };
    },
    // saves an edited question (a new code template, code progress)
    update: function (q) {
      var qs = rawQuestions();
      for (var i = 0; i < qs.length; i++) {
        if (qs[i].id === q.id) {
          q.updated = now();
          qs[i] = q;
          save(qs, rawState());
          changed("update");
          return;
        }
      }
    },
    remove: function (id) {
      var st = rawState();
      st[id] = { del: true, t: now() };
      save(rawQuestions().filter(function (q) { return q.id !== id; }), st);
      changed("remove");
    }
  };

  // ---- "New question" dialog ----------------------------------------------------
  // Both pages share #modal. Whoever adds or removes .show-modal, this moves
  // focus into the dialog, keeps Tab inside it, closes it on Esc and returns
  // focus to where it was.
  function initDialog() {
    var modal = document.getElementById("modal");
    if (!modal || modal.getAttribute("data-dialog-ready")) return;
    modal.setAttribute("data-dialog-ready", "true");
    var back = null;
    var open = false;

    function focusables() {
      return Array.prototype.filter.call(
        modal.querySelectorAll("button, input, select, textarea, a[href]"),
        function (el) { return !el.disabled && el.offsetParent !== null; });
    }
    function sync() {
      var isOpen = modal.classList.contains("show-modal");
      if (isOpen === open) return;
      open = isOpen;
      document.body.classList.toggle("modal-open", isOpen);
      if (isOpen) {
        back = document.activeElement;
        var first = document.getElementById("topic") || focusables()[0];
        if (first) setTimeout(function () { first.focus(); }, 30);
      } else if (back && document.contains(back)) {
        try { back.focus({ preventScroll: true }); } catch (e) { back.focus(); }
        back = null;
      }
    }
    if (window.MutationObserver) new MutationObserver(sync).observe(modal, { attributes: true, attributeFilter: ["class"] });
    // generateNewQuestion (common.js) adds a .loader to #modal while it works
    var submit = document.getElementById("qsubmit-btn");
    function busy() {
      var on = !!modal.querySelector(":scope > .loader");
      modal.classList.toggle("is-busy", on);
      if (!submit) return;
      if (on && !submit.getAttribute("data-idle")) {
        submit.setAttribute("data-idle", submit.textContent);
        submit.textContent = "Generating…";
        submit.disabled = true;
      } else if (!on && submit.getAttribute("data-idle")) {
        submit.textContent = submit.getAttribute("data-idle");
        submit.removeAttribute("data-idle");
        submit.disabled = false;
      }
    }
    if (window.MutationObserver) new MutationObserver(busy).observe(modal, { childList: true });
    modal.addEventListener("keydown", function (e) {
      if (e.key === "Escape") {
        e.preventDefault();
        e.stopPropagation();
        modal.classList.remove("show-modal");
        return;
      }
      if (e.key !== "Tab") return;
      var f = focusables();
      if (!f.length) return;
      var first = f[0], last = f[f.length - 1];
      if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
      else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
    });
    var cancel = document.getElementById("modal-cancel");
    if (cancel) cancel.addEventListener("click", function () { modal.classList.remove("show-modal"); });
    // suggestions for the topic field; `topics` comes from common.js
    var list = document.getElementById("topic-list");
    if (list && !list.options.length && typeof topics !== "undefined") {
      list.innerHTML = topics.map(function (t) {
        return '<option value="' + String(t).replace(/&/g, "&amp;").replace(/"/g, "&quot;") + '"></option>';
      }).join("");
    }
  }
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", initDialog);
  else initDialog();
})();
