// The "Workspace snapshots" card of the profile page (designed in the "Workspace snapshots" canvas): the snapshots of the signed-in
// user's own workspace, read from the server (which reads the object store) when the page opens and on Refresh, and a way to get
// one back: into a new folder, as a zip, or by replacing the whole workspace (after a copy of the files that are there, and
// after the word REPLACE has been typed). The server decides which home it is: the page only names a generation.
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
    meta.textContent = list.snapshots.length + " of " + list.keep + " kept";
    if (!list.snapshots.length) {
      body.appendChild(el("p", "profile-snap__note", "No snapshots yet. The first one is taken a few minutes after you save a file."));
      return;
    }
    var table = el("table", "profile-snap__table");
    var head = el("thead");
    var hr = el("tr");
    ["Taken", "Size", ""].forEach(function (t) { hr.appendChild(el("th", "", t)); });
    head.appendChild(hr);
    table.appendChild(head);
    var tb = el("tbody");
    list.snapshots.forEach(function (s) {
      var tr = el("tr");
      tr.setAttribute("data-snap-row", String(s.generation));
      var w = when(s.at);
      var c1 = el("td");
      c1.appendChild(el("div", "profile-snap__when", w.main));
      c1.appendChild(el("div", "profile-snap__sub", w.full + " · generation " + s.generation));
      var c2 = el("td", "profile-snap__size", size(s.size));
      var c3 = el("td", "profile-snap__actions");
      if (s.newest) c3.appendChild(el("span", "profile-snap__badge is-newest", "Newest"));
      if (s.before) c3.appendChild(el("span", "profile-snap__badge", "Before restore"));
      if (s.few) { var few = el("span", "profile-snap__badge is-few", "Few files"); few.title = "Much smaller than the snapshot before it"; c3.appendChild(few); }
      var btn = el("button", "profile-btn", "Get back…");
      btn.type = "button";
      btn.addEventListener("click", function () { openDialog(s, btn); });
      c3.appendChild(btn);
      tr.appendChild(c1); tr.appendChild(c2); tr.appendChild(c3);
      tb.appendChild(tr);
    });
    table.appendChild(tb);
    body.appendChild(table);
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
    var w = when(s.at);
    var backdrop = el("div", "profile-snap__backdrop");
    backdrop.__back = back;
    var box = el("div", "profile-snap__dialog");
    box.setAttribute("role", "dialog");
    box.setAttribute("aria-modal", "true");
    box.setAttribute("aria-labelledby", "snap-dialog-title");
    var title = el("h2", "", "Get back the snapshot of " + w.main + "?");
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
      else if (choice === "zip") { closeDialog(back); var a = document.createElement("a"); a.href = "./profile/snapshots/zip?generation=" + s.generation; a.click(); say("The zip is downloading.", "ok"); }
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
    var w = when(s.at);
    var t = el("h2", "is-danger", "Replace your whole workspace?");
    t.id = "snap-dialog-title";
    box.appendChild(t);
    box.appendChild(el("p", "profile-snap__note", "Every file you have now is removed and replaced by the files of the snapshot of " + w.main + "."));
    box.appendChild(el("p", "profile-snap__note", "First, a copy of what you have now is saved as a snapshot, so you can come back to it from this list. Only the newest " + (last ? last.keep : 3) + " snapshots are kept, so that way back does not stay open for ever."));
    var label = el("label", "profile-snap__sub", "Type REPLACE to confirm");
    var input = document.createElement("input");
    input.type = "text"; input.className = "profile-snap__input"; input.autocomplete = "off"; input.spellcheck = false;
    input.setAttribute("aria-label", "Type REPLACE to confirm");
    label.appendChild(input);
    box.appendChild(label);
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
      body: JSON.stringify({ generation: s.generation, mode: mode, confirm: confirm })
    }).then(function (res) {
      closeDialog(back);
      var w = when(s.at);
      if (mode === "folder") say("Done: " + res.files + " files are in the folder " + res.folder + " in your workspace. Open the editor to see them.", "ok");
      else say("Done: your workspace is the snapshot of " + w.main + ". A copy of the files you had is in the list as Before restore.", "ok");
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
