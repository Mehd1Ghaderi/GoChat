// The sidebar's width: drag its edge, or focus the edge and use the arrow
// keys; a double click restores the default. Each browser keeps its own. It
// lives on the root element, which no swap replaces, and this runs in the
// head so a kept width is in place before the first paint.
(function () {
  "use strict";

  var KEY = "gochat.sidebar-w";
  var STEP = 16;
  var root = document.documentElement;
  var drag = null;
  var frame = 0;

  function load() {
    try {
      return parseInt(window.localStorage.getItem(KEY), 10);
    } catch (e) {
      return NaN;
    }
  }

  function save(px) {
    try {
      if (px) window.localStorage.setItem(KEY, String(px));
      else window.localStorage.removeItem(KEY);
    } catch (e) { /* private mode: the width lasts as long as the page */ }
  }

  function set(px) {
    root.style.setProperty("--sidebar-w", px + "px");
  }

  var kept = load();
  if (kept > 0) set(kept);

  function width() {
    return Math.round(document.getElementById("sidebar").getBoundingClientRect().width);
  }

  // The stylesheet bounds the width by the window's; ask for too little and
  // too much to learn the bounds.
  function limits() {
    var was = root.style.getPropertyValue("--sidebar-w");
    set(0);
    var min = width();
    set(100000);
    var max = width();
    if (was) root.style.setProperty("--sidebar-w", was);
    else root.style.removeProperty("--sidebar-w");
    return { min: min, max: Math.max(min, max) };
  }

  function clamp(px, lim) {
    return Math.min(lim.max, Math.max(lim.min, Math.round(px)));
  }

  function sync(handle, lim) {
    handle.setAttribute("aria-valuemin", lim.min);
    handle.setAttribute("aria-valuemax", lim.max);
    handle.setAttribute("aria-valuenow", width());
  }

  function handleOf(e) {
    return e.target instanceof Element ? e.target.closest(".sidebar-resize") : null;
  }

  document.addEventListener("pointerdown", function (e) {
    var handle = handleOf(e);
    if (!handle || drag || e.button !== 0) return;
    e.preventDefault();
    handle.setPointerCapture(e.pointerId);
    var lim = limits();
    drag = { handle: handle, id: e.pointerId, x: e.clientX, from: width(), to: 0, lim: lim };
    sync(handle, lim);
    root.classList.add("resizing");
  });

  document.addEventListener("pointermove", function (e) {
    if (!drag || e.pointerId !== drag.id) return;
    drag.to = clamp(drag.from + e.clientX - drag.x, drag.lim);
    if (frame) return;
    frame = requestAnimationFrame(function () {
      frame = 0;
      if (!drag) return;
      set(drag.to);
      drag.handle.setAttribute("aria-valuenow", drag.to);
    });
  });

  function endDrag(e) {
    if (!drag || e.pointerId !== drag.id) return;
    cancelAnimationFrame(frame);
    frame = 0;
    if (drag.to) {
      set(drag.to);
      save(drag.to);
      drag.handle.setAttribute("aria-valuenow", drag.to);
    }
    drag = null;
    root.classList.remove("resizing");
  }
  document.addEventListener("pointerup", endDrag);
  document.addEventListener("pointercancel", endDrag);

  document.addEventListener("dblclick", function (e) {
    var handle = handleOf(e);
    if (!handle) return;
    root.style.removeProperty("--sidebar-w");
    save(0);
    sync(handle, limits());
  });

  document.addEventListener("focusin", function (e) {
    var handle = handleOf(e);
    if (handle) sync(handle, limits());
  });

  document.addEventListener("keydown", function (e) {
    var handle = handleOf(e);
    var keys = { ArrowLeft: -1, ArrowRight: 1, Home: -Infinity, End: Infinity };
    if (!handle || !(e.key in keys) || e.ctrlKey || e.metaKey || e.altKey) return;
    e.preventDefault();
    var lim = limits();
    var px = clamp(width() + keys[e.key] * (e.shiftKey ? STEP * 4 : STEP), lim);
    set(px);
    save(px);
    handle.setAttribute("aria-valuenow", px);
  });
})();
