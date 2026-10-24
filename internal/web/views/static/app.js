// GoChat's client glue around htmx. No inline handlers: the page runs under a
// strict Content-Security-Policy.
(function () {
  "use strict";

  var socket = null;
  var opened = false;
  // Phones and tablets: no hover, and no Shift on their keyboards.
  var touch = window.matchMedia("(hover: none) and (pointer: coarse)");

  function view() {
    return document.getElementById("channel-view");
  }

  function send(msg) {
    if (socket) socket.send(JSON.stringify(msg));
  }

  // Tell the server what this tab shows, so it knows where new messages go
  // and what to mark read.
  function announce(resync) {
    var v = view();
    if (!v) return;
    send({
      op: "view",
      team_id: v.dataset.team,
      team_name: v.dataset.teamName,
      channel_id: v.dataset.channel,
      last_id: v.dataset.newest || "",
      moderator: v.dataset.moderator ? "1" : "",
      resync: resync ? "1" : ""
    });
  }

  // The open thread, if any, so its replies arrive as they are written.
  function threadRoot() {
    var t = document.getElementById("thread-view");
    return t ? t.dataset.root : "";
  }

  function announceThread() {
    send({ op: "thread", root_id: threadRoot() });
  }

  function closeThread() {
    var pane = document.getElementById("thread");
    if (!pane || pane.innerHTML === "") return false;
    pane.innerHTML = "";
    announceThread();
    return true;
  }

  function scrollThread(always) {
    var box = document.getElementById("thread-scroll");
    if (!box) return;
    if (always || box.scrollHeight - box.scrollTop - box.clientHeight < 120) box.scrollTop = box.scrollHeight;
  }

  function markActive() {
    var v = view();
    if (!v) return;
    if (v.dataset.title) document.title = v.dataset.title + " · GoChat";
    var id = v.dataset.channel;
    document.querySelectorAll(".sb-link").forEach(function (a) {
      var on = a.dataset.channel === id;
      a.classList.toggle("active", on);
      if (on) {
        a.setAttribute("aria-current", "page");
        var dot = a.querySelector(".dot");
        if (dot) dot.classList.remove("on");
      } else {
        a.removeAttribute("aria-current");
      }
    });
    var toggle = document.getElementById("nav-toggle");
    if (toggle) toggle.checked = false;
  }

  function localTimes(root) {
    root.querySelectorAll("time[datetime]").forEach(function (t) {
      var d = new Date(t.getAttribute("datetime"));
      if (isNaN(d)) return;
      t.textContent = d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
      t.title = d.toLocaleString();
    });
  }

  function closeModal() {
    var m = document.getElementById("modal");
    if (m) m.innerHTML = "";
  }

  function toast(text, kind) {
    var box = document.getElementById("toasts");
    if (!box) return;
    var t = document.createElement("div");
    t.className = "toast " + (kind || "");
    t.setAttribute("role", "status");
    t.textContent = text;
    box.appendChild(t);
  }

  function resetComposer(form) {
    form.reset();
    var wrap = form.closest(".composer-wrap");
    var pending = wrap && wrap.querySelector(".pending");
    if (pending) pending.innerHTML = "";
    var ta = form.querySelector("textarea");
    if (ta) ta.style.height = "";
  }

  function isComposer(form) {
    return !!form && form.classList.contains("composer");
  }

  htmx.onLoad(function (elt) {
    localTimes(elt);
  });

  document.body.addEventListener("htmx:wsOpen", function (e) {
    socket = e.detail.socketWrapper;
    announce(opened);
    if (threadRoot()) announceThread();
    opened = true;
  });

  document.body.addEventListener("htmx:wsClose", function () {
    socket = null;
  });

  // Validate and clear the composer around a send.
  document.body.addEventListener("htmx:wsConfigSend", function (e) {
    var form = e.detail.elt;
    if (!isComposer(form)) return;
    var p = e.detail.parameters;
    if (p.op !== "post") return;
    var hasText = (p.content || "").trim() !== "";
    if (!hasText && !p.attachment) {
      e.preventDefault();
      return;
    }
    setTimeout(function () { resetComposer(form); }, 0);
  });

  document.body.addEventListener("htmx:afterSwap", function (e) {
    if (e.detail.target && e.detail.target.id === "thread") {
      announceThread();
      scrollThread(true);
      var reply = document.querySelector("#reply-composer textarea");
      if (reply && window.matchMedia("(min-width: 821px)").matches) reply.focus();
      return;
    }
    if (e.detail.target && e.detail.target.id === "main") {
      closeThread();
      markActive();
      announce(false);
      var ta = document.querySelector("#composer textarea");
      if (ta && window.matchMedia("(min-width: 821px)").matches) ta.focus();
    }
  });

  // The event fires on the swapped-in elements.
  document.body.addEventListener("htmx:oobAfterSwap", function (e) {
    var t = e.target;
    if (!(t instanceof Element)) return;
    if (t.id === "ws-control" && t.dataset.redirect && t.dataset.redirect.charAt(0) === "/") {
      window.location.assign(t.dataset.redirect);
    }
    if (t.classList.contains("toast")) expireToasts();
    if (t.id.indexOf("replies-") === 0) scrollThread(false);
  });

  document.body.addEventListener("closeModal", closeModal);

  document.body.addEventListener("htmx:responseError", function (e) {
    var xhr = e.detail.xhr;
    if (xhr && xhr.status === 401) {
      window.location.assign("/login");
      return;
    }
    var msg = (xhr && xhr.getResponseHeader("X-Error")) || "Something went wrong.";
    toast(msg, "error");
    expireToasts();
  });

  document.body.addEventListener("htmx:sendError", function () {
    toast("You appear to be offline.", "error");
    expireToasts();
  });

  // Upload progress and cleanup.
  function progressOf(elt) {
    var wrap = elt instanceof Element ? elt.closest(".composer-wrap") : null;
    return wrap && wrap.querySelector(".upload-progress");
  }
  document.body.addEventListener("htmx:xhr:progress", function (e) {
    var bar = progressOf(e.target);
    if (!bar || !e.detail.lengthComputable) return;
    bar.hidden = false;
    bar.value = Math.round((e.detail.loaded / e.detail.total) * 100);
  });
  document.body.addEventListener("htmx:afterRequest", function (e) {
    var elt = e.detail.elt;
    if (elt && elt.classList && elt.classList.contains("upload-form")) {
      elt.reset();
      var bar = progressOf(elt);
      if (bar) { bar.hidden = true; bar.value = 0; }
    }
  });

  function expireToasts() {
    document.querySelectorAll(".toast:not([data-expiring])").forEach(function (t) {
      t.dataset.expiring = "1";
      setTimeout(function () { t.remove(); }, 5000);
    });
  }

  document.addEventListener("click", function (e) {
    var target = e.target;
    if (!(target instanceof Element)) return;
    if (target.closest("[data-close-thread]")) {
      closeThread();
      return;
    }
    var closer = target.closest("[data-close-modal]");
    if (closer && (closer === target || closer.tagName === "BUTTON")) {
      closeModal();
      return;
    }
    var remove = target.closest(".chip-remove");
    if (remove) {
      remove.closest(".chip").remove();
      return;
    }
    var copy = target.closest("[data-copy]");
    if (copy && navigator.clipboard) {
      navigator.clipboard.writeText(copy.dataset.copy).then(function () {
        copy.textContent = "Copied";
      });
      return;
    }
    if (target.closest(".menu-items")) {
      var menu = target.closest("details");
      if (menu) menu.open = false;
    }
    if (touch.matches) toggleActions(target);
  });

  // A tap on a message shows its actions, which a mouse shows on hover.
  function toggleActions(target) {
    var msg = target.closest(".msg");
    if (msg && target.closest("a, button, textarea, input, .msg-actions")) return;
    var open = msg && !msg.classList.contains("show-actions");
    document.querySelectorAll(".msg.show-actions").forEach(function (m) { m.classList.remove("show-actions"); });
    if (open && msg.querySelector(".msg-actions")) msg.classList.add("show-actions");
  }

  document.addEventListener("focusin", function (e) {
    if (e.target instanceof HTMLInputElement && e.target.hasAttribute("data-select-on-focus")) e.target.select();
  });

  document.addEventListener("keydown", function (e) {
    if (e.key === "Escape") {
      var m = document.getElementById("modal");
      if (m && m.innerHTML !== "") closeModal();
      else closeThread();
    }
    var t = e.target;
    if (!(t instanceof HTMLTextAreaElement) || !isComposer(t.form)) return;
    if (e.key === "Enter" && !e.shiftKey && !e.isComposing && !touch.matches) {
      e.preventDefault();
      t.form.requestSubmit();
    }
  });

  // Grow the composer with its content, and tell the channel we type, at
  // most every two seconds.
  var lastTyping = 0;
  document.addEventListener("input", function (e) {
    var t = e.target;
    if (!(t instanceof HTMLTextAreaElement) || !isComposer(t.form)) return;
    t.style.height = "auto";
    t.style.height = Math.min(t.scrollHeight, window.innerHeight * 0.4) + "px";
    var now = Date.now();
    if (t.value.trim() !== "" && now - lastTyping > 2000) {
      lastTyping = now;
      send({ op: "typing", channel_id: t.form.elements.channel_id.value });
    }
  });

  document.addEventListener("DOMContentLoaded", function () {
    localTimes(document);
    markActive();
  });
})();
