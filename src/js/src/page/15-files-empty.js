// The empty Files panel note (T18).
//
// Part of the page script (T20). webpack (src/js) bundles src/js/src/page/*.js
// into js/scribbler.js. Top-level names are page globals (window.*), as they
// were in the single file: index.html, palette.js and the other parts use them.

window.updateFilesEmptyState = updateFilesEmptyState;

// ---------------------------------------------------------------------------
// Empty Files panel (T18): a short note while the workspace folder is empty.
// ---------------------------------------------------------------------------
function updateFilesEmptyState() {
  var el = $('#file-browser');
  var tree = el.jstree ? el.jstree(true) : null;
  var note = document.getElementById("files-empty");
  if (!tree || !note || !tree.get_node) return;
  var roots = (tree.get_node("#") || {}).children || [];
  var root = roots.length ? tree.get_node(roots[0]) : null;
  note.hidden = !(root && root.state && root.state.loaded !== false && root.children.length === 0);
}
$(function () {
  $("#file-browser").on("ready.jstree refresh.jstree load_node.jstree create_node.jstree delete_node.jstree move_node.jstree copy_node.jstree", function () {
    setTimeout(updateFilesEmptyState, 0);
  });
  $("#files-empty-upload").on("click", function () {
    var up = document.getElementById("upload-button");
    if (up) up.click();
  });
});

export {};  // an ES module: strict mode, bundled by webpack
