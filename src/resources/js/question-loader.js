/*
 * The question on the practice editor page (/practice?name=two-sum): the
 * loading state while Genie writes its description and a starter for the
 * language, and what the visitor is told when that does not work.
 *
 * A question that is not generated yet is fetched from Genie when it is first
 * opened, which can take a while (a description and a template, 5 to 20
 * seconds, more for a model that thinks). Until it comes the editor is covered
 * by a card that says so. It gives up after TIMEOUT_MS: the proxy's own limit to
 * the model is 90 seconds, and a visitor should not wait for that. Whatever goes
 * wrong, the card says what, in words, with Try again; Cancel is there all the
 * while.
 *
 *   QuestionLoader.waiting(text)   the card, before the work starts
 *   QuestionLoader.load({title, language, work(signal), onReady(value), onGiveUp(why)})
 *   QuestionLoader.hide()
 *   QuestionLoader.describeFailure(error)   what is said, for an Error with .kind
 *
 * An Error that tells why carries .kind: "timeout", "offline", "limit" (429),
 * "auth" (401, 403), "off" (Genie or the model is switched off), "format" (an
 * answer that cannot be used) or "server"; and .status and .serverMessage when
 * the server said something. A newer load takes over from an older one (a
 * language was picked meanwhile): the older one is stopped and says nothing.
 */
