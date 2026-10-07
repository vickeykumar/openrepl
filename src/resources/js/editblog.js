/*
 * The blog editor (editblog.html). Posts are listed from /blog?q=index, opened
 * with /blog?q=json&name=..., and saved or deleted with POST /blog, which only
 * an admin may do and only with the header below (server/blog_db.go).
 *
 * What it adds to a plain form: a list of the posts with search, an address made
 * from the title, a draft kept on this device (so a closed tab or a crash loses
 * nothing), a warning before unsaved changes are lost, a confirmation before a
 * delete, and a preview that looks like the blog.
 */
(function () {
  "use strict";

  var ADMIN_HEADER = { "X-Requested-With": "openrepl-admin" };
  var DRAFT_PREFIX = "blog-draft:";
  var MAX_POST_BYTES = 1.9 * 1024 * 1024; // the server takes 2 MB

  var $ = function (id) { return document.getElementById(id); };
  var state = { posts: [], original: null, dirty: false, nameTouched: false, editor: null, saving: false };

  // ---- small helpers ----------------------------------------------------------

  function api(method, url, form) {
    var headers = Object.assign({ Accept: "application/json" }, ADMIN_HEADER);
    var opts = { method: method, headers: headers, credentials: "same-origin" };
    if (form) {
      headers["Content-Type"] = "application/x-www-form-urlencoded";
      opts.body = new URLSearchParams(form).toString();
    }
    return fetch(url, opts).then(function (res) {
      return res.json().catch(function () { return {}; }).then(function (data) {
        return { ok: res.ok, status: res.status, data: data };
      });
    });
  }

  function say(message, type, title) {
    if (window.notify) window.notify(message, { type: type || "info", title: title });
    else console.log(message);
  }

  function slugify(text) {
    return text.toLowerCase().normalize("NFKD").replace(/[̀-ͯ]/g, "")
      .replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 80);
  }

  function shortDate(iso) {
    var d = new Date(iso);
    if (isNaN(d) || d.getFullYear() < 2000) return "";
    return d.toLocaleDateString(undefined, { month: "short", day: "numeric", year: "numeric" });
  }

  function confirmDialog(title, body, okText, danger) {
    return new Promise(function (resolve) {
      var dlg = $("eb-dialog");
      $("eb-dialog-title").textContent = title;
      $("eb-dialog-body").textContent = body;
      var ok = $("eb-dialog-ok");
      ok.textContent = okText || "OK";
      ok.className = "eb-btn " + (danger ? "eb-btn--danger" : "eb-btn--primary");
      var answer = false;
      var done = function (yes) { answer = yes; dlg.close(); };
      ok.onclick = function () { done(true); };
      $("eb-dialog-cancel").onclick = function () { done(false); };
      dlg.onclose = function () { dlg.onclose = null; resolve(answer); };
      dlg.showModal();
      (danger ? $("eb-dialog-cancel") : ok).focus();
    });
  }

  // ---- the state of the form ----------------------------------------------------

  function setDirty(on) {
    state.dirty = on;
    var pill = $("eb-state");
    pill.hidden = false;
    pill.dataset.state = on ? "dirty" : "saved";
    pill.textContent = on ? "Unsaved changes" : "All changes saved";
    if (!on) pill.hidden = !state.original;
  }

  function editorHTML() { return state.editor ? state.editor.getContent() : ""; }

  function formValues() {
    return { title: $("blogtitle").value.trim(), name: $("blogname").value.trim(), desc: $("blogdesc").value.trim(), content: editorHTML() };
  }

  function updateHints() {
    var n = $("blogdesc").value.length;
    $("eb-count").textContent = n + " / 500";
    var hint = $("eb-name-hint");
    if (state.original && $("blogname").value.trim() !== state.original) {
      hint.firstChild.textContent = "A new address saves a new post. The old one stays until you delete it.";
    } else {
      hint.firstChild.textContent = "Shown as /blog?name=" + ($("blogname").value.trim() || "…");
    }
    $("eb-delete").disabled = !state.original;
  }

  function markDirty() {
    if (!state.dirty) setDirty(true);
    scheduleDraft();
  }

  // ---- the draft kept on this device ----------------------------------------------

  var draftTimer = null;
  function draftKey() { return DRAFT_PREFIX + (state.original || "new"); }

  function scheduleDraft() {
    clearTimeout(draftTimer);
    draftTimer = setTimeout(function () {
      if (!state.dirty) return;
      try {
        localStorage.setItem(draftKey(), JSON.stringify(Object.assign(formValues(), { at: Date.now() })));
      } catch (e) { /* storage full or blocked: the page still works, without a draft */ }
    }, 1500);
  }

  function clearDraft(key) {
    clearTimeout(draftTimer);
    try { localStorage.removeItem(key || draftKey()); } catch (e) {}
  }

  function offerDraft() {
    var raw = null;
    try { raw = localStorage.getItem(draftKey()); } catch (e) {}
    var banner = $("eb-draft");
    banner.hidden = true;
    if (!raw) return;
    var draft;
    try { draft = JSON.parse(raw); } catch (e) { return; }
    var now = formValues();
    if (draft.content === now.content && draft.title === now.title && draft.desc === now.desc && draft.name === now.name) {
      clearDraft();
      return;
    }
    $("eb-draft-text").textContent = "A draft of this post from " + new Date(draft.at).toLocaleString() + " was kept on this device.";
    banner.hidden = false;
    $("eb-draft-restore").onclick = function () {
      fill(draft, true);
      banner.hidden = true;
      markDirty();
    };
    $("eb-draft-discard").onclick = function () { clearDraft(); banner.hidden = true; };
  }

  // ---- the list of posts -------------------------------------------------------------

  function renderList() {
    var term = $("eb-search").value.trim().toLowerCase();
    var list = $("eb-list");
    list.textContent = "";
    var shown = state.posts.filter(function (p) {
      return !term || p.title.toLowerCase().indexOf(term) >= 0 || p.name.toLowerCase().indexOf(term) >= 0;
    });
    if (!shown.length) {
      var empty = document.createElement("li");
      empty.className = "eb-empty";
      empty.textContent = state.posts.length ? "No post matches." : "No posts yet.";
      list.appendChild(empty);
      return;
    }
    shown.forEach(function (p) {
      var li = document.createElement("li");
      var b = document.createElement("button");
      b.type = "button";
      b.className = "eb-item";
      if (p.name === state.original) b.setAttribute("aria-current", "true");
      b.textContent = p.title;
      var small = document.createElement("small");
      small.textContent = shortDate(p.lastupdated) ? "Updated " + shortDate(p.lastupdated) : p.name;
      b.appendChild(small);
      b.addEventListener("click", function () { openPost(p.name); });
      li.appendChild(b);
      list.appendChild(li);
    });
  }

  function loadList() {
    return api("GET", "/blog?q=index").then(function (res) {
      state.posts = res.ok && Array.isArray(res.data) ? res.data : [];
      renderList();
    });
  }

  // ---- opening, filling, creating ---------------------------------------------------------

  function fill(post, keepOriginal) {
    $("blogtitle").value = post.title || "";
    $("blogname").value = post.name || "";
    $("blogdesc").value = post.desc || "";
    if (state.editor) {
      state.editor.setContent(post.content || "");
      state.editor.setDirty(false);
      state.editor.undoManager.clear();
    }
    if (!keepOriginal) setDirty(false);
    updateHints();
  }

  function guardDirty() {
    if (!state.dirty) return Promise.resolve(true);
    return confirmDialog("Leave without saving?", "This post has changes that are not saved. The draft stays on this device.", "Leave", true);
  }

  function openPost(name) {
    if (name === state.original && !state.dirty) return Promise.resolve();
    return guardDirty().then(function (ok) {
      if (!ok) return;
      return api("GET", "/blog?q=json&name=" + encodeURIComponent(name)).then(function (res) {
        if (!res.ok) { say("Couldn't open that post.", "error"); return; }
        state.original = res.data.name;
        state.nameTouched = true;
        fill(res.data);
        history.replaceState(null, "", "?name=" + encodeURIComponent(state.original));
        renderList();
        offerDraft();
      });
    });
  }

  function newPost() {
    return guardDirty().then(function (ok) {
      if (!ok) return;
      state.original = null;
      state.nameTouched = false;
      fill({});
      history.replaceState(null, "", location.pathname);
      renderList();
      offerDraft();
      $("blogtitle").focus();
    });
  }

  // ---- saving and deleting ------------------------------------------------------------------

  function validate(v) {
    if (!v.title) return "Add a title first.";
    if (!v.name) return "The post needs an address.";
    if (!v.desc) return "Add a short description for the list of posts.";
    var text = state.editor ? state.editor.getContent({ format: "text" }).trim() : "";
    if (!text && !/<(img|iframe|video|table)\b/i.test(v.content)) return "Write something in the post first.";
    if (new Blob([v.content]).size > MAX_POST_BYTES) return "This post is too big (pictures included). Make the pictures smaller.";
    return "";
  }

  function save() {
    if (state.saving) return;
    var v = formValues();
    var problem = validate(v);
    if (problem) { say(problem, "error"); return; }
    var overwrites = v.name !== state.original && state.posts.some(function (p) { return p.name === v.name; });
    var go = overwrites
      ? confirmDialog("Replace " + v.name + "?", "A post with this address exists already. Saving replaces it.", "Replace", true)
      : Promise.resolve(true);
    go.then(function (ok) {
      if (!ok) return;
      state.saving = true;
      $("eb-save").disabled = true;
      return api("POST", "/blog", v).then(function (res) {
        if (!res.ok) { say(res.data.message || "The post could not be saved.", "error", "Not saved"); return; }
        clearDraft();
        state.original = v.name;
        state.nameTouched = true;
        if (state.editor) state.editor.setDirty(false);
        setDirty(false);
        history.replaceState(null, "", "?name=" + encodeURIComponent(v.name));
        say(res.data.message || "Saved.", "success");
        return loadList().then(updateHints);
      }, function () {
        say("Couldn't reach the server. Your draft is kept on this device.", "error");
      }).then(function () {
        state.saving = false;
        $("eb-save").disabled = false;
      });
    });
  }

  function remove() {
    var name = state.original;
    if (!name) return;
    var title = $("blogtitle").value.trim() || name;
    confirmDialog("Delete “" + title + "”?", "The post is removed from the blog. This can't be undone.", "Delete post", true).then(function (ok) {
      if (!ok) return;
      return api("POST", "/blog", { q: "delete", name: name }).then(function (res) {
        if (!res.ok) { say(res.data.message || "The post could not be deleted.", "error"); return; }
        clearDraft(DRAFT_PREFIX + name);
        say(res.data.message || "Deleted.", "success");
        state.original = null;
        state.dirty = false;
        fill({});
        history.replaceState(null, "", location.pathname);
        return loadList();
      });
    });
  }

  // ---- preview ---------------------------------------------------------------------------------

  function preview() {
    var v = formValues();
    var theme = document.documentElement.getAttribute("data-theme") || "light";
    var esc = function (s) { return s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;"); };
    var doc = '<!doctype html><html data-theme="' + theme + '"><head><meta charset="utf-8">' +
      '<link rel="stylesheet" href="/css/scribbler-global.css"><link rel="stylesheet" href="/css/scribbler-doc.css">' +
      '<base target="_blank"></head><body><div class="wrapper"><article class="doc__content blog-page blog-article">' +
      '<h1>' + esc(v.title || "Untitled") + '</h1><p class="post-meta">Preview</p><div class="blog-body">' + v.content + '</div></article></div></body></html>';
    $("eb-preview-frame").srcdoc = doc;
    $("eb-preview-dialog").showModal();
  }

  // ---- the editor itself --------------------------------------------------------------------------

  function initEditor(html) {
    var dark = document.documentElement.getAttribute("data-theme") === "dark";
    return tinymce.init({
      selector: "#blog-editor",
      license_key: "gpl",
      height: 540,
      menubar: false,
      branding: false,
      promotion: false,
      skin: dark ? "oxide-dark" : "oxide",
      content_css: dark ? "dark" : "default",
      // only what the free build includes
      plugins: "anchor autolink charmap code codesample emoticons fullscreen image link lists media searchreplace table visualblocks wordcount",
      external_plugins: { genie: "/js/genie_plugin.js" },
      toolbar: "undo redo | blocks | bold italic underline strikethrough | link image media table codesample | bullist numlist blockquote | removeformat code | genie fullscreen",
      toolbar_mode: "wrap",
      convert_urls: false,
      relative_urls: false,
      paste_data_images: true,
      content_style: "body { font-family: system-ui, sans-serif; font-size: 16px; line-height: 1.65; max-width: 760px; margin: 12px auto; } img { max-width: 100%; height: auto; } pre { background: rgba(127,127,127,.15); padding: 10px; border-radius: 6px; }",
      openai: { api_key: window.openai_access_token, baseUri: "/chat/completions" },
      setup: function (ed) {
        ed.on("input change undo redo", markDirty);
        ed.on("init", function () {
          state.editor = ed;
          if (html) { ed.setContent(html); ed.setDirty(false); }
        });
        ed.addShortcut("meta+s", "Save", save);
      }
    });
  }

  function reinitForTheme() {
    if (!state.editor) return;
    var html = state.editor.getContent();
    var wasDirty = state.dirty;
    tinymce.remove("#blog-editor");
    state.editor = null;
    initEditor(html).then(function () { if (!wasDirty && state.editor) state.editor.setDirty(false); });
  }

  // ---- start ---------------------------------------------------------------------------------------------

  function start() {
    $("copyright_year").textContent = new Date().getFullYear();

    $("blogtitle").addEventListener("input", function () {
      if (!state.original && !state.nameTouched) $("blogname").value = slugify(this.value);
      updateHints();
      markDirty();
    });
    $("blogname").addEventListener("input", function () { state.nameTouched = true; updateHints(); markDirty(); });
    $("blogdesc").addEventListener("input", function () { updateHints(); markDirty(); });
    $("eb-search").addEventListener("input", renderList);
    $("eb-new").addEventListener("click", newPost);
    $("eb-save").addEventListener("click", save);
    $("eb-delete").addEventListener("click", remove);
    $("eb-preview").addEventListener("click", preview);
    $("eb-preview-close").addEventListener("click", function () { $("eb-preview-dialog").close(); });
    $("editor-form").addEventListener("submit", function (e) { e.preventDefault(); save(); });
    document.addEventListener("keydown", function (e) {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "s") { e.preventDefault(); save(); }
    });
    window.addEventListener("beforeunload", function (e) {
      if (state.dirty) { e.preventDefault(); e.returnValue = ""; }
    });
    // the theme button switches the editor's skin too
    document.addEventListener("click", function (e) {
      if (e.target.closest && e.target.closest("[data-theme-toggle]")) setTimeout(reinitForTheme, 80);
    });
    document.querySelector("[data-logout]").addEventListener("click", function (e) {
      e.preventDefault();
      fetch("/logout", { method: "POST", credentials: "same-origin" }).finally(function () { location.assign("/"); });
    });

    initEditor().then(function () {
      return loadList();
    }).then(function () {
      var wanted = new URLSearchParams(location.search).get("name");
      updateHints();
      if (wanted) return openPost(wanted);
      offerDraft();
    });
  }

  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", start);
  else start();
})();
