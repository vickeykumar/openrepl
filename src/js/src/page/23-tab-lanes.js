// The strips of tabs (the editor's, 22-editor-tabs.js, and the terminal's, main.ts) are lanes that scroll sideways when
// there are more tabs than room, instead of growing and pushing the buttons beside them around. The buttons stay where
// they are (css: sticky). This part makes a lane scroll with the mouse wheel, shows an edge where there is more, and
// brings the shown tab into view.

function reveal(lane, el) {
  if (!lane || !el) return;
  var l = lane.getBoundingClientRect();
  var r = el.getBoundingClientRect();
  // the pinned buttons at the right of a terminal lane cover its last part
  var covered = parseInt(getComputedStyle(lane).getPropertyValue("--tabs-pinned-w"), 10) || 0;
  if (r.left < l.left) lane.scrollLeft -= l.left - r.left + 8;
  else if (r.right > l.right - covered) lane.scrollLeft += r.right - (l.right - covered) + 8;
}

function edges(lane) {
  var max = lane.scrollWidth - lane.clientWidth;
  lane.classList.toggle("has-more-left", lane.scrollLeft > 2);
  lane.classList.toggle("has-more-right", max - lane.scrollLeft > 2);
}

function enhance(lane, activeSelector) {
  if (!lane || lane.dataset.laneReady) return;
  lane.dataset.laneReady = "1";
  lane.addEventListener("wheel", function (e) {
    if (lane.scrollWidth <= lane.clientWidth) return;
    var d = Math.abs(e.deltaX) > Math.abs(e.deltaY) ? e.deltaX : e.deltaY;
    if (!d) return;
    lane.scrollLeft += d;
    e.preventDefault();
  }, { passive: false });
  lane.addEventListener("scroll", function () { edges(lane); }, { passive: true });
  if (window.MutationObserver) {
    new MutationObserver(function () {
      var on = lane.querySelector(activeSelector);
      if (on) reveal(lane, on);
      edges(lane);
    }).observe(lane, { subtree: true, childList: true, attributes: true, attributeFilter: ["class"] });
  }
  if (window.ResizeObserver) {
    new ResizeObserver(function () { edges(lane); }).observe(lane);
  }
  edges(lane);
}

// the width of what is pinned at the right of the terminal lane (the + button and the actions), for the css and reveal()
function pinned(lane) {
  if (!lane || !window.ResizeObserver) return;
  var set = function () {
    var w = 0;
    var add = lane.querySelector(".add-tab");
    var actions = lane.querySelector(".terminal-tabs__actions");
    if (add) w += add.offsetWidth;
    if (actions) w += actions.offsetWidth;
    lane.style.setProperty("--tabs-pinned-w", w + "px");
    lane.style.setProperty("--tabs-actions-w", (actions ? actions.offsetWidth : 0) + "px");
  };
  var ro = new ResizeObserver(set);
  var add = lane.querySelector(".add-tab");
  var actions = lane.querySelector(".terminal-tabs__actions");
  if (add) ro.observe(add);
  if (actions) ro.observe(actions);
  set();
}

window.TabLane = { reveal: reveal, enhance: enhance, pinned: pinned };

document.addEventListener("DOMContentLoaded", function () {
  var term = document.getElementById("terminal-tabs");
  if (term) {
    enhance(term, ".tab.active");
    pinned(term);
  }
  enhance(document.getElementById("editor-tabs"), ".etab.active");
});
