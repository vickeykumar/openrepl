// The "Genie chats" card of the profile page (docs/lld/19-genie-agent.md, section 9): how many chats the signed-in user's workspace
// holds and how much room they take, a switch to stop saving, a download of all of them as Markdown, and a button that deletes
// them. The chats are files of the user's own workspace (server/genie_chats.go); the server decides whose workspace it is, so this
// page only asks. The card stays hidden where chats are not kept (the site has them off, or the person is not the owner).
(function () {
  "use strict";
  var card = document.getElementById("profile-chats");
  if (!card) return;
  var meta = card.querySelector("[data-chats-meta]");
  var status = card.querySelector("[data-chats-status]");
  var save = card.querySelector("[data-chats-save]");
  var dl = card.querySelector("[data-chats-download]");
  var del = card.querySelector("[data-chats-delete]");
  var armed = false;

  function say(text, kind) {
    status.textContent = text || "";
    status.className = "profile-snap__status" + (kind ? " is-" + kind : "");
    status.hidden = !text;
  }
  function size(n) {
    if (n < 1024) return n + " B";
    if (n < 1048576) return (n / 1024).toFixed(1) + " KB";
    return (n / 1048576).toFixed(1) + " MB";
  }
  function call(body) {
    return fetch("./genie_chats", { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) }).then(function (r) {
      return r.json().catch(function () { return null; }).then(function (j) { return { status: r.status, json: j }; });
    });
  }
  function show(st) {
    var n = st.chats.length;
    meta.textContent = n + (n === 1 ? " chat" : " chats") + " · " + size(st.bytes) + " · " + st.open.length + (st.open.length === 1 ? " open tab" : " open tabs");
    save.checked = !st.off;
    del.disabled = n === 0;
    dl.disabled = n === 0;
    card.hidden = false;
  }
  function load() {
    return call({ op: "state" }).then(function (r) {
      if (r.status !== 200 || !r.json || r.json.enabled !== true) { card.hidden = true; return; }
      show(r.json);
    }).catch(function () { card.hidden = true; });
  }

  save.addEventListener("change", function () {
    var off = !save.checked;
    call({ op: "off", off: off }).then(function (r) {
      if (r.status === 200) say(off ? "New chats are not saved any more." : "Your chats are saved again.", "ok");
      else { save.checked = !save.checked; say("This could not be changed right now.", "error"); }
    });
  });

  dl.addEventListener("click", function () {
    call({ op: "exportAll" }).then(function (r) {
      if (r.status !== 200 || !r.json) { say("The chats could not be downloaded right now.", "error"); return; }
      var a = document.createElement("a");
      a.href = URL.createObjectURL(new Blob([r.json.markdown || ""], { type: "text/markdown" }));
      a.download = "genie-chats.md";
      document.body.appendChild(a);
      a.click();
      a.remove();
      setTimeout(function () { URL.revokeObjectURL(a.href); }, 2000);
    });
  });

  // asks once: the first press arms the button, the second deletes
  del.addEventListener("click", function () {
    if (!armed) {
      armed = true;
      del.textContent = "Delete all chats for good?";
      say("Press the button again to delete every chat of this workspace. It cannot be undone, and a snapshot that holds them keeps them until it is pruned.", "warn");
      setTimeout(function () { armed = false; del.textContent = "Delete all chats"; }, 8000);
      return;
    }
    armed = false;
    del.textContent = "Delete all chats";
    call({ op: "deleteAll" }).then(function (r) {
      if (r.status === 200) { say("All chats were deleted.", "ok"); load(); }
      else say("The chats could not be deleted right now.", "error");
    });
  });

  load();
})();
