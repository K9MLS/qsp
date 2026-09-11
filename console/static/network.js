/* QSP network settings.
 *
 * Reads /api/config, edits the join details and parrot, posts the whole
 * document back. Same terms as the access page: the server compares it with
 * what is running and records the difference, so a page sending fragments would
 * have to reason about what it had not touched.
 */
(function () {
  "use strict";

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

  var networkName = document.getElementById("network-name");
  var networkAddress = document.getElementById("network-address");
  var talkgroups = document.getElementById("talkgroups");
  var tgCount = document.getElementById("tg-count");
  var identityCallsign = document.getElementById("identity-callsign");
  var identityLocation = document.getElementById("identity-location");
  var identityLatitude = document.getElementById("identity-latitude");
  var identityLongitude = document.getElementById("identity-longitude");
  var identityState = document.getElementById("identity-state");

  var peerPasswords = document.getElementById("peer-passwords");
  var peerPasswordsState = document.getElementById("peerpw-state");

  var subEnabled = document.getElementById("subscription-enabled");
  var subTimeout = document.getElementById("subscription-timeout");
  var subUnlink = document.getElementById("subscription-unlink");
  var subState = document.getElementById("subscription-state");
  var retain = document.getElementById("retain");
  var retainState = document.getElementById("retain-state");

  var parrotEnabled = document.getElementById("parrot-enabled");
  var parrotTalkgroup = document.getElementById("parrot-talkgroup");
  var parrotTimeslot = document.getElementById("parrot-timeslot");
  var parrotState = document.getElementById("parrot-state");
  var p25Enabled = document.getElementById("p25-enabled");
  var p25Listen = document.getElementById("p25-listen");
  var p25Callsign = document.getElementById("p25-callsign");
  var p25Allowed = document.getElementById("p25-allowed");
  var p25EnabledState = document.getElementById("p25-enabled-state");
  var p25State = document.getElementById("p25-state");
  var p25AllowedState = document.getElementById("p25-allowed-state");
  var ipscEnabled = document.getElementById("ipsc-enabled");
  var ipscListen = document.getElementById("ipsc-listen");
  var ipscMaster = document.getElementById("ipsc-master");
  var ipscCC = document.getElementById("ipsc-cc");
  var ipscTimeout = document.getElementById("ipsc-timeout");
  var ipscPeers = document.getElementById("ipsc-peers");
  var ipscSlot2 = document.getElementById("ipsc-slot2");
  var ipscState = document.getElementById("ipsc-state");
  var ipscEnabledState = document.getElementById("ipsc-enabled-state");
  var ipscSlot2State = document.getElementById("ipsc-slot2-state");

  var loaded = null;

  function show(el) { if (el) { el.hidden = false; } }

  /* **A confirmation you cannot see is not one.** The saved notice sits at the
   * top of this page and the Save button at the bottom of it, so pressing Save
   * changed nothing in view: the version number, the change count and the
   * restart instruction all rendered several screens above, which is
   * indistinguishable from a button that does nothing.
   *
   * Honours prefers-reduced-motion, and is guarded because scrollIntoView with
   * options is absent in older browsers — a missing scroll is better than a
   * broken save handler. */
  function scrollIntoView(el) {
    if (!el || !el.scrollIntoView) { return; }
    var still = window.matchMedia &&
      window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    try {
      el.scrollIntoView({ behavior: still ? "auto" : "smooth", block: "center" });
    } catch (e) {
      el.scrollIntoView();
    }
  }
  function hide(el) { if (el) { el.hidden = true; } }

  function escapeText(value) {
    return String(value === null || value === undefined ? "" : value)
      .replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;").replace(/'/g, "&#39;");
  }

  /* renderTalkgroups draws one row per talkgroup, plus its own remove button.
   *
   * Rebuilt from an array rather than edited in place, so adding and removing
   * cannot leave the indices in the DOM disagreeing with the ones in the
   * configuration — which is the usual way a form like this deletes the wrong
   * row. */
  var rows = [];

  function renderTalkgroups() {
    var html = "";
    for (var i = 0; i < rows.length; i++) {
      var r = rows[i];
      html +=
        '<div class="tg-row">' +
        '<label class="tg-row__field"><span class="field__label">Name</span>' +
        '<input class="field__input" data-index="' + i + '" data-key="name" type="text" value="' +
        escapeText(r.name) + '"></label>' +
        '<label class="tg-row__field tg-row__field--small"><span class="field__label">Dialled</span>' +
        '<input class="field__input" data-index="' + i + '" data-key="dialled" type="number" ' +
        'inputmode="numeric" value="' + escapeText(r.dialled || "") + '"></label>' +
        '<label class="tg-row__field tg-row__field--small"><span class="field__label">Slot</span>' +
        '<select class="field__input" data-index="' + i + '" data-key="timeslot">' +
        '<option value="1"' + (r.timeslot === 1 ? " selected" : "") + ">1</option>" +
        '<option value="2"' + (r.timeslot !== 1 ? " selected" : "") + ">2</option>" +
        "</select></label>" +
        '<button class="button button--quiet tg-row__remove" data-remove="' + i +
        '" type="button">Remove</button>' +
        "</div>";
    }
    talkgroups.innerHTML = html;
    tgCount.textContent = rows.length + (rows.length === 1 ? " talkgroup" : " talkgroups");

    var inputs = talkgroups.querySelectorAll("[data-key]");
    for (var j = 0; j < inputs.length; j++) {
      inputs[j].addEventListener("change", function (event) {
        var el = event.currentTarget;
        var row = rows[parseInt(el.getAttribute("data-index"), 10)];
        var key = el.getAttribute("data-key");
        row[key] = key === "name" ? el.value : parseInt(el.value, 10) || 0;
      });
    }

    var removals = talkgroups.querySelectorAll("[data-remove]");
    for (var k = 0; k < removals.length; k++) {
      removals[k].addEventListener("click", function (event) {
        rows.splice(parseInt(event.currentTarget.getAttribute("data-remove"), 10), 1);
        renderTalkgroups();
      });
    }
  }

  /* **The panel is shown whether or not IPSC is on**, so the summary has to
   * distinguish three states rather than two: off, on with no repeaters
   * permitted, and on with a list. Hiding the section when disabled is what
   * left an operator with no way to enable it at all — this was editable only
   * by hand in qsp.json. */
  /* **"on, any gateway" is the case that catches people.** An empty allow
   * list answers everything that knows the address, which is the opposite of
   * what an empty list usually means — the same trap the IPSC panel warns
   * about, and the same wording, so an operator reading both is not asked to
   * hold two meanings for one idea. */
  function refreshP25State() {
    p25EnabledState.textContent = p25Enabled.checked ? "On" : "Off";
    var calls = p25Allowed.value.split("\n").filter(function (c) {
      return c.trim() !== "";
    });
    if (!p25Enabled.checked) {
      p25State.textContent = "off";
      p25AllowedState.textContent = "";
      return;
    }
    p25State.textContent = calls.length
      ? "on, " + calls.length + (calls.length === 1 ? " gateway" : " gateways")
      : "on, any gateway";
    p25AllowedState.textContent = calls.length
      ? "Only the " + calls.length + " gateway" + (calls.length === 1 ? "" : "s") +
        " listed are answered. Everything else is ignored and counted."
      : "Every gateway that knows the address is answered. On an address the " +
        "internet can reach, name the gateways instead.";
  }

  function refreshIPSCState() {
    ipscEnabledState.textContent = ipscEnabled.checked ? "On" : "Off";
    ipscSlot2State.textContent = ipscSlot2.checked ? "Yes" : "No";
    if (!ipscEnabled.checked) {
      ipscState.textContent = "off";
      return;
    }
    var peers = ipscPeers.value.split(",").filter(function (p) {
      return p.trim() !== "";
    });
    ipscState.textContent = peers.length
      ? "on, " + peers.length + (peers.length === 1 ? " repeater" : " repeaters")
      : "on, any repeater";
  }

  function refreshParrotState() {
    parrotState.textContent = parrotEnabled.checked
      ? "on, talkgroup " + (parrotTalkgroup.value || "?")
      : "off";
  }

  function render(cfg) {
    var join = (cfg.dmr && cfg.dmr.join) || {};
    networkName.value = join.network_name || "";
    networkAddress.value = join.address || "";

    rows = [];
    (join.talkgroups || []).forEach(function (t) {
      rows.push({
        name: t.name || "",
        dialled: t.dialled || 0,
        arrives: t.arrives || 0,
        timeslot: t.timeslot || 2
      });
    });
    renderTalkgroups();

    var identity = (cfg.dmr && cfg.dmr.identity) || {};
    identityCallsign.value = identity.callsign || "";
    identityLocation.value = identity.location || "";
    /* Blank rather than 0 for an absent coordinate. Zero is a real place in the
     * Gulf of Guinea, which is why a hotspot announcing 0,0 is refused a pin —
     * and a form that shows 0 for "not set" invites somebody to save it. */
    identityLatitude.value = identity.latitude ? String(identity.latitude) : "";
    identityLongitude.value = identity.longitude ? String(identity.longitude) : "";
    refreshIdentityState();

    peerPasswords.value = (cfg.dmr && cfg.dmr.peer_passwords) || "";
    refreshPeerPasswordState();

    var sub = (cfg.dmr && cfg.dmr.subscription) || {};
    subEnabled.checked = !!sub.enabled;
    /* Only select a stored value the list actually offers. Forcing an unlisted
     * one would silently rewrite a duration an administrator chose in the file
     * — a form that quietly changes what it was shown is worse than one that
     * cannot express it. */
    setIfOffered(subTimeout, sub.timeout);
    subUnlink.value = sub.unlink ? String(sub.unlink) : "";
    refreshSubState();

    var calls = (cfg.dmr && cfg.dmr.calls) || {};
    setIfOffered(retain, calls.retain);
    refreshRetainState();

    var ipsc = cfg.ipsc || {};
    ipscEnabled.checked = !!ipsc.enabled;
    ipscListen.value = ipsc.listen_address || "";
    ipscMaster.value = ipsc.master_id || "";
    /* colour_code is a pointer in the document: absent and zero are different
     * things, and 0 is a valid colour code. */
    ipscCC.value = (ipsc.colour_code === null || ipsc.colour_code === undefined)
      ? "" : String(ipsc.colour_code);
    ipscTimeout.value = ipsc.peer_timeout_seconds || "";
    ipscPeers.value = (ipsc.allowed_peers || []).join(", ");
    ipscSlot2.checked = !!ipsc.slot_bit_is_timeslot2;
    refreshIPSCState();

    var p25 = cfg.p25 || {};
    p25Enabled.checked = !!p25.enabled;
    p25Listen.value = p25.listen_address || "";
    p25Callsign.value = p25.callsign || "";
    /* One per line rather than comma separated, because a callsign list is
     * read down a column and a long comma-separated line is not. */
    p25Allowed.value = (p25.allowed_callsigns || []).join("\n");
    refreshP25State();

    var parrot = (cfg.dmr && cfg.dmr.parrot) || {};
    parrotEnabled.checked = !!parrot.enabled;
    parrotTalkgroup.value = parrot.talkgroup || "";
    parrotTimeslot.value = String(parrot.timeslot || 2);
    refreshParrotState();
  }

  function setIfOffered(select, value) {
    if (!select || !value) { return; }
    for (var i = 0; i < select.options.length; i++) {
      if (select.options[i].value === value) {
        select.value = value;
        return;
      }
    }
  }

  function refreshPeerPasswordState() {
    peerPasswordsState.textContent = peerPasswords.value.trim()
      ? "members may have their own"
      : "one shared password";
  }

  function refreshSubState() {
    subState.textContent = subEnabled.checked
      ? "on, lapses after " + subTimeout.value
      : "off, everyone hears everything";
  }

  function refreshRetainState() {
    retainState.textContent = retain.value === "0s"
      ? "nothing is kept"
      : "kept " + retain.options[retain.selectedIndex].text.toLowerCase();
  }

  function refreshIdentityState() {
    identityState.textContent = identityCallsign.value.trim()
      ? identityCallsign.value.trim().toUpperCase()
      : "no callsign";
  }

  /* A coordinate is written only when it parses. An unparseable one is left out
   * rather than saved as zero, because zero is a real place and being plotted
   * in the Gulf of Guinea is worse than not being plotted. */
  function coordinate(el) {
    var raw = el.value.trim();
    if (raw === "") { return 0; }
    var n = Number(raw);
    return isFinite(n) ? n : 0;
  }

  function collect() {
    var next = JSON.parse(JSON.stringify(loaded));
    if (!next.dmr) { next.dmr = {}; }

    /* Written even when blank, so clearing the field actually returns the
     * network to one shared password rather than leaving the old directory in
     * place. */
    next.dmr.peer_passwords = peerPasswords.value.trim();

    next.dmr.subscription = next.dmr.subscription || {};
    next.dmr.subscription.enabled = subEnabled.checked;
    next.dmr.subscription.timeout = subTimeout.value;
    /* Blank means the network offers no disconnect talkgroup, which is a real
     * choice: PNWDigital does not use 4000 at all. */
    next.dmr.subscription.unlink = parseInt(subUnlink.value, 10) || 0;

    next.dmr.calls = next.dmr.calls || {};
    next.dmr.calls.retain = retain.value;

    next.dmr.identity = next.dmr.identity || {};
    next.dmr.identity.callsign = identityCallsign.value.trim().toUpperCase();
    next.dmr.identity.location = identityLocation.value.trim();
    next.dmr.identity.latitude = coordinate(identityLatitude);
    next.dmr.identity.longitude = coordinate(identityLongitude);

    next.dmr.join = next.dmr.join || {};
    next.dmr.join.network_name = networkName.value.trim();
    next.dmr.join.address = networkAddress.value.trim();
    next.dmr.join.talkgroups = rows
      .filter(function (r) { return r.dialled > 0; })
      .map(function (r) {
        var out = { name: r.name, dialled: r.dialled, timeslot: r.timeslot || 2 };
        /* **Arrives is never written.** A talkgroup number is the same on both
         * sides of a hotspot: 2 is 2 and 11 is 11. Offering a field that
         * changes one invites a rewrite nobody afterwards remembers writing,
         * and the symptom is a member transmitting into silence with every log
         * healthy. Existing values are preserved by the model and no longer
         * created here.
         *
         * Kept for the shape of the old comment:
         * absent as "the hotspot does not rewrite it" and storing the same
         * number twice would say something different. */
        if (r.arrives && r.arrives !== r.dialled) {
          out.arrives = r.arrives;
        }
        return out;
      });

    next.p25 = next.p25 || {};
    next.p25.enabled = p25Enabled.checked;
    next.p25.listen_address = p25Listen.value.trim();
    next.p25.callsign = p25Callsign.value.trim().toUpperCase();
    next.p25.allowed_callsigns = p25Allowed.value.split("\n")
      .map(function (c) { return c.trim().toUpperCase(); })
      .filter(function (c) { return c !== ""; });
    /* Supplied here rather than left empty, which validation refuses. An
     * operator turning this on should not have to know a port number. */
    if (next.p25.enabled && !next.p25.listen_address) {
      next.p25.listen_address = "0.0.0.0:41000";
    }

    next.ipsc = next.ipsc || {};
    next.ipsc.enabled = ipscEnabled.checked;
    next.ipsc.listen_address = ipscListen.value.trim();
    next.ipsc.master_id = parseInt(ipscMaster.value, 10) || 0;
    next.ipsc.peer_timeout_seconds = parseInt(ipscTimeout.value, 10) || 0;
    next.ipsc.slot_bit_is_timeslot2 = ipscSlot2.checked;
    next.ipsc.colour_code = ipscCC.value.trim() === ""
      ? null : parseInt(ipscCC.value, 10);
    next.ipsc.allowed_peers = ipscPeers.value.split(",")
      .map(function (p) { return parseInt(p.trim(), 10); })
      .filter(function (p) { return !isNaN(p) && p > 0; });
    /* Supplied here rather than left at zero, which validation refuses. An
     * operator turning this on should not have to know a default. */
    if (next.ipsc.enabled) {
      if (!next.ipsc.listen_address) { next.ipsc.listen_address = "0.0.0.0:50000"; }
      if (!next.ipsc.peer_timeout_seconds) { next.ipsc.peer_timeout_seconds = 90; }
    }

    next.dmr.parrot = next.dmr.parrot || {};
    next.dmr.parrot.enabled = parrotEnabled.checked;
    next.dmr.parrot.talkgroup = parseInt(parrotTalkgroup.value, 10) || 0;
    next.dmr.parrot.timeslot = parseInt(parrotTimeslot.value, 10) || 2;
    /* Defaults supplied here rather than left at zero, which validation
     * refuses. An operator turning parrot on should not have to know that a
     * duration is required. */
    if (!next.dmr.parrot.max_duration || next.dmr.parrot.max_duration === "0s") {
      next.dmr.parrot.max_duration = "30s";
    }
    if (!next.dmr.parrot.gap || next.dmr.parrot.gap === "0s") {
      next.dmr.parrot.gap = "1s";
    }
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

    var next = collect();
    saveButton.disabled = true;

    fetch("/api/config", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      credentials: "same-origin",
      body: JSON.stringify({ config: next, summary: "network settings" })
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

          /* Named rather than a bare warning. "Restart required" tells an
           * operator to interrupt their network without saying what for, and
           * they will reasonably want to know whether it can wait. */
          var restart = res.body.needs_restart || [];
          if (restart.length) {
            /* **It says how, not only that.** An operator reading "restart
             * required" has to go and find out what the command is, and the
             * two ways QSP is installed need different ones. */
            restartNote.textContent =
              "Restart QSP for these to take effect: " + restart.join(", ") +
              ". On a service install that is \u0022sudo systemctl restart qsp\u0022; " +
              "in a container, recreate it.";
            show(restartNote);
          }
          show(savedBox);
          scrollIntoView(savedBox);
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

  document.getElementById("add-talkgroup").addEventListener("click", function () {
    rows.push({ name: "", dialled: 0, arrives: 0, timeslot: 2 });
    renderTalkgroups();
  });

  identityCallsign.addEventListener("input", refreshIdentityState);
  peerPasswords.addEventListener("input", refreshPeerPasswordState);
  subEnabled.addEventListener("change", refreshSubState);
  subTimeout.addEventListener("change", refreshSubState);
  retain.addEventListener("change", refreshRetainState);
  ipscEnabled.addEventListener("change", refreshIPSCState);
  ipscSlot2.addEventListener("change", refreshIPSCState);
  ipscPeers.addEventListener("input", refreshIPSCState);
  p25Enabled.addEventListener("change", refreshP25State);
  p25Allowed.addEventListener("input", refreshP25State);
  parrotEnabled.addEventListener("change", refreshParrotState);
  parrotTalkgroup.addEventListener("input", refreshParrotState);

  saveButton.addEventListener("click", save);
  document.getElementById("revert").addEventListener("click", function () {
    hide(errorBox);
    hide(savedBox);
    if (loaded) { render(loaded); }
  });

  load();
})();
