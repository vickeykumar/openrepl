// The "Workspace snapshots" card of the profile page (designed in the "Workspace snapshots" canvas): the snapshots of the signed-in
// user's own workspace, read from the server (which reads the object store) when the page opens and on Refresh, and a way to get
// one back: into a new folder, as a zip, or by replacing the whole workspace (after the word REPLACE has been typed; nothing is
// saved of the files that are there, so the dialog tells the user to copy or download them first). There is one file for each
// UTC day, overwritten by every snapshot of that day; the store's earlier versions of it are listed under the day. The server
// decides which home it is: the page only names a snapshot id.
(function () {
  "use strict";
  var card = document.getElementById("profile-snapshots");
  if (!card) return;
  var body = card.querySelector("[data-snap-body]");
  var status = card.querySelector("[data-snap-status]");
  var meta = card.querySelector("[data-snap-meta]");
  var refreshBtn = card.querySelector("[data-snap-refresh]");
  var dialog = null;
  var last = null; // the last answer of the list

  function el(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text !== undefined) e.textContent = text;
    return e;
  }
  function say(text, kind) {
    status.textContent = text || "";
    status.className = "profile-snap__status" + (kind ? " is-" + kind : "");
    status.hidden = !text;
  }
  function pad(n) { return (n < 10 ? "0" : "") + n; }
  function clock(d) { return pad(d.getHours()) + ":" + pad(d.getMinutes()); }
  function ymd(d) { return d.getFullYear() + "-" + pad(d.getMonth() + 1) + "-" + pad(d.getDate()); }
  function when(iso) {
    var d = new Date(iso), now = new Date();
    var yest = new Date(now.getFullYear(), now.getMonth(), now.getDate() - 1);
    var day = ymd(d) === ymd(now) ? "Today" : ymd(d) === ymd(yest) ? "Yesterday" : ymd(d);
    return { main: day + ", " + clock(d), full: ymd(d) + " " + clock(d) };
  }
  function size(n) {
    if (n < 1024) return n + " B";
    if (n < 1048576) return (n / 1024).toFixed(1) + " KB";
    return (n / 1048576).toFixed(1) + " MB";
  }
  function request(url, opts) {
    opts = opts || {};
    opts.credentials = "same-origin";
    return fetch(url, opts).then(function (res) {
      return res.text().then(function (text) {
        var data = null;
        try { data = JSON.parse(text); } catch (e) { /* a plain message */ }
        if (!res.ok) {
          var plain = text.trim() === "null" ? "" : text.trim();
          var err = new Error((data && data.error) || plain || ("The server answered " + res.status + ". Nothing was changed."));
          err.status = res.status;
          throw err;
        }
        return data;
      });
    });
  }

  // ---- the list ---------------------------------------------------------------------------------------------------------

  function label(s) {
    var w = when(s.at);
    if (s.kind === "day" || s.kind === "hidden") {
      var d = new Date(s.at), now = new Date();
      var yest = new Date(now.getFullYear(), now.getMonth(), now.getDate() - 1);
      var u = new Date(s.day + "T12:00:00");
      var name = ymd(u) === ymd(now) ? "Today" : ymd(u) === ymd(yest) ? "Yesterday" : s.day;
      return { main: name + " (" + s.day + ")", sub: "last saved " + clock(d), short: "the files of " + (name === s.day ? name : name.toLowerCase()) };
    }
    return { main: w.main, sub: w.full, short: "the snapshot of " + w.main };
  }

  function actions(s, opts) {
    var c = el("td", "profile-snap__actions");
    if (s.newest) c.appendChild(el("span", "profile-snap__badge is-newest", "Newest"));
    if (s.few) { var few = el("span", "profile-snap__badge is-few", "Few files"); few.title = "Much smaller than the snapshot before it"; c.appendChild(few); }
    var btn = el("button", "profile-btn", "Get back…");
    btn.type = "button";
    btn.addEventListener("click", function () { openDialog(s, btn); });
    c.appendChild(btn);
    return c;
  }

  function row(s, sub) {
    var tr = el("tr", sub ? "profile-snap__earlier" : "");
    tr.setAttribute("data-snap-row", s.id);
    var l = label(s);
    var c1 = el("td");
    c1.appendChild(el("div", "profile-snap__when", sub ? when(s.at).main : l.main));
    c1.appendChild(el("div", "profile-snap__sub", sub ? when(s.at).full : l.sub));
    tr.appendChild(c1);
    tr.appendChild(el("td", "profile-snap__size", size(s.size)));
    tr.appendChild(actions(s));
    return tr;
  }

  function table(rows) {
    var t = el("table", "profile-snap__table");
    var head = el("thead");
    var hr = el("tr");
    ["Taken", "Size", ""].forEach(function (x) { hr.appendChild(el("th", "", x)); });
    head.appendChild(hr);
    t.appendChild(head);
    var tb = el("tbody");
    rows.forEach(function (r) { tb.appendChild(r); });
    t.appendChild(tb);
    return t;
  }

  function render(list) {
    last = list;
    body.textContent = "";
    meta.textContent = "";
    if (!list.enabled) {
      body.appendChild(el("p", "profile-snap__note", "This server keeps no workspace snapshots."));
      refreshBtn.hidden = true;
      return;
    }
    refreshBtn.hidden = false;
    var kept = list.snapshots.filter(function (s) { return s.kind !== "hidden"; });
    var hidden = list.snapshots.filter(function (s) { return s.kind === "hidden"; });
    meta.textContent = kept.length + " of " + list.keep + " days kept";
    if (!kept.length && !hidden.length) {
      body.appendChild(el("p", "profile-snap__note", "No snapshots yet. The first one is taken a few minutes after you save a file."));
      return;
    }
    var rows = [];
    kept.forEach(function (s) {
      rows.push(row(s, false));
      var more = s.earlier || [];
      if (more.length) {
        var open = false, extra = [];
        var tr = el("tr", "profile-snap__toggle");
        var td = el("td"); td.colSpan = 3;
        var tg = el("button", "profile-snap__link", more.length + " earlier version" + (more.length === 1 ? "" : "s") + " of this day");
        tg.type = "button"; tg.setAttribute("aria-expanded", "false");
        tg.addEventListener("click", function () {
          open = !open;
          tg.setAttribute("aria-expanded", String(open));
          if (open) {
            var after = tr;
            more.forEach(function (m) { var r = row(m, true); after.parentNode.insertBefore(r, after.nextSibling); after = r; extra.push(r); });
          } else { extra.forEach(function (r) { r.parentNode.removeChild(r); }); extra = []; }
        });
        td.appendChild(tg); tr.appendChild(td); rows.push(tr);
      }
    });
    if (rows.length) body.appendChild(table(rows));
    if (hidden.length) {
      body.appendChild(el("h3", "profile-snap__h", "Removed days the store still has"));
      body.appendChild(el("p", "profile-snap__note", "These days are no longer kept, but the object store has not deleted them yet. How long it keeps them is set in the store, so they may disappear without notice."));
      body.appendChild(table(hidden.map(function (s) { return row(s, false); })));
    }
  }

  function load(keepMessage) {
    if (!keepMessage) say("");
    body.textContent = "";
    body.appendChild(el("p", "profile-snap__note", "Reading your snapshots…"));
    refreshBtn.disabled = true;
    return request("./profile/snapshots").then(function (list) {
      render(list);
    }, function (err) {
      body.textContent = "";
      body.appendChild(el("p", "profile-snap__note is-error", err.status === 401 ? "Sign in again to see your snapshots." : (err.message || "Could not read your snapshots. Try again in a minute.")));
    }).then(function () { refreshBtn.disabled = false; });
  }

  // ---- getting one back -------------------------------------------------------------------------------------------------

  function closeDialog(back) {
    if (dialog) { dialog.parentNode.removeChild(dialog); dialog = null; }
    document.removeEventListener("keydown", onKey, true);
    if (back && back.focus) back.focus();
  }
  function onKey(e) { if (e.key === "Escape" && dialog) { e.preventDefault(); closeDialog(dialog.__back); } }

  function openDialog(s, back) {
    closeDialog();
    var l = label(s);
    var backdrop = el("div", "profile-snap__backdrop");
    backdrop.__back = back;
    var box = el("div", "profile-snap__dialog");
    box.setAttribute("role", "dialog");
    box.setAttribute("aria-modal", "true");
    box.setAttribute("aria-labelledby", "snap-dialog-title");
    var title = el("h2", "", "Get back " + l.short + "?");
    title.id = "snap-dialog-title";
    box.appendChild(title);
    box.appendChild(el("p", "profile-snap__note", size(s.size) + ". Your files as they were then."));
    var form = el("div", "profile-snap__choices");
    var choice = "folder";
    var options = [
      { id: "folder", head: "Put them in a new folder", text: "A folder named restored-… is made in your workspace. Your current files are not touched." },
      { id: "zip", head: "Download as a zip", text: "Nothing is added to your workspace." },
      { id: "replace", head: "Replace my workspace", text: "Your files become exactly this snapshot. The files you have now are removed.", danger: true }
    ];
    options.forEach(function (o, i) {
      var label = el("label", "profile-snap__choice" + (o.danger ? " is-danger" : ""));
      var radio = document.createElement("input");
      radio.type = "radio"; radio.name = "snap-way"; radio.value = o.id; radio.checked = i === 0;
      radio.addEventListener("change", function () { choice = o.id; });
      var txt = el("span");
      txt.appendChild(el("strong", "", o.head));
      txt.appendChild(el("span", "profile-snap__sub", o.text));
      label.appendChild(radio); label.appendChild(txt);
      form.appendChild(label);
    });
    box.appendChild(form);
    var row = el("div", "profile-snap__buttons");
    var cancel = el("button", "profile-btn", "Cancel"); cancel.type = "button";
    var go = el("button", "profile-btn profile-btn--primary", "Continue"); go.type = "button";
    cancel.addEventListener("click", function () { closeDialog(back); });
    go.addEventListener("click", function () {
      if (choice === "folder") run(s, "folder", "", back);
      else if (choice === "zip") { closeDialog(back); var a = document.createElement("a"); a.href = "./profile/snapshots/zip?id=" + encodeURIComponent(s.id); a.click(); say("The zip is downloading.", "ok"); }
      else confirmReplace(s, box, back);
    });
    row.appendChild(cancel); row.appendChild(go);
    box.appendChild(row);
    backdrop.appendChild(box);
    document.body.appendChild(backdrop);
    dialog = backdrop;
    document.addEventListener("keydown", onKey, true);
    go.focus();
  }

  // the second step of Replace: what is removed, and the word to type
  function confirmReplace(s, box, back) {
    box.textContent = "";
    var l = label(s);
    var t = el("h2", "is-danger", "Replace your whole workspace?");
    t.id = "snap-dialog-title";
    box.appendChild(t);
    box.appendChild(el("p", "profile-snap__note", "Every file you have now is removed and replaced by " + l.short + "."));
    box.appendChild(el("p", "profile-snap__note is-warn", "Nothing is saved of the files you have now, and this cannot be taken back. Copy or download any file you want to keep before you continue."));
    var lab = el("label", "profile-snap__sub", "Type REPLACE to confirm");
    var input = document.createElement("input");
    input.type = "text"; input.className = "profile-snap__input"; input.autocomplete = "off"; input.spellcheck = false;
    input.setAttribute("aria-label", "Type REPLACE to confirm");
    lab.appendChild(input);
    box.appendChild(lab);
    var row = el("div", "profile-snap__buttons");
    var cancel = el("button", "profile-btn", "Cancel"); cancel.type = "button";
    var go = el("button", "profile-btn profile-btn--danger", "Replace"); go.type = "button"; go.disabled = true;
    input.addEventListener("input", function () { go.disabled = input.value !== "REPLACE"; });
    cancel.addEventListener("click", function () { closeDialog(back); });
    go.addEventListener("click", function () { if (input.value === "REPLACE") run(s, "replace", "REPLACE", back); });
    row.appendChild(cancel); row.appendChild(go);
    box.appendChild(row);
    input.focus();
  }

  function run(s, mode, confirm, back) {
    var busy = dialog && dialog.querySelectorAll("button");
    if (busy) Array.prototype.forEach.call(busy, function (b) { b.disabled = true; });
    request("./profile/snapshots/restore", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ id: s.id, mode: mode, confirm: confirm })
    }).then(function (res) {
      closeDialog(back);
      var l = label(s);
      var note = res.forced ? " This snapshot did not match its own file list, so its files were restored as they are in it: look them over." : "";
      if (mode === "folder") say("Done: " + res.files + " files are in the folder " + res.folder + " in your workspace. Open the editor to see them." + note, "ok");
      else say("Done: your workspace is " + l.short + "." + note, "ok");
      return load(true);
    }, function (err) {
      closeDialog(back);
      say(err.message || "That did not work. Nothing was changed.", "error");
    });
  }

  refreshBtn.addEventListener("click", function () { load(false); });
  card.hidden = false;
  load(false);
})();