(function () {
  "use strict";

  var settings = { timeoutMs: 60000, slowMs: 20000 };

  // ---- reading the model's answer ---------------------------------------------------

  // The prompt asks for {"description": ..., "<Language>": {"template", "multiline_comment_start",
  // "multiline_comment_end"}}, but a model may write the key as "c" for "C", put the
  // templates one level down ("templates", "code_templates"), or leave the key out
  // for a template that is the whole answer. Any of those is read; a template with
  // no comment marks gets the language's. Returns null when there is no template at
  // all (then the Error says which keys the answer had).
  var COMMENTS = {
    python: ['"""', '"""'], python3: ['"""', '"""'], ruby: ["=begin", "=end"], perl: ["=pod", "=cut"],
    bash: [": '", "'"], tcl: ["if 0 {", "}"], sql: ["/*", "*/"], sqlite: ["/*", "*/"]
  };

  var OTHER_LANGUAGES = {};
  ["c", "c++", "cpp", "go", "golang", "java", "python", "python2", "python3", "javascript", "typescript", "ruby", "perl", "rust",
   "bash", "tcl", "sql", "sqlite", "node", "assembly"].forEach(function (n) { OTHER_LANGUAGES[n] = true; });

  function isTemplate(v) {
    return !!v && typeof v === "object" && typeof v.template === "string";
  }

  function plain(name) {
    return String(name || "").toLowerCase().replace(/[^a-z0-9+#]/g, "");
  }

  function pickTemplate(answer, language) {
    if (!answer || typeof answer !== "object") return null;
    var found = null;
    if (isTemplate(answer[language])) found = answer[language];
    var want = plain(language);
    [answer, answer.code_templates, answer.templates].forEach(function (holder) {
      if (found || !holder || typeof holder !== "object") return;
      Object.keys(holder).forEach(function (key) {
        if (!found && plain(key) === want && isTemplate(holder[key])) found = holder[key];
      });
    });
    if (!found && isTemplate(answer)) found = answer;
    if (!found) {
      // one template under some other name for the language ("C99", "c_language"), but not
      // under the name of another language: a C++ template is not the C one that was asked for
      var named = Object.keys(answer).filter(function (k) { return isTemplate(answer[k]) && (plain(k) === want || !OTHER_LANGUAGES[plain(k)]); });
      if (named.length === 1) found = answer[named[0]];
    }
    if (!found) return null;
    var marks = COMMENTS[want] || ["/*", "*/"];
    return {
      template: found.template,
      multiline_comment_start: typeof found.multiline_comment_start === "string" && found.multiline_comment_start ? found.multiline_comment_start : marks[0],
      multiline_comment_end: typeof found.multiline_comment_end === "string" && found.multiline_comment_end ? found.multiline_comment_end : marks[1]
    };
  }

  // ---- what to tell the visitor ----------------------------------------------------

  function describeFailure(err, timeoutMs) {
    var kind = (err && err.kind) || "server";
    var status = err && err.status ? "HTTP " + err.status : "";
    switch (kind) {
      case "timeout":
        return {
          title: "This is taking too long",
          text: "Genie didn't answer within " + Math.round((timeoutMs || settings.timeoutMs) / 1000) + " seconds, so your question wasn't loaded. The AI service may be busy right now.",
          detail: ""
        };
      case "offline":
        return { title: "Couldn't reach OpenREPL", text: "Check your internet connection, then try again.", detail: "" };
      case "limit":
        return {
          title: "You've reached your Genie limit for now",
          text: "Your requests refill over time (signed-in users get more). Try again in a minute.",
          detail: status
        };
      case "auth":
        return {
          title: "Genie couldn't verify this page",
          text: "Reload the page and try again. If it keeps happening, sign out and sign in again.",
          detail: status
        };
      case "off":
        return {
          title: "Genie isn't available right now",
          text: (err && (err.serverMessage || err.message)) || "It has been switched off for now. Try again later.",
          detail: status
        };
      case "format":
        return {
          title: "Genie's answer couldn't be used",
          text: "It answered, but not in a form a question could be built from. Trying again usually fixes it.",
          detail: ""
        };
      default:
        return {
          title: "The AI service had a problem",
          text: "It answered with an error" + (status ? " (" + status + ")" : "") + ". Try again in a moment.",
          detail: (err && err.serverMessage && err.serverMessage.length <= 160) ? err.serverMessage : ""
        };
    }
  }

  // ---- the card ---------------------------------------------------------------------

  function domUi() {
    var host = document.querySelector(".editor-body");
    var root = null, ring, title, text, hint, primary, secondary;

    function build() {
      if (root || !host) return;
      root = document.createElement("div");
      root.id = "question-loading";
      root.className = "q-loading";
      root.hidden = true;
      var card = document.createElement("div");
      card.className = "q-loading__card";
      ring = document.createElement("span");
      ring.className = "q-loading__ring";
      ring.setAttribute("aria-hidden", "true");
      title = document.createElement("p");
      title.className = "q-loading__title";
      text = document.createElement("p");
      text.className = "q-loading__text";
      hint = document.createElement("p");
      hint.className = "q-loading__hint";
      var actions = document.createElement("div");
      actions.className = "q-loading__actions";
      primary = document.createElement("button");
      primary.type = "button";
      primary.className = "q-loading__btn q-loading__btn--primary";
      secondary = document.createElement("button");
      secondary.type = "button";
      secondary.className = "q-loading__btn";
      actions.append(primary, secondary);
      card.append(ring, title, text, hint, actions);
      root.appendChild(card);
      host.appendChild(root);
    }

    function show(state, o) {
      build();
      if (!root) return;
      root.hidden = false;
      root.setAttribute("data-state", state);
      root.setAttribute("role", state === "failed" ? "alert" : "status");
      root.setAttribute("aria-busy", state === "failed" ? "false" : "true");
      title.textContent = o.title || "";
      text.textContent = o.text || "";
      hint.textContent = o.hint || "";
      hint.hidden = !o.hint;
      primary.hidden = !o.primary;
      secondary.hidden = !o.secondary;
      if (o.primary) {
        primary.textContent = o.primary.label;
        primary.onclick = o.primary.run;
      }
      if (o.secondary) {
        secondary.textContent = o.secondary.label;
        secondary.onclick = o.secondary.run;
      }
      root.onkeydown = function (e) {
        if (e.key === "Escape" && o.secondary) {
          e.preventDefault();
          e.stopPropagation();
          o.secondary.run();
        }
      };
      // the keyboard goes to the card: typing into the editor under it would be overwritten
      var focusTo = o.primary ? primary : o.secondary ? secondary : null;
      if (focusTo) {
        try { focusTo.focus({ preventScroll: true }); } catch (e) { /* not essential */ }
      }
    }

    return {
      waiting: function (label) { show("loading", { title: label }); },
      loading: function (o) { show("loading", { title: o.title, text: o.text, secondary: { label: "Cancel", run: o.onCancel } }); },
      slow: function (o) { show("loading", { title: o.title, text: o.text, hint: "Still working. A busy model can take up to a minute.", secondary: { label: "Cancel", run: o.onCancel } }); },
      failed: function (d, h) {
        show("failed", {
          title: d.title,
          text: d.text,
          hint: d.detail,
          primary: { label: "Try again", run: h.retry },
          secondary: { label: "Close", run: h.close }
        });
      },
      hide: function () { if (root) { root.hidden = true; root.onkeydown = null; } }
    };
  }

  // ---- loading ----------------------------------------------------------------------

  var current = null; // the load that is running
  var shared = null;  // the one card of the page
  function sharedUi() { return shared || (shared = domUi()); }

  function load(opts) {
    var ui = opts.ui || sharedUi();
    var timeoutMs = opts.timeoutMs || settings.timeoutMs;
    var slowMs = opts.slowMs || settings.slowMs;
    if (current) current.stop("superseded");

    var attempt = { dead: false, stop: function () {} };
    current = attempt;
    var what = opts.title ? "“" + opts.title + "”" : "your question";
    var loadingText = "Genie is writing " + what + (opts.language ? " and a starter for " + opts.language : "") + ". This usually takes 5 to 20 seconds.";

    function finish() {
      if (current === attempt) current = null;
    }

    function run() {
      var over = false, why = "", slowTimer = 0, limitTimer = 0;
      var ctl = typeof AbortController === "function" ? new AbortController() : { signal: undefined, abort: function () {} };

      attempt.stop = function (reason) {
        if (over) return;
        over = true;
        why = reason;
        clearTimeout(slowTimer);
        clearTimeout(limitTimer);
        try { ctl.abort(); } catch (e) { /* nothing to cancel */ }
        if (reason === "superseded") attempt.dead = true;
      };
      var cancel = function () {
        attempt.stop("cancelled");
      };

      ui.loading({ title: "Getting your question ready…", text: loadingText, onCancel: cancel });
      slowTimer = setTimeout(function () {
        if (!over) ui.slow({ title: "Getting your question ready…", text: loadingText, onCancel: cancel });
      }, slowMs);
      limitTimer = setTimeout(function () { attempt.stop("timeout"); }, timeoutMs);

      var work;
      try {
        work = Promise.resolve(opts.work(ctl.signal));
      } catch (e) {
        work = Promise.reject(e);
      }
      work.then(
        function (value) {
          if (over || attempt.dead) return;
          over = true;
          clearTimeout(slowTimer);
          clearTimeout(limitTimer);
          finish();
          ui.hide();
          if (opts.onReady) opts.onReady(value);
        },
        function (err) {
          clearTimeout(slowTimer);
          clearTimeout(limitTimer);
          if (attempt.dead) return;                  // a newer load has the card now
          if (over && why === "cancelled") {
            finish();
            ui.hide();
            if (opts.onGiveUp) opts.onGiveUp("cancelled");
            return;
          }
          over = true;
          if (why === "timeout") {
            err = new Error("timed out");
            err.kind = "timeout";
          }
          console.error("question: couldn't load", err);
          ui.failed(describeFailure(err, timeoutMs), {
            retry: function () { if (!attempt.dead) run(); },
            close: function () {
              finish();
              ui.hide();
              if (opts.onGiveUp) opts.onGiveUp("failed");
            }
          });
        }
      );
    }
    run();
    return attempt;
  }

  window.QuestionLoader = {
    settings: settings,
    describeFailure: describeFailure,
    pickTemplate: pickTemplate,
    load: load,
    waiting: function (label) { sharedUi().waiting(label || "Getting your question ready…"); },
    hide: function () { sharedUi().hide(); }
  };
})();
