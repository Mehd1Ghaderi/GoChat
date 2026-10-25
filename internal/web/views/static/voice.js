// GoChat's voice client, which joins voice channels the way Discord's does:
// the chat server hands out a voice server and a token, and the page connects
// to that server's signaling websocket, sends the microphone over WebRTC and
// plays everyone else in the channel. No inline handlers: the page runs under
// a strict Content-Security-Policy.
(function () {
  "use strict";

  var call = null;
  var muted = load("muted");
  var deafened = load("deafened");
  // Who speaks, by user id, so redrawn lists keep showing it.
  var speakers = {};
  // The page's other files sit beside this one, versioned alike.
  var staticBase = document.currentScript ? document.currentScript.src.replace(/[^/]*$/, "") : "";
  var noiseModes = ["off", "standard", "strong"];
  var rnnoise = null;

  // Discord's own: why a voice server closed the connection.
  var closeReasons = {
    4001: "Could not join the voice channel: try again.",
    4003: "That voice channel is full.",
    4004: "Voice is unavailable right now.",
    4005: "The voice connection failed."
  };

  function load(key) {
    try {
      return window.localStorage.getItem("gochat.voice." + key) === "1";
    } catch (e) {
      return false;
    }
  }

  function save(key, on) {
    try {
      window.localStorage.setItem("gochat.voice." + key, on ? "1" : "0");
    } catch (e) { /* private mode: the choice lasts this page */ }
  }

  // noiseMode is how much noise to take out of the microphone: none, the
  // browser's own filter, or RNNoise's.
  function noiseMode() {
    var mode = null;
    try {
      mode = window.localStorage.getItem("gochat.voice.noise");
    } catch (e) { /* private mode: the default stands */ }
    return noiseModes.indexOf(mode) < 0 ? "standard" : mode;
  }

  function canDenoise() {
    return !!(window.AudioWorkletNode && window.WebAssembly && staticBase);
  }

  // showNoiseSetting marks this browser's choice on the settings page.
  function showNoiseSetting(saved) {
    var box = document.querySelector("[data-voice-noise]");
    var state = document.querySelector("[data-voice-noise-state]");
    if (!box || !state) return;
    var mode = noiseMode();
    box.querySelectorAll('input[name="noise"]').forEach(function (input) {
      if (input.value === "strong") input.disabled = !canDenoise();
      input.checked = input.value === (mode === "strong" && !canDenoise() ? "standard" : mode);
    });
    state.textContent = saved ? "Saved." : canDenoise() ? "" : "This browser cannot run strong suppression.";
    state.className = "status" + (saved ? " ok" : "");
  }

  function toast(text) {
    var box = document.getElementById("toasts");
    if (!box) return;
    var t = document.createElement("div");
    t.className = "toast error";
    t.setAttribute("role", "status");
    t.textContent = text;
    box.appendChild(t);
    setTimeout(function () { t.remove(); }, 5000);
  }

  function headers() {
    var h = {};
    try {
      h = JSON.parse(document.body.getAttribute("hx-headers") || "{}");
    } catch (e) { /* no token: the server refuses */ }
    h["HX-Request"] = "true";
    h["Content-Type"] = "application/x-www-form-urlencoded";
    return h;
  }

  function post(url, data) {
    return fetch(url, { method: "POST", headers: headers(), body: new URLSearchParams(data || {}), credentials: "same-origin" })
      .then(function (r) {
        if (r.status === 401) {
          window.location.assign("/login");
          throw new Error("Sign in again.");
        }
        if (!r.ok) throw new Error(r.headers.get("X-Error") || "Something went wrong.");
        return r;
      });
  }

  function stopTracks(stream) {
    if (stream) stream.getTracks().forEach(function (t) { t.stop(); });
  }

  function send(state, msg) {
    if (state.ws && state.ws.readyState === WebSocket.OPEN) state.ws.send(JSON.stringify(msg));
  }

  // denoise runs mic through RNNoise in an audio worklet, at the 48 kHz it
  // takes, once the worklet says it is ready.
  function denoise(mic) {
    var ctx;
    try {
      ctx = new AudioContext({ sampleRate: 48000 });
    } catch (e) {
      return Promise.reject(e);
    }
    if (!rnnoise) {
      rnnoise = fetch(staticBase + "rnnoise.wasm").then(function (r) {
        if (!r.ok) throw new Error("rnnoise.wasm: " + r.status);
        return r.arrayBuffer();
      });
    }
    var wasm = rnnoise;
    return Promise.all([wasm, ctx.audioWorklet.addModule(staticBase + "denoise.js")])
      .then(function (loaded) {
        var node = new AudioWorkletNode(ctx, "gochat-denoise", {
          numberOfInputs: 1, numberOfOutputs: 1, outputChannelCount: [1],
          channelCount: 1, channelCountMode: "explicit", processorOptions: { wasm: loaded[0] }
        });
        var out = ctx.createMediaStreamDestination();
        ctx.createMediaStreamSource(mic).connect(node).connect(out);
        return new Promise(function (resolve, reject) {
          node.port.onmessage = function (e) {
            if (e.data && e.data.ready) resolve({ out: out.stream, ctx: ctx });
            else reject(new Error(e.data && e.data.error));
          };
          node.onprocessorerror = function () { reject(new Error("the noise filter failed")); };
          // A context the browser keeps suspended never builds it.
          setTimeout(function () { reject(new Error("the noise filter did not start")); }, 5000);
          ctx.resume().catch(function () {});
        });
      })
      .catch(function (err) {
        if (wasm === rnnoise) rnnoise = null;
        ctx.close().catch(function () {});
        throw err;
      });
  }

  // microphone opens the microphone as mode asks, with what to send: the
  // microphone itself, or what RNNoise makes of it.
  function microphone(mode) {
    var audio = { echoCancellation: true, noiseSuppression: mode === "standard", autoGainControl: true };
    if (mode === "strong") {
      if (!canDenoise()) return microphone("standard");
      audio.sampleRate = 48000;
      audio.channelCount = 1;
    }
    return navigator.mediaDevices.getUserMedia({ audio: audio }).then(function (mic) {
      if (mode !== "strong") return { raw: mic, out: mic };
      return denoise(mic).then(function (d) {
        return { raw: mic, out: d.out, ctx: d.ctx };
      }, function () {
        stopTracks(mic);
        toast("Strong noise suppression does not work in this browser: using standard.");
        return microphone("standard");
      });
    });
  }

  function release(m) {
    if (m.ctx) m.ctx.close().catch(function () {});
    if (m.out !== m.raw) stopTracks(m.out);
    stopTracks(m.raw);
  }

  function join(channelID) {
    if (call && call.channel === channelID) return;
    if (!navigator.mediaDevices || !navigator.mediaDevices.getUserMedia || !window.RTCPeerConnection) {
      toast("This browser cannot join voice channels.");
      return;
    }
    // The chat server moves whoever joins elsewhere: only the page's own
    // connection has to go.
    if (call) hangUp(call);
    var state = { channel: channelID, name: "", remote: {}, early: [], queue: Promise.resolve(), connected: false, closing: false };
    call = state;
    render();
    microphone(noiseMode())
      .then(function (m) {
        if (call !== state) {
          release(m);
          return null;
        }
        state.mic = m;
        applyMute();
        return post("/voice/join", { channel_id: channelID });
      })
      .then(function (r) { return r && call === state ? r.json() : null; })
      .then(function (ticket) {
        if (!ticket || call !== state) return;
        state.name = ticket.name;
        connect(state, ticket);
        render();
      })
      .catch(function (err) {
        if (call === state) hangUp(state);
        toast(err && err.name === "NotAllowedError" ? "Allow this site to use the microphone to join voice channels." : err.message);
      });
  }

  function connect(state, ticket) {
    var ws = new WebSocket(ticket.endpoint);
    state.ws = ws;
    ws.onopen = function () { send(state, { op: "identify", token: ticket.token }); };
    ws.onmessage = function (e) {
      var msg;
      try {
        msg = JSON.parse(e.data);
      } catch (err) {
        return;
      }
      // Offers are taken one at a time, in order.
      state.queue = state.queue.then(function () { return handle(state, msg); }).catch(function () {});
    };
    ws.onclose = function (e) {
      if (call !== state || state.closing) return;
      hangUp(state);
      if (e.code === 4002) toast("Disconnected from voice: " + (e.reason || "the server ended it") + ".");
      else toast(closeReasons[e.code] || "Disconnected from voice.");
    };
  }

  function handle(state, msg) {
    switch (msg.op) {
      case "ready":
        state.userID = msg.user_id;
        setup(state, msg.ice_servers || []);
        // The others see a mute kept from earlier as soon as one is.
        if (muted || deafened) report();
        return null;
      case "offer":
        return state.pc.setRemoteDescription({ type: "offer", sdp: msg.sdp })
          .then(function () {
            var early = state.early.splice(0);
            return Promise.all(early.map(function (c) { return state.pc.addIceCandidate(c).catch(function () {}); }));
          })
          .then(function () { return state.pc.createAnswer(); })
          .then(function (answer) { return state.pc.setLocalDescription(answer); })
          .then(function () { send(state, { op: "answer", sdp: state.pc.localDescription.sdp }); });
      case "candidate":
        if (!msg.candidate || !state.pc) return null;
        // A candidate before the offer it belongs to waits for it.
        if (!state.pc.remoteDescription) {
          state.early.push(msg.candidate);
          return null;
        }
        return state.pc.addIceCandidate(msg.candidate);
      case "speaking":
        setSpeaking(msg.user_id, !!msg.speaking);
        return null;
    }
    return null;
  }

  function setup(state, iceServers) {
    var pc = new RTCPeerConnection({ iceServers: iceServers });
    state.pc = pc;
    state.mic.out.getTracks().forEach(function (t) { pc.addTrack(t, state.mic.out); });
    pc.onicecandidate = function (e) {
      if (e.candidate) send(state, { op: "candidate", candidate: e.candidate.toJSON() });
    };
    // Each voice arrives as its own stream, named after whose it is.
    pc.ontrack = function (e) {
      if (e.streams[0]) play(state, e.streams[0]);
    };
    pc.onconnectionstatechange = function () {
      if (call !== state) return;
      state.connected = pc.connectionState === "connected";
      if (pc.connectionState === "failed") {
        leave();
        toast("The voice connection failed: your network may block it.");
      }
      render();
    };
    listen(state);
  }

  function audioBox() {
    var box = document.getElementById("voice-audio");
    if (!box) {
      box = document.createElement("div");
      box.id = "voice-audio";
      box.hidden = true;
      document.body.appendChild(box);
    }
    return box;
  }

  function play(state, stream) {
    var id = stream.id;
    var el = state.remote[id];
    if (!el) {
      el = document.createElement("audio");
      el.autoplay = true;
      audioBox().appendChild(el);
      state.remote[id] = el;
    }
    el.srcObject = stream;
    el.muted = deafened;
    var playing = el.play();
    if (playing && playing.catch) playing.catch(function () {});
    stream.onremovetrack = function () {
      if (stream.getTracks().length) return;
      el.remove();
      delete state.remote[id];
      setSpeaking(id, false);
    };
  }

  // listen tells the others when the microphone hears a voice, as
  // Discord's speaking indicator does.
  function listen(state) {
    var Ctx = window.AudioContext || window.webkitAudioContext;
    if (!Ctx) return;
    var ctx = new Ctx();
    var analyser = ctx.createAnalyser();
    analyser.fftSize = 512;
    ctx.createMediaStreamSource(state.mic.out).connect(analyser);
    var data = new Uint8Array(analyser.fftSize);
    var loudAt = 0;
    state.audioCtx = ctx;
    state.speaking = false;
    state.vad = setInterval(function () {
      analyser.getByteTimeDomainData(data);
      var sum = 0;
      for (var i = 0; i < data.length; i++) {
        var v = (data[i] - 128) / 128;
        sum += v * v;
      }
      var now = Date.now();
      if (Math.sqrt(sum / data.length) > 0.02) loudAt = now;
      var on = !muted && !deafened && now - loudAt < 300;
      if (on !== state.speaking) {
        state.speaking = on;
        send(state, { op: "speaking", speaking: on });
        setSpeaking(state.userID, on);
      }
    }, 60);
  }

  function cssEscape(s) {
    return window.CSS && CSS.escape ? CSS.escape(s) : String(s).replace(/[^a-zA-Z0-9_-]/g, "\\$&");
  }

  function setSpeaking(userID, on) {
    if (!userID) return;
    if (on) speakers[userID] = true;
    else delete speakers[userID];
    document.querySelectorAll('.voice-member[data-user="' + cssEscape(userID) + '"], .voice-tile[data-user="' + cssEscape(userID) + '"]')
      .forEach(function (el) { el.classList.toggle("speaking", on && !!call); });
  }

  function showSpeakers() {
    document.querySelectorAll(".voice-member, .voice-tile").forEach(function (el) {
      el.classList.toggle("speaking", !!call && !!speakers[el.dataset.user]);
    });
  }

  // hangUp ends the page's connection without telling the chat server,
  // which hears of it from the voice server.
  function hangUp(state) {
    state.closing = true;
    if (call === state) call = null;
    clearInterval(state.vad);
    if (state.audioCtx) state.audioCtx.close().catch(function () {});
    if (state.ws) state.ws.close(1000);
    if (state.pc) state.pc.close();
    if (state.mic) release(state.mic);
    Object.keys(state.remote).forEach(function (id) { state.remote[id].remove(); });
    speakers = {};
    showSpeakers();
    render();
  }

  function leave() {
    if (!call) return;
    hangUp(call);
    post("/voice/leave").catch(function () {});
  }

  function applyMute() {
    var silent = muted || deafened;
    if (call && call.mic) {
      call.mic.raw.getAudioTracks().concat(call.mic.out.getAudioTracks()).forEach(function (t) { t.enabled = !silent; });
    }
    if (call) Object.keys(call.remote).forEach(function (id) { call.remote[id].muted = deafened; });
    document.querySelectorAll("[data-voice-mute]").forEach(function (b) {
      b.setAttribute("aria-pressed", silent ? "true" : "false");
      b.setAttribute("aria-label", silent ? "Unmute" : "Mute");
      b.title = silent ? "Unmute" : "Mute";
    });
    document.querySelectorAll("[data-voice-deaf]").forEach(function (b) {
      b.setAttribute("aria-pressed", deafened ? "true" : "false");
      b.setAttribute("aria-label", deafened ? "Undeafen" : "Deafen");
      b.title = deafened ? "Undeafen" : "Deafen";
    });
  }

  function report() {
    if (call && call.ws) post("/voice/state", { self_mute: muted || deafened ? "1" : "0", self_deaf: deafened ? "1" : "0" }).catch(function () {});
  }

  function toggleMute() {
    // Unmuting while deafened hears again too, as in Discord.
    if (muted || deafened) {
      muted = false;
      deafened = false;
    } else {
      muted = true;
    }
    save("muted", muted);
    save("deafened", deafened);
    applyMute();
    report();
  }

  function toggleDeaf() {
    deafened = !deafened;
    save("deafened", deafened);
    applyMute();
    report();
  }

  // render shows where the page talks: the panel above the user, the
  // sidebar entry and the room's own buttons.
  function render() {
    var panel = document.getElementById("voice-panel");
    if (panel) {
      panel.hidden = !call;
      var status = panel.querySelector(".vp-status");
      var where = panel.querySelector(".vp-where");
      if (status) status.textContent = call && call.connected ? "Voice connected" : "Connecting…";
      if (where) where.textContent = call ? call.name : "";
      panel.classList.toggle("connected", !!call && call.connected);
    }
    document.querySelectorAll(".voice-link").forEach(function (a) {
      a.classList.toggle("connected", !!call && a.dataset.voiceJoin === call.channel);
    });
    var room = document.querySelector("[data-voice-room]");
    if (room) {
      var here = !!call && call.channel === room.dataset.voiceRoom;
      room.querySelectorAll(".voice-room-bar [data-voice-join]").forEach(function (b) { b.hidden = here; });
      room.querySelectorAll(".voice-room-bar [data-voice-leave]").forEach(function (b) { b.hidden = !here; });
    }
    document.body.classList.toggle("in-voice", !!call);
  }

  document.addEventListener("click", function (e) {
    var t = e.target;
    if (!(t instanceof Element)) return;
    var joinEl = t.closest("[data-voice-join]");
    if (joinEl) {
      // A sidebar entry also opens the room; the room's button only joins.
      join(joinEl.dataset.voiceJoin);
      return;
    }
    if (t.closest("[data-voice-leave]")) {
      leave();
      return;
    }
    if (t.closest("[data-voice-mute]")) {
      toggleMute();
      return;
    }
    if (t.closest("[data-voice-deaf]")) toggleDeaf();
  });

  document.addEventListener("change", function (e) {
    var t = e.target;
    if (!(t instanceof HTMLInputElement) || t.name !== "noise" || !t.closest("[data-voice-noise]")) return;
    try {
      window.localStorage.setItem("gochat.voice.noise", t.value);
    } catch (err) { /* private mode: nothing keeps it */ }
    showNoiseSetting(true);
  });

  function isVoiceList(el) {
    return el instanceof Element && (el.id.indexOf("voice-members-") === 0 || el.id.indexOf("voice-tiles-") === 0);
  }

  // Updates may pass each other: a list never goes back to an older one.
  document.body.addEventListener("htmx:oobBeforeSwap", function (e) {
    var target = e.detail.target;
    if (!isVoiceList(target)) return;
    var incoming = e.detail.fragment && e.detail.fragment.firstElementChild;
    if (incoming && Number(incoming.dataset.version) < Number(target.dataset.version)) e.detail.shouldSwap = false;
  });

  document.body.addEventListener("htmx:oobAfterSwap", function (e) {
    if (isVoiceList(e.target)) showSpeakers();
  });

  document.body.addEventListener("htmx:afterSwap", function () {
    render();
    showSpeakers();
  });

  // Leaving the page leaves the voice channel.
  window.addEventListener("beforeunload", function (e) {
    if (call) {
      e.preventDefault();
      e.returnValue = "";
    }
  });

  applyMute();
  render();
  showNoiseSetting(false);
})();
