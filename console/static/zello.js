/* QSP console: set up Zello.
 *
 * **Written for somebody who has just installed QSP**, so the page is in the
 * order the work is done, and the checklist at the top reads the server's own
 * health report rather than trusting that a save means something works.
 *
 * Three things are written, by three different routes, and the page says which
 * is which:
 *
 *  - the credentials, each stored the moment its button is pressed, through
 *    /api/secrets. They are never read back: the list says only whether each
 *    is stored and when it changed.
 *  - the settings, saved together with Save changes, through /api/config,
 *    where they are versioned like every other setting.
 *  - the connector's own file, which this page generates and cannot write,
 *    because it lives on the machine rather than in QSP.
 *
 * The transcoder and its bridge are both named "zello". A page that managed
 * several would ask a new user a question they cannot answer yet.
 */
(function () {
  "use strict";

  var NAME = "zello";
  var CREDENTIALS = ["zello-username", "zello-password", "zello-private-key"];
  var DEFAULT_SOCKET = "/run/qsp/zello.sock";
  var DEFAULT_USRP_LISTEN = "127.0.0.1:32001";
  var DEFAULT_USRP_PEER = "127.0.0.1:32002";
  var DEFAULT_AMBE = "127.0.0.1:2460";

  function el(id) { return document.getElementById(id); }

  var loading = document.getElementById("loading");
  var signedOut = document.getElementById("signed-out");
  var readOnly = document.getElementById("read-only");
  var readOnlyReason = document.getElementById("read-only-reason");
  var errorBox = document.getElementById("error");
  var errorText = document.getElementById("error-text");
  var errorFields = document.getElementById("error-fields");
  var savedBox = document.getElementById("saved");
  var savedText = document.getElementById("saved-text");
  var restartNote = document.getElementById("restart-note");
  var form = document.getElementById("form");
  var saveButton = document.getElementById("save");

  var checklist = document.getElementById("checklist");
  var checklistState = document.getElementById("checklist-state");
  var forwardingOff = document.getElementById("forwarding-off");
  var enabled = document.getElementById("zello-enabled");
  var enabledState = document.getElementById("zello-enabled-state");
  var switchState = document.getElementById("switch-state");
  var channel = document.getElementById("zello-channel");
  var issuer = document.getElementById("zello-issuer");
  var ambe = document.getElementById("zello-ambe");
  var radioID = document.getElementById("zello-radio-id");
  var talkgroup = document.getElementById("zello-talkgroup");
  var timeslot = document.getElementById("zello-timeslot");
  var permitAll = document.getElementById("zello-permit-all");
  var permitAllState = document.getElementById("zello-permit-all-state");
  var permit = document.getElementById("zello-permit");
  var peersNow = document.getElementById("zello-peers-now");
  var carriesState = document.getElementById("carries-state");
  var usrpListen = document.getElementById("zello-usrp-listen");
  var usrpPeer = document.getElementById("zello-usrp-peer");
  var socket = document.getElementById("zello-socket");
  var connectorConfig = document.getElementById("zello-connector-config");

  /* Written out rather than assembled from the name, so the console's element
   * gate — which reads getElementById literals — checks every one of them. */
  var credentialFields = {
    "zello-username": {
      input: document.getElementById("cred-zello-username"),
      state: document.getElementById("cred-zello-username-state"),
      store: document.getElementById("cred-zello-username-store"),
      remove: document.getElementById("cred-zello-username-remove")
    },
    "zello-password": {
      input: document.getElementById("cred-zello-password"),
      state: document.getElementById("cred-zello-password-state"),
      store: document.getElementById("cred-zello-password-store"),
      remove: document.getElementById("cred-zello-password-remove")
    },
    "zello-private-key": {
      input: document.getElementById("cred-zello-private-key"),
      state: document.getElementById("cred-zello-private-key-state"),
      store: document.getElementById("cred-zello-private-key-store"),
      remove: document.getElementById("cred-zello-private-key-remove")
    }
  };

  var loaded = null;

  function show(node) { if (node) { node.hidden = false; } }
  function hide(node) { if (node) { node.hidden = true; } }

  function scrollIntoView(node) {
    if (node && node.scrollIntoView) {
      node.scrollIntoView({ block: "nearest", behavior: "smooth" });
    }
  }

  function same(a, b) {
    return String(a || "").trim().toLowerCase() === String(b || "").trim().toLowerCase();
  }

  function findTranscoder(cfg) {
    var list = (cfg.dmr && cfg.dmr.transcoders) || [];
    for (var i = 0; i < list.length; i++) {
      if (same(list[i].name, NAME)) { return list[i]; }
    }
    return null;
  }

  function findBridge(cfg) {
    var list = (cfg.dmr && cfg.dmr.bridges) || [];
    for (var i = 0; i < list.length; i++) {
      if (same(list[i].name, NAME)) { return list[i]; }
    }
    return null;
  }

  function parseIDs(text) {
    return String(text || "").split(",")
      .map(function (p) { return parseInt(p.trim(), 10); })
      .filter(function (p) { return !isNaN(p) && p > 0; });
  }

  /* ---- Settings ---- */

  function render(cfg) {
    var z = cfg.zello || {};
    var t = findTranscoder(cfg) || {};
    var b = findBridge(cfg);

    enabled.checked = !!z.enabled;
    channel.value = z.channel || "";
    issuer.value = z.issuer || "";
    socket.value = z.logon_socket || DEFAULT_SOCKET;

    ambe.value = t.address || DEFAULT_AMBE;
    radioID.value = t.radio_id ? String(t.radio_id) : "";
    usrpListen.value = t.usrp_listen || DEFAULT_USRP_LISTEN;
    usrpPeer.value = t.usrp_peer || DEFAULT_USRP_PEER;
    permitAll.checked = !!t.permit_all_peers;
    permit.value = (t.permit_peers || []).join(", ");

    /* The talkgroup and timeslot are read from the transcoder's own endpoint
     * in the bridge, which is what routing reads. */
    talkgroup.value = "";
    timeslot.value = "2";
    if (b) {
      (b.endpoints || []).forEach(function (e) {
        if (same(e.transcoder, NAME)) {
          talkgroup.value = e.talkgroup ? String(e.talkgroup) : "";
          timeslot.value = String(e.timeslot === 1 ? 1 : 2);
        }
      });
    }

    if (cfg.dmr && cfg.dmr.forwarding === false) { show(forwardingOff); } else { hide(forwardingOff); }
    refreshStates();
  }

  function refreshStates() {
    enabledState.textContent = enabled.checked ? "On" : "Off";
    switchState.textContent = enabled.checked ? "On" : "Off";
    permitAllState.textContent = permitAll.checked ? "On" : "Off";
    permit.disabled = permitAll.checked;

    var ids = parseIDs(permit.value);
    carriesState.textContent = permitAll.checked
      ? "every repeater"
      : ids.length === 0 ? "no repeater yet" : ids.length + (ids.length === 1 ? " repeater" : " repeaters");

    connectorConfig.textContent = JSON.stringify({
      logon_socket: socket.value.trim() || DEFAULT_SOCKET,
      /* Crossed: each side listens where the other sends. Generated rather
       * than explained, because this is the setting most easily swapped. */
      usrp_listen: usrpPeer.value.trim() || DEFAULT_USRP_PEER,
      usrp_peer: usrpListen.value.trim() || DEFAULT_USRP_LISTEN,
      health_listen: "127.0.0.1:18090"
    }, null, 2);
  }

  /* What the page can say before the server does. The server validates too;
   * these are the three a new user most often leaves out, said in the words
   * of this page rather than as configuration field names. */
  function pageProblems() {
    var problems = [];
    if (!enabled.checked) { return problems; }
    if (!channel.value.trim()) { problems.push("Step 1: give the Zello channel."); }
    if (!issuer.value.trim()) { problems.push("Step 1: give the issuer from the developer portal."); }
    if (!(parseInt(radioID.value, 10) > 0)) { problems.push("Step 2: give the gateway its DMR ID."); }
    if (!(parseInt(talkgroup.value, 10) > 0)) { problems.push("Step 3: choose the talkgroup Zello carries."); }
    if (!permitAll.checked && parseIDs(permit.value).length === 0) {
      problems.push("Step 3: list at least one repeater whose owner has agreed, or say every one has.");
    }
    return problems;
  }

  function collect() {
    var next = JSON.parse(JSON.stringify(loaded));
    next.dmr = next.dmr || {};
    next.dmr.transcoders = next.dmr.transcoders || [];
    next.dmr.bridges = next.dmr.bridges || [];

    var on = enabled.checked;
    var tg = parseInt(talkgroup.value, 10) || 0;
    var ts = parseInt(timeslot.value, 10) === 1 ? 1 : 2;
    var ids = parseIDs(permit.value);

    next.zello = {
      logon_socket: socket.value.trim() || DEFAULT_SOCKET,
      issuer: issuer.value.trim(),
      audience: (loaded.zello && loaded.zello.audience) || "",
      channel: channel.value.trim()
    };
    next.zello.enabled = on;

    var t = findTranscoder(next);
    if (!t) {
      t = { name: NAME };
      next.dmr.transcoders.push(t);
    }
    t.enabled = on;
    t.address = ambe.value.trim();
    t.radio_id = parseInt(radioID.value, 10) || 0;
    t.usrp_listen = usrpListen.value.trim();
    t.usrp_peer = usrpPeer.value.trim();
    t.permit_all_peers = permitAll.checked;
    t.permit_peers = permitAll.checked ? [] : ids;

    /* **One endpoint per agreed repeater, not one for every peer.** Routing
     * withholds transcoded audio from an every-peer endpoint unless every
     * peer has agreed, so a bridge written that way with a list would carry
     * nothing and say so only in a log. */
    var endpoints = [];
    if (permitAll.checked) {
      endpoints.push({ peer: 0, talkgroup: tg, timeslot: ts });
    } else {
      ids.forEach(function (id) { endpoints.push({ peer: id, talkgroup: tg, timeslot: ts }); });
    }
    endpoints.push({ transcoder: NAME, talkgroup: tg, timeslot: ts });

    var b = findBridge(next);
    if (!b) {
      b = { name: NAME };
      next.dmr.bridges.push(b);
    }
    b.enabled = on;
    b.endpoints = endpoints;
    return next;
  }

  function load() {
    fetch("/api/config", { headers: { Accept: "application/json" }, credentials: "same-origin" })
      .then(function (r) {
        if (r.status === 401) {
          hide(loading);
          show(signedOut);
          return null;
        }
        return r.json();
      })
      .then(function (body) {
        if (!body) { return; }
        hide(loading);
        loaded = body.config;
        render(loaded);
        show(form);
        if (!body.writable) {
          readOnlyReason.textContent = body.read_only_reason || "";
          show(readOnly);
          saveButton.disabled = true;
        }
        loadCredentials();
        loadDongle();
        loadPeers();
        loadChecklist();
      })
      .catch(function () {
        hide(loading);
        errorText.textContent = "Cannot reach this instance.";
        show(errorBox);
      });
  }

  function save() {
    hide(errorBox);
    hide(savedBox);
    hide(restartNote);
    errorFields.innerHTML = "";

    var problems = pageProblems();
    if (problems.length) {
      errorText.textContent = "Some steps are not finished.";
      problems.forEach(function (p) {
        var li = document.createElement("li");
        li.textContent = p;
        errorFields.appendChild(li);
      });
      show(errorBox);
      scrollIntoView(errorBox);
      return;
    }

    var next = collect();
    saveButton.disabled = true;
    fetch("/api/config", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      credentials: "same-origin",
      body: JSON.stringify({ config: next, summary: "zello" })
    })
      .then(function (r) {
        return r.json().then(function (body) { return { status: r.status, body: body }; });
      })
      .then(function (res) {
        saveButton.disabled = false;
        if (res.status === 200) {
          loaded = next;
          var changes = (res.body.changes || []).length;
          savedText.textContent = changes === 0
            ? "Nothing had changed, so nothing was recorded."
            : changes + (changes === 1 ? " change" : " changes") +
              " saved as version " + res.body.version + ".";
          var restart = res.body.needs_restart || [];
          if (restart.length) {
            restartNote.textContent =
              "Restart QSP for these to take effect: " + restart.join(", ") +
              ". On a service install that is \u0022sudo systemctl restart qsp\u0022; " +
              "in a container, recreate it.";
            show(restartNote);
          }
          show(savedBox);
          scrollIntoView(savedBox);
          loadChecklist();
          return;
        }
        if (res.status === 400 && res.body.fields) {
          errorText.textContent = res.body.error || "";
          res.body.fields.forEach(function (f) {
            var li = document.createElement("li");
            li.textContent = f.field + ": " + f.problem + (f.fix ? " — " + f.fix : "");
            errorFields.appendChild(li);
          });
          show(errorBox);
          scrollIntoView(errorBox);
          return;
        }
        errorText.textContent = (res.body && res.body.error) || "That did not save.";
        show(errorBox);
      })
      .catch(function () {
        saveButton.disabled = false;
        errorText.textContent = "Cannot reach this instance.";
        show(errorBox);
      });
  }

  /* ---- Credentials ---- */

  function loadCredentials() {
    fetch("/api/secrets", { headers: { Accept: "application/json" }, credentials: "same-origin" })
      .then(function (r) {
        return r.json().then(function (body) { return { status: r.status, body: body }; });
      })
      .then(function (res) {
        var stored = {};
        ((res.body && res.body.secrets) || []).forEach(function (s) { stored[s.name] = s; });
        CREDENTIALS.forEach(function (name) {
          var state = credentialFields[name].state;
          if (res.status !== 200) {
            /* Usually an instance with no database, which has nowhere to
             * keep a credential; the server's own sentence says so. */
            state.textContent = (res.body && res.body.error) || "Credentials cannot be read here.";
            return;
          }
          var s = stored[name];
          state.textContent = s
            ? "Stored, changed " + new Date(s.updated_at).toLocaleString() +
              (s.updated_by ? " by " + s.updated_by : "") + "."
            : "Not stored yet.";
        });
      })
      .catch(function () { /* the checklist says the instance is unreachable */ });
  }

  function storeCredential(name) {
    var input = credentialFields[name].input;
    var state = credentialFields[name].state;
    var value = input.value;
    if (name !== "zello-private-key") { value = value.trim(); }
    if (!value) {
      state.textContent = "Nothing to store: the box is empty.";
      return;
    }
    state.textContent = "Storing…";
    fetch("/api/secrets/" + encodeURIComponent(name), {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      credentials: "same-origin",
      body: JSON.stringify({ value: value })
    })
      .then(function (r) {
        return r.json().then(function (body) { return { status: r.status, body: body }; });
      })
      .then(function (res) {
        if (res.status === 200) {
          /* **Cleared at once.** A password left in a box is on the screen
           * for whoever walks past, which is the reason nothing reads it back. */
          input.value = "";
          loadCredentials();
          loadChecklist();
          return;
        }
        state.textContent = (res.body && res.body.error) || "That was not stored.";
      })
      .catch(function () { state.textContent = "Cannot reach this instance."; });
  }

  function removeCredential(name) {
    var state = credentialFields[name].state;
    if (!window.confirm("Remove the stored " + name + "? Zello will not connect until it is entered again.")) {
      return;
    }
    fetch("/api/secrets/" + encodeURIComponent(name), { method: "DELETE", credentials: "same-origin" })
      .then(function (r) {
        if (r.status === 204) {
          loadCredentials();
          loadChecklist();
          return;
        }
        state.textContent = "That was not removed.";
      })
      .catch(function () { state.textContent = "Cannot reach this instance."; });
  }

  /* ---- The dongle ---- */

  var donglePanel = document.getElementById("dongle-panel");
  var dongleState = document.getElementById("dongle-state");
  var dongleService = document.getElementById("dongle-service");
  var dongleAdapter = document.getElementById("dongle-adapter");
  var dongleProblems = document.getElementById("dongle-problems");
  var dongleResult = document.getElementById("dongle-result");
  /* Written out per verb, not assembled, so every URL is one the console's
   * endpoint gate can read. */
  var dongleActions = {
    start: { button: document.getElementById("dongle-start"), url: "/api/dongle/start" },
    restart: { button: document.getElementById("dongle-restart"), url: "/api/dongle/restart" },
    stop: { button: document.getElementById("dongle-stop"), url: "/api/dongle/stop" }
  };

  function renderDongle(st) {
    show(donglePanel);
    if (!st.managed) {
      dongleState.textContent = "outside QSP";
      dongleService.textContent = "AMBEserver is not managed by systemd on this install, so it is started and stopped outside QSP.";
    } else if (!st.installed) {
      dongleState.textContent = "not installed";
      dongleService.textContent = "AMBEserver is not installed as a service.";
    } else {
      dongleState.textContent = st.active === "active" ? "running" : st.active;
      dongleService.textContent = "Service: " + st.active + " (" + st.sub + ")" + (st.since ? ", since " + st.since : "") + ".";
    }
    dongleAdapter.textContent = (st.adapters || []).length === 0
      ? "No USB-serial adapter is present."
      : "Adapter: " + (st.adapters || []).map(function (a) {
        return a.name + (a.driver ? " (" + a.driver + ")" : "") +
          (a.latency_ms >= 0 ? ", latency timer " + a.latency_ms + " ms" : "");
      }).join("; ") + ".";
    dongleProblems.innerHTML = "";
    (st.problems || []).forEach(function (p) {
      var li = document.createElement("li");
      li.textContent = p;
      dongleProblems.appendChild(li);
    });
    var controllable = st.managed && st.installed;
    Object.keys(dongleActions).forEach(function (verb) {
      dongleActions[verb].button.disabled = !controllable;
    });
  }

  function loadDongle() {
    fetch("/api/dongle", { headers: { Accept: "application/json" }, credentials: "same-origin" })
      .then(function (r) {
        return r.json().then(function (body) { return { status: r.status, body: body }; });
      })
      .then(function (res) {
        if (res.status === 200) { renderDongle(res.body); }
        /* 503 is an instance with no transcoder: no panel at all. */
      })
      .catch(function () { /* the checklist says the instance is unreachable */ });
  }

  function controlDongle(verb) {
    var warn = verb === "restart"
      ? "Restart AMBEserver? A call in progress is dropped; the next one sets the dongle up again."
      : verb === "stop" ? "Stop AMBEserver? Zello is off the air until it is started again."
      : "Start AMBEserver?";
    if (!window.confirm(warn)) { return; }
    dongleResult.textContent = "Asking systemd…";
    fetch(dongleActions[verb].url, { method: "POST", credentials: "same-origin" })
      .then(function (r) {
        return r.json().then(function (body) { return { status: r.status, body: body }; });
      })
      .then(function (res) {
        if (res.status === 200) {
          dongleResult.textContent = "Done: " + verb + ".";
          renderDongle(res.body);
          loadChecklist();
          return;
        }
        dongleResult.textContent = (res.body && res.body.error) || "That did not work.";
      })
      .catch(function () { dongleResult.textContent = "Cannot reach this instance."; });
  }

  Object.keys(dongleActions).forEach(function (verb) {
    dongleActions[verb].button.addEventListener("click", function () { controlDongle(verb); });
  });

  /* ---- Repeaters connected now ---- */

  function loadPeers() {
    fetch("/api/peers", { headers: { Accept: "application/json" } })
      .then(function (r) { return r.json(); })
      .then(function (body) {
        /* Motorola repeaters included and marked: they receive Zello audio
         * when they are listed, as Homebrew peers do. */
        var peers = (body && body.peers) || [];
        peersNow.textContent = peers.length === 0
          ? "No hotspot or repeater is connected right now."
          : "Connected now: " + peers.map(function (p) {
            return p.id + (p.callsign ? " " + p.callsign : "") +
              (p.protocol === "ipsc" ? " (Motorola)" : "");
          }).join(", ") + ".";
      })
      .catch(function () { peersNow.textContent = ""; });
  }

  /* ---- Checklist ---- */

  function loadChecklist() {
    fetch("/healthz", { headers: { Accept: "application/json" } })
      .then(function (r) { return r.json(); })
      .then(function (report) {
        var byName = {};
        ((report && report.results) || []).forEach(function (res) { byName[res.name] = res; });
        renderChecklist(byName);
      })
      .catch(function () {
        checklist.innerHTML = "";
        var li = document.createElement("li");
        li.textContent = "Cannot read this server's health report.";
        checklist.appendChild(li);
      });
  }

  /* Each item names the check it reads, and when that check is absent says
   * why in terms of this page: a check QSP never registered is a setting that
   * is off, or saved and not yet restarted — never "unknown". */
  function renderChecklist(byName) {
    var items = [
      { label: "Zello is on, and QSP has restarted since", check: "zello-logon",
        absent: "Not yet: switch Zello on, save, and restart QSP." },
      { label: "The Zello credentials are stored", check: "zello-logon",
        absent: "Waiting for the step above." },
      { label: "QSP can reach the vocoder", check: "transcoder:" + NAME,
        absent: "Waiting for Zello to be on after a restart." },
      { label: "Audio has crossed both ways", check: "transcoder-audio:" + NAME,
        absent: "Waiting for Zello to be on after a restart." }
    ];
    checklist.innerHTML = "";
    var done = 0;
    items.forEach(function (item, i) {
      var res = byName[item.check];
      var status = "unavailable";
      var text = item.absent;
      if (res) {
        status = res.status;
        text = res.summary + (res.fix ? " — " + res.fix : "");
        /* The first item is only whether the logon socket exists at all. */
        if (i === 0) { status = "healthy"; text = "Serving logons."; }
      }
      if (status === "healthy") { done++; }
      var li = document.createElement("li");
      var pill = document.createElement("span");
      pill.className = "status status--" + status;
      pill.textContent = status === "healthy" ? "Done" : status === "unavailable" ? "Not yet" : "Needs attention";
      li.appendChild(pill);
      li.appendChild(document.createTextNode(" " + item.label + ". " + text));
      checklist.appendChild(li);
    });
    checklistState.textContent = done + " of " + items.length;
  }

  /* ---- Copy ---- */

  function copyText(node, button) {
    var text = node.textContent || "";
    function done() {
      var word = button.querySelector(".copyblock__word");
      var was = word ? word.textContent : "";
      button.setAttribute("data-copied", "yes");
      if (word) { word.textContent = "Copied"; }
      setTimeout(function () {
        button.removeAttribute("data-copied");
        if (word) { word.textContent = was; }
      }, 1500);
    }
    if (navigator.clipboard && window.isSecureContext) {
      navigator.clipboard.writeText(text).then(done, function () { select(node); });
      return;
    }
    select(node);
    try { if (document.execCommand("copy")) { done(); } } catch (e) { /* selected, at least */ }
  }

  function select(node) {
    var range = document.createRange();
    range.selectNodeContents(node);
    var sel = window.getSelection();
    sel.removeAllRanges();
    sel.addRange(range);
  }

  Array.prototype.forEach.call(document.querySelectorAll("[data-copy]"), function (button) {
    button.addEventListener("click", function () {
      var node = el(button.getAttribute("data-copy"));
      if (node) { copyText(node, button); }
    });
  });

  /* ---- Wiring ---- */

  CREDENTIALS.forEach(function (name) {
    credentialFields[name].store.addEventListener("click", function () { storeCredential(name); });
    credentialFields[name].remove.addEventListener("click", function () { removeCredential(name); });
  });
  [enabled, permitAll].forEach(function (n) { n.addEventListener("change", refreshStates); });
  [permit, usrpListen, usrpPeer, socket].forEach(function (n) { n.addEventListener("input", refreshStates); });
  document.getElementById("recheck").addEventListener("click", loadChecklist);
  saveButton.addEventListener("click", save);
  document.getElementById("revert").addEventListener("click", function () {
    hide(errorBox);
    hide(savedBox);
    if (loaded) { render(loaded); }
  });

  load();
})();
