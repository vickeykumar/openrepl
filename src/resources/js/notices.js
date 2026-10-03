/*
 * On-page notices (T6). They replace window.alert() pop-ups, which block the
 * page. Usage:
 *   notify("Saved main.py", { type: "success" });
 *   notify("words.csv is over 20 MB.", { type: "error", title: "Upload failed" });
 * type: "info" (default), "success" or "error". Errors stay until dismissed
 * or for 10 seconds; the others go after 4 seconds.
 */
(function () {
  var ICONS = {
    success: '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M5 12l5 5 9-10"></path></svg>',
    info: '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" aria-hidden="true"><path d="M12 11v6 M12 7h.01"></path></svg>',
    error: '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" aria-hidden="true"><path d="M12 7v6 M12 17h.01"></path></svg>'
  };

  function region() {
    var r = document.getElementById("notices");
    if (!r) {
      r = document.createElement("div");
      r.id = "notices";
      r.className = "notices";
      r.setAttribute("aria-live", "polite");
      document.body.appendChild(r);
    }
    return r;
  }

  function text(el, value) {
    el.textContent = value == null ? "" : String(value);
    return el;
  }

  window.notify = function (message, opts) {
    opts = opts || {};
    var type = ICONS[opts.type] ? opts.type : "info";
    if (!document.body) { console.log("notice:", message); return; }
    var n = document.createElement("div");
    n.className = "notice notice--" + type;
    n.setAttribute("role", type === "error" ? "alert" : "status");

    var icon = document.createElement("span");
    icon.className = "notice__icon";
    icon.innerHTML = ICONS[type];
    n.appendChild(icon);

    var body = document.createElement("div");
    body.className = "notice__body";
    if (opts.title) {
      var t = text(document.createElement("span"), opts.title);
      t.className = "notice__title";
      body.appendChild(t);
    }
    var m = text(document.createElement("span"), message);
    m.className = "notice__text";
    body.appendChild(m);
    n.appendChild(body);

    var close = document.createElement("button");
    close.type = "button";
    close.className = "notice__close";
    close.setAttribute("aria-label", "Dismiss");
    close.innerHTML = '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" aria-hidden="true"><path d="M6 6l12 12 M18 6L6 18"></path></svg>';
    n.appendChild(close);

    var r = region();
    r.appendChild(n);
    while (r.children.length > 3) r.removeChild(r.firstChild);

    var timer = null;
    function dismiss() {
      clearTimeout(timer);
      n.classList.add("notice--hide");
      setTimeout(function () { if (n.parentNode) n.parentNode.removeChild(n); }, 250);
    }
    close.addEventListener("click", dismiss);
    var ms = opts.timeout != null ? opts.timeout : (type === "error" ? 10000 : 4000);
    if (ms > 0) timer = setTimeout(dismiss, ms);
    return dismiss;
  };

  // Anything that still calls alert() shows a notice instead of a blocking pop-up.
  window.alert = function (message) {
    window.notify(message, { type: "info", timeout: 8000 });
  };
})();
