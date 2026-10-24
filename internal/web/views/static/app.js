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

  // The main pane: a channel, or the threads the user follows.
  function place() {
    return view() || document.getElementById("threads-view");
  }

  function send(msg) {
    if (socket) socket.send(JSON.stringify(msg));
  }

  // Tell the server what this tab shows, so it knows where new messages go
  // and what to mark read.
  function announce(resync) {
    var v = place();
    if (!v) return;
    send({
      op: "view",
      team_id: v.dataset.team,
      team_name: v.dataset.teamName,
      channel_id: v.dataset.channel || "",
      last_id: v.dataset.newest || "",
      moderator: v.dataset.moderator ? "1" : "",
      anchored: v.dataset.anchored ? "1" : "",
      resync: resync ? "1" : ""
    });
  }

  // The open thread, if any, so its replies arrive as they are written.
  function threadRoot() {
    var t = document.getElementById("thread-view");
    return t ? t.dataset.root : "";
  }

  function announceThread() {
    var t = document.getElementById("thread-view");
    send({ op: "thread", root_id: threadRoot(), channel_id: t ? t.dataset.channel : "" });
  }

  function closeThread() {
    var pane = document.getElementById("thread");
    if (!pane || pane.innerHTML === "") return false;
    pane.innerHTML = "";
    announceThread();
    return true;
  }

  var wide = window.matchMedia("(min-width: 821px)");
  // Where the side pane sits beside the channel rather than over it.
  var roomy = window.matchMedia("(min-width: 1181px)");

  // Search shares the side pane with threads; it stays open beside the
  // channel a result opened, where there is room.
  function openSearch() {
    var v = place();
    var input = document.querySelector("[data-search-input]");
    if (input) {
      input.focus();
      input.select();
      return;
    }
    if (v) htmx.ajax("GET", "/t/" + v.dataset.teamName + "/search", { target: "#thread", swap: "innerHTML" });
  }

  // A link to a message opens its channel around it. htmx settles a
  // message's new class late, so its data attribute marks it.
  function focusMessage() {
    var m = document.querySelector("#main .msg[data-focus]");
    if (m) m.scrollIntoView({ block: "center" });
  }

  function scrollThread(always) {
    var box = document.getElementById("thread-scroll");
    if (!box) return;
    if (always || box.scrollHeight - box.scrollTop - box.clientHeight < 120) box.scrollTop = box.scrollHeight;
  }

  // The tab's title counts unread mentions, so they show from any tab.
  function updateTitle() {
    var v = place();
    if (!v || !v.dataset.title) return;
    var count = 0;
    document.querySelectorAll(".channel-lists .mentions").forEach(function (b) {
      count += parseInt(b.textContent, 10) || 0;
    });
    document.title = (count ? "(" + count + ") " : "") + v.dataset.title + " · GoChat";
  }

  // Desktop notifications: the server says which messages deserve one; the
  // tab shows it unless it already shows that conversation to the user.
  function notifyPref() {
    try {
      return window.localStorage.getItem("gochat.notify") || "on";
    } catch (e) {
      return "on";
    }
  }

  function setNotifyPref(value) {
    try {
      window.localStorage.setItem("gochat.notify", value);
    } catch (e) { /* private mode: the default stands */ }
  }

  function showNotice(d) {
    if (!("Notification" in window) || Notification.permission !== "granted" || notifyPref() === "off") return;
    var v = view();
    var looking = document.visibilityState === "visible" && document.hasFocus();
    var here = d.root ? threadRoot() === d.root : !!v && v.dataset.channel === d.channel;
    if (looking && here) return;
    var n = new Notification(d.title, { body: d.body, tag: d.tag });
    n.onclick = function () {
      window.focus();
      n.close();
      window.location.assign(d.url);
    };
  }

  function refreshNotifySetting() {
    var button = document.querySelector("[data-notify-toggle]");
    var state = document.querySelector("[data-notify-state]");
    if (!button || !state) return;
    if (!("Notification" in window)) {
      state.textContent = "This browser does not support them.";
      return;
    }
    var perm = Notification.permission;
    var on = perm === "granted" && notifyPref() !== "off";
    button.hidden = perm === "denied";
    button.textContent = on ? "Turn off" : "Turn on";
    state.textContent = perm === "denied" ? "Blocked: allow notifications for this site in your browser." : on ? "On" : "Off";
    state.className = "status" + (on ? " ok" : "");
  }

  function openLinkedThread() {
    var v = view();
    if (!v || !v.dataset.openThread) return;
    var root = v.dataset.openThread;
    delete v.dataset.openThread;
    htmx.ajax("GET", "/m/" + v.dataset.channel + "/" + root + "/thread", { target: "#thread", swap: "innerHTML" });
  }

  function markActive() {
    var v = place();
    if (!v) return;
    updateTitle();
    var id = v.dataset.channel || "threads";
    document.querySelectorAll(".sb-link").forEach(function (a) {
      var on = (a.dataset.channel || a.dataset.place) === id;
      a.classList.toggle("active", on);
      if (on) a.setAttribute("aria-current", "page");
      else a.removeAttribute("aria-current");
      // Opening a channel reads it; opening Threads does not read them.
      var dot = on && a.dataset.channel ? a.querySelector(".dot, .mentions") : null;
      if (dot) {
        dot.className = "dot";
        dot.textContent = "";
        dot.removeAttribute("title");
        dot.setAttribute("aria-hidden", "true");
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
    closeSuggest(wrap && wrap.querySelector(".suggest-box"));
    var ta = form.querySelector("textarea");
    if (ta) ta.style.height = "";
  }

  function isComposer(form) {
    return !!form && form.classList.contains("composer");
  }

  // @ completion: the channel's people whose name starts with what follows
  // the @ being typed.
  var suggestSeq = 0;
  var suggestTimer = 0;

  function mentionQuery(ta) {
    var m = /(^|[^A-Za-z0-9._@-])@([A-Za-z0-9._-]{0,32})$/.exec(ta.value.slice(0, ta.selectionStart));
    return m ? m[2].toLowerCase() : null;
  }

  function suggestBox(ta) {
    var wrap = ta.closest(".composer-wrap");
    if (!wrap) return null;
    var box = wrap.querySelector(".suggest-box");
    if (!box) {
      box = document.createElement("div");
      box.className = "suggest-box";
      box.hidden = true;
      wrap.insertBefore(box, wrap.firstChild);
    }
    return box;
  }

  function closeSuggest(box) {
    if (!box || box.hidden) return false;
    box.hidden = true;
    box.innerHTML = "";
    return true;
  }

  function updateSuggest(ta) {
    var box = suggestBox(ta);
    var q = mentionQuery(ta);
    var channel = ta.form.elements.channel_id;
    clearTimeout(suggestTimer);
    if (!box || q === null || !channel) {
      suggestSeq++;
      closeSuggest(box);
      return;
    }
    var seq = ++suggestSeq;
    suggestTimer = setTimeout(function () {
      fetch("/m/" + channel.value + "/people?q=" + encodeURIComponent(q), { headers: { "HX-Request": "true" }, credentials: "same-origin" })
        .then(function (r) { return r.ok ? r.text() : ""; })
        .then(function (html) {
          if (seq !== suggestSeq) return;
          box.innerHTML = html;
          box.hidden = html.trim() === "";
        })
        .catch(function () { closeSuggest(box); });
    }, 120);
  }

  function acceptSuggest(ta, li) {
    var end = ta.selectionStart;
    var before = ta.value.slice(0, end).replace(/@[A-Za-z0-9._-]{0,32}$/, li.dataset.insert);
    ta.value = before + ta.value.slice(end);
    ta.selectionStart = ta.selectionEnd = before.length;
    suggestSeq++;
    closeSuggest(suggestBox(ta));
    ta.focus();
  }

  // The people picked for a conversation are chips in its form.
  var maxPicked = 7;

  function pickedIDs() {
    var box = document.getElementById("dm-picked");
    if (!box) return [];
    return Array.prototype.map.call(box.querySelectorAll("input[name=user_id]"), function (i) { return i.value; });
  }

  function syncPicks() {
    var ids = pickedIDs();
    document.querySelectorAll(".pick[data-pick]").forEach(function (b) {
      b.setAttribute("aria-pressed", ids.indexOf(b.dataset.pick) >= 0 ? "true" : "false");
    });
    var go = document.querySelector("[data-dm-go]");
    if (go) {
      go.disabled = ids.length === 0;
      go.textContent = ids.length > 1 ? "Start a group of " + (ids.length + 1) : "Start conversation";
    }
  }

  function togglePick(button) {
    var box = document.getElementById("dm-picked");
    if (!box) return;
    var id = button.dataset.pick;
    var chip = Array.prototype.find.call(box.querySelectorAll(".chip"), function (c) { return c.dataset.id === id; });
    if (chip) {
      chip.remove();
    } else if (pickedIDs().length >= maxPicked) {
      toast("A group holds up to eight people.", "error");
      expireToasts();
      return;
    } else {
      chip = document.createElement("span");
      chip.className = "chip";
      chip.dataset.id = id;
      var input = document.createElement("input");
      input.type = "hidden";
      input.name = "user_id";
      input.value = id;
      var name = document.createElement("span");
      name.className = "chip-name";
      name.textContent = button.dataset.pickName;
      var remove = document.createElement("button");
      remove.type = "button";
      remove.className = "chip-remove";
      remove.setAttribute("aria-label", "Remove " + button.dataset.pickName);
      remove.textContent = "\u00d7";
      chip.append(input, name, remove);
      box.appendChild(chip);
    }
    syncPicks();
  }

  // The quick switcher: Ctrl or Cmd and K.
  function openSwitcher() {
    var input = document.querySelector("[data-switch-input]");
    if (input) {
      input.select();
      return;
    }
    var v = place();
    if (v) htmx.ajax("GET", "/t/" + v.dataset.teamName + "/switch", { target: "#modal", swap: "innerHTML" });
  }

  function moveOption(list, step) {
    var items = Array.prototype.slice.call(list.querySelectorAll("[role=option]"));
    if (!items.length) return;
    var at = items.findIndex(function (li) { return li.getAttribute("aria-selected") === "true"; });
    var next = (at + step + items.length) % items.length;
    items.forEach(function (li, i) { li.setAttribute("aria-selected", i === next ? "true" : "false"); });
    items[next].scrollIntoView({ block: "nearest" });
  }

  var mac = /Mac|iPhone|iPad/.test(navigator.platform);

  htmx.onLoad(function (elt) {
    localTimes(elt);
    if (mac) elt.querySelectorAll("[data-shortcut]").forEach(function (k) { k.textContent = "\u2318K"; });
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
      var input = document.querySelector("[data-search-input]");
      if (input) {
        input.focus();
        input.setSelectionRange(input.value.length, input.value.length);
        return;
      }
      scrollThread(true);
      var reply = document.querySelector("#reply-composer textarea");
      if (reply && wide.matches) reply.focus();
      return;
    }
    if (e.detail.target && e.detail.target.id === "people-results") {
      syncPicks();
      return;
    }
    if (e.detail.target && e.detail.target.id === "main") {
      if (document.getElementById("switcher")) closeModal();
      if (!(roomy.matches && document.getElementById("search-view"))) closeThread();
      markActive();
      announce(false);
      openLinkedThread();
      focusMessage();
      var ta = document.querySelector("#composer textarea");
      if (ta && wide.matches && !document.querySelector("#main .msg[data-focus]")) ta.focus();
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
    if (t.id === "notify") showNotice(t.dataset);
    if (t.id === "channel-lists" || t.id.indexOf("unread-") === 0) updateTitle();
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
    if (target.closest("[data-notify-toggle]")) {
      if (Notification.permission === "default") {
        Notification.requestPermission().then(function () {
          setNotifyPref("on");
          refreshNotifySetting();
        });
      } else {
        setNotifyPref(notifyPref() === "off" ? "on" : "off");
        refreshNotifySetting();
      }
      return;
    }
    var option = target.closest(".suggest-box [role=option]");
    if (option) {
      var ta = option.closest(".composer-wrap").querySelector("form.composer textarea");
      if (ta) acceptSuggest(ta, option);
      return;
    }
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
      syncPicks();
      return;
    }
    var pick = target.closest(".pick[data-pick]");
    if (pick) {
      togglePick(pick);
      return;
    }
    var card = target.closest("[data-thread-card]");
    if (card && card.classList.contains("unread")) {
      card.classList.remove("unread");
      var mark = card.querySelector(".dot");
      if (mark) mark.remove();
      var dot = document.getElementById("threads-unread");
      if (dot && !document.querySelector("[data-thread-card].unread")) dot.className = "dot";
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

  // Choosing with the mouse keeps the composer focused; leaving it closes
  // the list.
  document.addEventListener("mousedown", function (e) {
    if (e.target instanceof Element && e.target.closest(".suggest-box")) e.preventDefault();
  });
  document.addEventListener("focusout", function (e) {
    var t = e.target;
    if (t instanceof HTMLTextAreaElement && isComposer(t.form)) {
      suggestSeq++;
      closeSuggest(suggestBox(t));
    }
  });

  document.addEventListener("focusin", function (e) {
    if (e.target instanceof HTMLInputElement && e.target.hasAttribute("data-select-on-focus")) e.target.select();
  });

  document.addEventListener("keydown", function (e) {
    var t = e.target;
    var box = t instanceof HTMLTextAreaElement && isComposer(t.form) ? suggestBox(t) : null;
    if (box && !box.hidden) {
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        moveOption(box, e.key === "ArrowDown" ? 1 : -1);
        return;
      }
      if ((e.key === "Enter" || e.key === "Tab") && !e.shiftKey && !e.isComposing) {
        var chosen = box.querySelector("[aria-selected=true]");
        if (chosen) {
          e.preventDefault();
          acceptSuggest(t, chosen);
          return;
        }
      }
      if (e.key === "Escape") {
        suggestSeq++;
        closeSuggest(box);
        return;
      }
    }
    if (e.key === "Escape") {
      var m = document.getElementById("modal");
      if (m && m.innerHTML !== "") closeModal();
      else closeThread();
    }
    if ((e.ctrlKey || e.metaKey) && e.shiftKey && (e.key === "F" || e.key === "f")) {
      e.preventDefault();
      openSearch();
      return;
    }
    if ((e.ctrlKey || e.metaKey) && !e.shiftKey && (e.key === "k" || e.key === "K")) {
      e.preventDefault();
      openSwitcher();
      return;
    }
    if (t instanceof HTMLInputElement && t.hasAttribute("data-switch-input")) {
      var list = document.getElementById("switch-results");
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        moveOption(list, e.key === "ArrowDown" ? 1 : -1);
        return;
      }
      if (e.key === "Enter" && !e.isComposing) {
        e.preventDefault();
        var item = list.querySelector("[aria-selected=true] .switch-item");
        if (item) item.click();
        return;
      }
    }
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
    updateSuggest(t);
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
    refreshNotifySetting();
    openLinkedThread();
    focusMessage();
  });
})();
