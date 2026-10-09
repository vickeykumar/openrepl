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
 * The starter questions (js/dsa.json: title, topic, level) are put in the same
 * list the first time a page loads, and any added to the file later after that
 * (seed). Each is an ordinary question with starter: true and the id s-<name>,
 * so the pages that read the list directly find it. Its description and code
 * are generated when it is first opened, as for any question. A starter is not
 * counted in the 100 a visitor keeps, and is not sent to the account until it
 * has a description or code of its own; only its done mark and a deletion are
 * (practiceState). A starter that was deleted is not put back.
 *
 * The bottom of the file handles the "New question" dialog on both pages.
 */
(function () {
  "use strict";

  var QKEY = "questions", SKEY = "practiceState", LEGACY = "bookmarkedRows";
  var MAX_QUESTIONS = 100;   // generated questions kept in the browser, newest first (starters are not counted)
  var CATALOG_URL = "/js/dsa.json";
  var STARTER_FIRST = 1735689600000; // 1 Jan 2025: starters sort after anything generated, in the order of the file
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

  // ---- starter questions --------------------------------------------------------

  function hasCode(q) {
    var t = q.code_templates;
    return !!t && typeof t === "object" && Object.keys(t).length > 0;
  }
  // a starter nobody has opened: it is the same on every device, so it is not sent
  function untouched(q) { return !!q.starter && !q.description && !hasCode(q); }

  // The oldest generated questions beyond MAX_QUESTIONS go, marked deleted so a
  // sync does not bring them back. Starters stay; qs is oldest first.
  function trimOwn(qs, st) {
    var own = qs.filter(function (q) { return !q.starter; });
    if (own.length <= MAX_QUESTIONS) return qs;
    var gone = {};
    own.slice(0, own.length - MAX_QUESTIONS).forEach(function (old) {
      st[old.id] = { del: true, t: now() };
      gone[old.id] = true;
    });
    return qs.filter(function (q) { return !gone[q.id]; });
  }

  var SMALL_WORDS = { a: 1, an: 1, and: 1, at: 1, by: 1, for: 1, from: 1, in: 1, of: 1, on: 1, or: 1, the: 1, to: 1, with: 1 };
  var CAPS_WORDS = { ii: 1, iii: 1, iv: 1, lfu: 1, lru: 1, bst: 1, lis: 1 };

  function hyphenate(title) {
    return String(title || "").toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "");
  }

  // "best-time-to-buy-and-sell-stock-ii" -> "Best Time to Buy and Sell Stock II"
  function readable(title) {
    title = String(title || "").trim();
    if (!/^[a-z0-9]+(-[a-z0-9]+)*$/.test(title)) return title; // already written for people
    return title.split("-").map(function (w, i) {
      if (CAPS_WORDS[w] || /^[a-z]$/.test(w) || /^\d+d$/.test(w)) return w.toUpperCase();
      if (i > 0 && SMALL_WORDS[w]) return w;
      return w.charAt(0).toUpperCase() + w.slice(1);
    }).join(" ");
  }

  function starterOf(item, index, slug) {
    return {
      id: "s-" + slug,
      name: readable(item.title),
      nameHyphenated: slug,
      topic: item.topic || "",
      difficulty: item.difficulty || "",
      description: null,
      code_templates: {},
      added: STARTER_FIRST - index,
      starter: true,
      delimeter: " Welcome to OpenREPL!! you can start coding here. "
    };
  }

  var seeding = null;

  // Puts the starters that are not in the list yet into it. Resolves to true
  // when it added any. Safe to call again: it runs once a page.
  function seed() {
    if (seeding) return seeding;
    if (!window.fetch) return (seeding = Promise.resolve(false));
    seeding = fetch(CATALOG_URL, { credentials: "same-origin" })
      .then(function (res) {
        if (!res.ok) throw new Error("HTTP " + res.status);
        return res.json();
      })
      .then(function (items) {
        if (!Array.isArray(items)) return false;
        var st = rawState(), qs = rawQuestions(), have = {}, add = [];
        qs.forEach(function (q) { have[q.nameHyphenated] = true; });
        items.forEach(function (item, i) {
          var slug = item && hyphenate(item.title);
          if (!slug || have[slug]) return;            // already there, or the file lists it twice
          have[slug] = true;
          var gone = st["s-" + slug];
          if (gone && gone.del) return;               // the visitor deleted it
          add.push(starterOf(item, i, slug));
        });
        if (!add.length) return false;
        save(qs.concat(add), st);
        emit("seed");
        return true;
      })
      .catch(function (e) {
        console.warn("practice: couldn't load the starter questions", e);
        return false;
      });
    return seeding;
  }

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
    rawQuestions().forEach(function (q) { if (!untouched(q)) questions[q.id] = q; });
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
    qs = trimOwn(qs, st);
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
    seed: seed,
    // resolves once the starter questions are in the list (or could not be loaded)
    ready: function () { return seed(); },
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
      qs = trimOwn(qs, st); // the oldest generated ones go, marked deleted so a sync doesn't bring them back
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

  seed();

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
