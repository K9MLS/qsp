/* QSP network settings.
 *
 * Reads /api/config, edits the join details and parrot, posts the whole
 * document back. Same terms as the access page: the server compares it with
 * what is running and records the difference, so a page sending fragments would
 * have to reason about what it had not touched.
 *
 * **Only what was edited is written into that document** (since 0.1.330).
 * Each control is remembered as it was drawn, and one still as it was drawn
 * leaves its setting exactly as the server sent it.
 *
 * Until then every setting on the page was written from its control on every
 * save, and a control cannot always show what is stored. A timeout the
 * dropdown does not offer was drawn as the dropdown's first choice and saved
 * as that; a server that had never had a parrot was given one with two
 * defaults and told to restart; turning nothing on supplied port numbers.
 * Saving the page to change one thing changed several nobody had touched
 * (found 2026-10-07, E4).
 *
 * **What cannot be read is refused, by name, and nothing is sent.** A
 * latitude typed as 41,88 was saved as 0, and a repeater ID with a stray
 * letter in the list was dropped from it, each with the answer "Saved" (E5).
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
  var bar = window.QSPSaveBar(document.getElementById("save-status"));

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
  var dmrEnabled = document.getElementById("dmr-enabled");
  var dmrEnabledState = document.getElementById("dmr-enabled-state");
  var dmrForwarding = document.getElementById("dmr-forwarding");
  var dmrForwardingState = document.getElementById("dmr-forwarding-state");
  var dmrForwardingOff = document.getElementById("dmr-forwarding-off");
  var dmrState = document.getElementById("dmr-state");
  var p25Enabled = document.getElementById("p25-enabled");
  var p25Listen = document.getElementById("p25-listen");
  var p25Callsign = document.getElementById("p25-callsign");
  var p25Allowed = document.getElementById("p25-allowed");
  var p25EnabledState = document.getElementById("p25-enabled-state");
  var p25State = document.getElementById("p25-state");
  var p25AllowedState = document.getElementById("p25-allowed-state");
  var repeatersEnabled = document.getElementById("p25_repeaters-enabled");
  var repeatersListen = document.getElementById("p25_repeaters-listen");
  var repeatersRecord = document.getElementById("p25_repeaters-record");
  var repeatersSite = document.getElementById("p25_repeaters-site");
  var repeatersPresent = document.getElementById("p25_repeaters-present");
  var repeatersHeader = document.getElementById("p25_repeaters-header");
  var repeatersHold = document.getElementById("p25_repeaters-hold");
  var repeatersHeaderState = document.getElementById("p25_repeaters-header-state");
  var repeatersAllowed = document.getElementById("p25_repeaters-allowed");
  var repeatersEnabledState = document.getElementById("p25_repeaters-enabled-state");
  var repeatersState = document.getElementById("p25_repeaters-state");
  var repeatersAllowedState = document.getElementById("p25_repeaters-allowed-state");
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

  /* Every control whose value is a setting. */
  var CONTROLS = [
    networkName, networkAddress, identityCallsign, identityLocation,
    identityLatitude, identityLongitude, peerPasswords, subEnabled, subTimeout,
    subUnlink, retain, parrotEnabled, parrotTalkgroup, parrotTimeslot,
    dmrEnabled, dmrForwarding, p25Enabled, p25Listen, p25Callsign, p25Allowed,
    repeatersEnabled, repeatersListen, repeatersRecord, repeatersSite,
    repeatersPresent, repeatersHeader, repeatersHold, repeatersAllowed,
    ipscEnabled, ipscListen, ipscMaster, ipscCC, ipscTimeout, ipscPeers, ipscSlot2
  ];

  /* What each control, and the talkgroup list, showed when it was last drawn
   * from the server's document. */
  var shown = {};
  var shownRows = "";

  function valueOf(el) { return el.type === "checkbox" ? el.checked : el.value; }

  function rowsNow() {
    return JSON.stringify(rows.map(function (r) {
      return [r.name, r.dialled, r.timeslot];
    }));
  }

  function mark() {
    CONTROLS.forEach(function (el) {
      shown[el.id] = valueOf(el);
      el.removeAttribute("aria-invalid");
    });
    shownRows = rowsNow();
  }

  function touched(el) { return valueOf(el) !== shown[el.id]; }

  function changed() {
    return !!loaded && (CONTROLS.some(touched) || rowsNow() !== shownRows);
  }

  function copy(value) { return JSON.parse(JSON.stringify(value)); }

  function show(el) { if (el) { el.hidden = false; } }

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

  function repeatersRouters() {
    return repeatersAllowed.value.split("\n")
      .map(function (r) { return r.trim(); })
      .filter(function (r) { return r !== ""; });
  }

  function refreshRepeatersState() {
    repeatersEnabledState.textContent = repeatersEnabled.checked ? "On" : "Off";
    repeatersHeaderState.textContent = repeatersHeader.checked ? "On" : "Off";
    var routers = repeatersRouters();
    if (!repeatersEnabled.checked) {
      repeatersState.textContent = "off";
      repeatersAllowedState.textContent = "";
      return;
    }
    repeatersState.textContent = routers.length
      ? "on, " + routers.length + (routers.length === 1 ? " router" : " routers")
      : "on, any router";
    repeatersAllowedState.textContent = routers.length
      ? (routers.length === 1 ? "Only the router listed is accepted."
          : "Only the " + routers.length + " routers listed are accepted.") +
        " Everything else is refused and counted."
      : "Every router that can reach the port is accepted.";
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
      ? (calls.length === 1 ? "Only the gateway listed is answered."
          : "Only the " + calls.length + " gateways listed are answered.") +
        " Everything else is ignored and counted."
      : "Every gateway that knows the address is answered. On an address the " +
        "internet can reach, name the gateways instead.";
  }

  function refreshDMRState() {
    dmrEnabledState.textContent = dmrEnabled.checked ? "On" : "Off";
    dmrForwardingState.textContent = dmrForwarding.checked ? "On" : "Off";
    /* Warned only while stations can log in: with the listener off too there
     * is nobody to hear anybody, and the count already says off. */
    if (dmrEnabled.checked && !dmrForwarding.checked) { show(dmrForwardingOff); } else { hide(dmrForwardingOff); }
    dmrState.textContent = !dmrEnabled.checked ? "off"
      : dmrForwarding.checked ? "on, forwarding" : "on, not forwarding";
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
        raw: copy(t),
        name: t.name || "",
        dialled: t.dialled || 0,
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

    dmrEnabled.checked = !!(cfg.dmr && cfg.dmr.enabled);
    dmrForwarding.checked = !!(cfg.dmr && cfg.dmr.forwarding);
    refreshDMRState();

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

    var repeaters = cfg.p25_repeaters || {};
    repeatersEnabled.checked = !!repeaters.enabled;
    repeatersListen.value = repeaters.listen_address || "";
    repeatersRecord.value = repeaters.record_dir || "";
    repeatersSite.value = repeaters.site ? String(repeaters.site) : "";
    repeatersPresent.value = repeaters.present_as === "console" ? "console" : "";
    repeatersHeader.checked = !!repeaters.send_header;
    /* Absent and 0 are different: absent is the default, 0 is no hold. */
    repeatersHold.value = repeaters.hold_ms == null ? "" : String(repeaters.hold_ms);
    repeatersAllowed.value = (repeaters.allowed_routers || []).join("\n");
    refreshRepeatersState();

    var parrot = (cfg.dmr && cfg.dmr.parrot) || {};
    parrotEnabled.checked = !!parrot.enabled;
    parrotTalkgroup.value = parrot.talkgroup || "";
    parrotTimeslot.value = String(parrot.timeslot || 2);
    refreshParrotState();

    mark();
  }

  /* setIfOffered selects a stored value, adding it to the list when the
   * list does not offer it.
   *
   * **A value set in the file is shown as what it is.** It used to be left
   * on the list's first choice, so the page showed fifteen minutes for a
   * timeout of twenty, and then saved fifteen. */
  function setIfOffered(select, value) {
    if (!select || !value) { return; }
    for (var i = 0; i < select.options.length; i++) {
      if (select.options[i].value === value) {
        select.value = value;
        return;
      }
    }
    var option = document.createElement("option");
    option.value = value;
    option.textContent = value + " (as set in the configuration)";
    select.appendChild(option);
    select.value = value;
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

  /* What could not be read on the last attempt to save: the control and a
   * sentence about it. */
  var problems = [];

  function bad(el, message) {
    problems.push({ el: el, message: message });
    return null;
  }

  /* whole reads a whole number of at least min, or reports why it cannot.
   * Blank is zero, which for every box here means "none" or "the default".
   *
   * **parseInt is not used to read what a person typed.** It reads "12abc"
   * as 12 and "abc" as nothing, and `|| 0` then turns nothing into zero:
   * both are a number the operator did not enter, saved without a word. */
  function whole(el, label, min, max) {
    var raw = el.value.trim();
    if (raw === "") { return 0; }
    if (!/^\d+$/.test(raw)) {
      return bad(el, label + ": “" + raw + "” is not a whole number.");
    }
    var n = Number(raw);
    if (n < min || (max !== undefined && n > max)) {
      return bad(el, label + ": " + raw + " is not between " + min + " and " + max + ".");
    }
    return n;
  }

  /* A coordinate. Blank clears it. Zero is a real place in the Gulf of
   * Guinea, so what cannot be read is refused and never becomes zero. */
  function coordinate(el, label, limit) {
    var raw = el.value.trim();
    if (raw === "") { return 0; }
    if (raw.indexOf(",") >= 0) {
      return bad(el, label + ": “" + raw + "” has a comma in it. " +
        "Write the decimal with a full stop, like " + raw.replace(",", ".") + ".");
    }
    var n = Number(raw);
    if (!isFinite(n)) {
      return bad(el, label + ": “" + raw + "” is not a number. Decimal degrees, like 41.88.");
    }
    if (Math.abs(n) > limit) {
      return bad(el, label + ": " + raw + " is not between -" + limit + " and " + limit + ".");
    }
    return n;
  }

  /* A list of whole numbers separated by commas, spaces or new lines. Every
   * entry has to be one, or the list is refused: an entry dropped from an
   * allow list is a repeater turned away. */
  function numbers(el, label) {
    var out = [];
    var entries = el.value.split(/[\s,]+/).filter(function (e) { return e !== ""; });
    for (var i = 0; i < entries.length; i++) {
      if (!/^\d+$/.test(entries[i]) || Number(entries[i]) === 0) {
        return bad(el, label + ": “" + entries[i] + "” is not an ID. " +
          "Nothing was saved, so no repeater has been dropped from the list.");
      }
      out.push(Number(entries[i]));
    }
    return out;
  }

  function lines(el) {
    return el.value.split("\n")
      .map(function (c) { return c.trim(); })
      .filter(function (c) { return c !== ""; });
  }

  /* collect is the document to save: the one loaded, with each setting whose
   * control has been edited replaced by what the control now says. It fills
   * `problems` with whatever could not be read. */
  function collect() {
    var next = copy(loaded);
    problems = [];

    function part(parent, key) {
      if (!parent[key]) { parent[key] = {}; }
      return parent[key];
    }
    function dmr() { return part(next, "dmr"); }
    /* put writes one setting, when its control was edited and could be read. */
    function put(el, section, key, value) {
      if (!touched(el)) { return; }
      var v = typeof value === "function" ? value() : value;
      if (v !== null) { section()[key] = v; }
    }
    function text(el) { return el.value.trim(); }
    function upper(el) { return el.value.trim().toUpperCase(); }

    /* Written when blanked, so clearing the field returns the network to one
     * shared password rather than leaving the old directory in place. */
    put(peerPasswords, dmr, "peer_passwords", text(peerPasswords));

    function subscription() { return part(dmr(), "subscription"); }
    put(subEnabled, subscription, "enabled", subEnabled.checked);
    put(subTimeout, subscription, "timeout", subTimeout.value);
    /* Blank means the network offers no disconnect talkgroup, which is a real
     * choice: PNWDigital does not use 4000 at all. */
    put(subUnlink, subscription, "unlink", function () {
      return whole(subUnlink, "Disconnect talkgroup", 0, 16777215);
    });

    put(retain, function () { return part(dmr(), "calls"); }, "retain", retain.value);

    function identity() { return part(dmr(), "identity"); }
    put(identityCallsign, identity, "callsign", upper(identityCallsign));
    put(identityLocation, identity, "location", text(identityLocation));
    put(identityLatitude, identity, "latitude", function () {
      return coordinate(identityLatitude, "Latitude", 90);
    });
    put(identityLongitude, identity, "longitude", function () {
      return coordinate(identityLongitude, "Longitude", 180);
    });

    function join() { return part(dmr(), "join"); }
    put(networkName, join, "network_name", text(networkName));
    put(networkAddress, join, "address", text(networkAddress));
    if (rowsNow() !== shownRows) {
      var kept = [];
      rows.forEach(function (r) {
        if (!r.name.trim() && !r.dialled) { return; } /* a row added and left empty */
        if (!(r.dialled > 0)) {
          bad(talkgroups.querySelector('[data-key="dialled"]') || talkgroups,
            "Talkgroup “" + r.name + "” has no number. Give it one or remove the row.");
          return;
        }
        /* Over what the server sent, so anything else a talkgroup carries
         * goes back with it. That includes `arrives`, which this page has
         * never offered: a talkgroup number is the same on both sides of a
         * hotspot, and a box that changes one invites a rewrite nobody
         * afterwards remembers writing. */
        var out = r.raw ? copy(r.raw) : {};
        out.name = r.name;
        out.dialled = r.dialled;
        out.timeslot = r.timeslot || 2;
        kept.push(out);
      });
      join().talkgroups = kept;
    }

    /* **A port is supplied only as something is turned on here**, so that
     * an operator turning it on does not have to know one. Supplied on
     * every save, it was a change to a server where nothing was turned on. */
    function turnedOn(el) { return touched(el) && el.checked; }

    function p25() { return part(next, "p25"); }
    put(p25Enabled, p25, "enabled", p25Enabled.checked);
    put(p25Listen, p25, "listen_address", text(p25Listen));
    put(p25Callsign, p25, "callsign", upper(p25Callsign));
    put(p25Allowed, p25, "allowed_callsigns", function () {
      var calls = lines(p25Allowed).map(function (c) { return c.toUpperCase(); });
      for (var i = 0; i < calls.length; i++) {
        if (/\s/.test(calls[i])) {
          return bad(p25Allowed, "P25 gateways allowed: “" + calls[i] +
            "” is more than one word. One callsign on each line.");
        }
      }
      return calls;
    });
    if (turnedOn(p25Enabled) && !p25().listen_address) {
      p25().listen_address = "0.0.0.0:41000";
    }

    function repeaters() { return part(next, "p25_repeaters"); }
    put(repeatersEnabled, repeaters, "enabled", repeatersEnabled.checked);
    put(repeatersListen, repeaters, "listen_address", text(repeatersListen));
    put(repeatersRecord, repeaters, "record_dir", text(repeatersRecord));
    put(repeatersSite, repeaters, "site", function () {
      return whole(repeatersSite, "Site number", 0, 127);
    });
    put(repeatersPresent, repeaters, "present_as", repeatersPresent.value);
    put(repeatersHeader, repeaters, "send_header", repeatersHeader.checked);
    if (touched(repeatersHold)) {
      /* Left out when empty, so the server's default applies. Absent and 0
       * are different: 0 is no hold at all. */
      if (repeatersHold.value.trim() === "") {
        delete repeaters().hold_ms;
      } else {
        var hold = whole(repeatersHold, "Hold before transmitting", 0, 200);
        if (hold !== null) { repeaters().hold_ms = hold; }
      }
    }
    put(repeatersAllowed, repeaters, "allowed_routers", repeatersRouters());
    if (turnedOn(repeatersEnabled) && !repeaters().listen_address) {
      repeaters().listen_address = "0.0.0.0:1994";
    }

    put(dmrEnabled, dmr, "enabled", dmrEnabled.checked);
    put(dmrForwarding, dmr, "forwarding", dmrForwarding.checked);
    if (turnedOn(dmrEnabled) && !dmr().listen_address) {
      dmr().listen_address = "0.0.0.0:62031";
    }

    function ipsc() { return part(next, "ipsc"); }
    put(ipscEnabled, ipsc, "enabled", ipscEnabled.checked);
    put(ipscListen, ipsc, "listen_address", text(ipscListen));
    put(ipscMaster, ipsc, "master_id", function () {
      return whole(ipscMaster, "Motorola master ID", 0, 16777215);
    });
    put(ipscTimeout, ipsc, "peer_timeout_seconds", function () {
      return whole(ipscTimeout, "Motorola repeater timeout", 0, 86400);
    });
    put(ipscSlot2, ipsc, "slot_bit_is_timeslot2", ipscSlot2.checked);
    if (touched(ipscCC)) {
      /* A pointer in the document: blank is "not set", and 0 is a colour
       * code. */
      if (ipscCC.value.trim() === "") {
        ipsc().colour_code = null;
      } else {
        var cc = whole(ipscCC, "Colour code", 0, 15);
        if (cc !== null) { ipsc().colour_code = cc; }
      }
    }
    put(ipscPeers, ipsc, "allowed_peers", function () {
      return numbers(ipscPeers, "Motorola repeaters allowed");
    });
    if (turnedOn(ipscEnabled)) {
      if (!ipsc().listen_address) { ipsc().listen_address = "0.0.0.0:50000"; }
      if (!ipsc().peer_timeout_seconds) { ipsc().peer_timeout_seconds = 90; }
    }

    function parrot() { return part(dmr(), "parrot"); }
    put(parrotEnabled, parrot, "enabled", parrotEnabled.checked);
    put(parrotTalkgroup, parrot, "talkgroup", function () {
      return whole(parrotTalkgroup, "Parrot talkgroup", 0, 16777215);
    });
    put(parrotTimeslot, parrot, "timeslot", function () {
      return Number(parrotTimeslot.value) === 1 ? 1 : 2;
    });
    /* The two durations no box here sets, supplied as parrot is turned on
     * so that it can be without anybody knowing they are required. */
    if (turnedOn(parrotEnabled)) {
      if (!parrot().max_duration || parrot().max_duration === "0s") { parrot().max_duration = "30s"; }
      if (!parrot().gap || parrot().gap === "0s") { parrot().gap = "1s"; }
    }
    return next;
  }

  /* refuse shows what could not be read and sends nothing. */
  function refuse() {
    errorText.textContent = problems.length === 1
      ? "One box could not be read, so nothing was saved."
      : problems.length + " boxes could not be read, so nothing was saved.";
    problems.forEach(function (p) {
      var li = document.createElement("li");
      li.textContent = p.message;
      errorFields.appendChild(li);
      if (p.el && p.el.setAttribute) { p.el.setAttribute("aria-invalid", "true"); }
    });
    show(errorBox);
    bar.failed("Not saved: " + problems[0].message);
    /* To the box itself and not to the notice: it is the thing to mend, and
     * the bar has already said what is wrong with it. */
    var first = problems[0].el;
    if (first && first.focus) {
      if (first.scrollIntoView) { first.scrollIntoView({ block: "center" }); }
      first.focus({ preventScroll: true });
    }
  }

  function load() {
    fetch("/api/config", { headers: { Accept: "application/json" }, credentials: "same-origin" })
      .then(function (r) {
        if (r.status === 401) {
          hide(loading);
          show(signedOut);
          return null;
        }
        /* An answer that is not the configuration is an error to show, not
         * an empty page to edit and save over what is there. */
        return r.json().catch(function () { return {}; }).then(function (body) {
          if (!r.ok || !body.config) {
            throw new Error(body.error || "This server answered " + r.status +
              " and not its configuration.");
          }
          return body;
        });
      })
      .then(function (body) {
        if (!body) { return; }
        hide(loading);
        loaded = body.config;
        render(loaded);
        show(form);
        bar.watch(form, changed);

        if (!body.writable) {
          readOnlyReason.textContent = body.read_only_reason || "";
          show(readOnly);
          saveButton.disabled = true;
        }
      })
      .catch(function (e) {
        hide(loading);
        errorText.textContent = (e && e.message && e.name === "Error")
          ? e.message : "Cannot reach this instance.";
        show(errorBox);
      });
  }

  function save() {
    hide(errorBox);
    hide(savedBox);
    hide(restartNote);
    errorFields.innerHTML = "";
    CONTROLS.forEach(function (el) { el.removeAttribute("aria-invalid"); });

    var next = collect();
    if (problems.length) {
      refuse();
      return;
    }
    saveButton.disabled = true;
    bar.saving();

    fetch("/api/config", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      credentials: "same-origin",
      /* base is what this page was given, so the server saves only what
         was changed here and keeps what was changed anywhere else. */
      body: JSON.stringify({ config: next, base: loaded, summary: "network settings" })
    })
      .then(function (r) {
        return r.json().catch(function () { return {}; })
          .then(function (body) { return { status: r.status, body: body }; });
      })
      .then(function (res) {
        saveButton.disabled = false;

        if (res.status === 200) {
          loaded = next;
          /* Redrawn from what was saved, so every control is again "as
           * drawn" and the next save starts from here. */
          render(loaded);
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
          /* The bar says it where the button is. The notice is brought into
           * view only when it has more to say than the bar has room for. */
          bar.saved(savedText.textContent +
            (restart.length ? " A restart is needed; see the top of the page." : ""));
          if (restart.length) { bar.reveal(savedBox); }
          return;
        }

        bar.failed("Not saved. The reason is shown above.");

        if (res.status === 400 && res.body.fields) {
          errorText.textContent = res.body.error || "";
          res.body.fields.forEach(function (f) {
            var li = document.createElement("li");
            li.textContent = f.field + ": " + f.problem + (f.fix ? " — " + f.fix : "");
            errorFields.appendChild(li);
          });
          show(errorBox);
          bar.reveal(errorBox);
          return;
        }

        errorText.textContent = (res.body && res.body.error) ||
          "That did not save: the server answered " + res.status + ".";
        show(errorBox);
        bar.reveal(errorBox);
      })
      .catch(function () {
        saveButton.disabled = false;
        errorText.textContent = "Cannot reach this instance. Nothing was saved.";
        show(errorBox);
        bar.failed("Not saved. The reason is shown above.");
        bar.reveal(errorBox);
      });
  }

  document.getElementById("add-talkgroup").addEventListener("click", function () {
    rows.push({ name: "", dialled: 0, timeslot: 2 });
    renderTalkgroups();
  });

  identityCallsign.addEventListener("input", refreshIdentityState);
  peerPasswords.addEventListener("input", refreshPeerPasswordState);
  subEnabled.addEventListener("change", refreshSubState);
  subTimeout.addEventListener("change", refreshSubState);
  retain.addEventListener("change", refreshRetainState);
  ipscEnabled.addEventListener("change", refreshIPSCState);
  /* Turning either off takes the network off the air for everyone, so it is
   * confirmed; turning on is not. */
  dmrEnabled.addEventListener("change", function () {
    if (!dmrEnabled.checked && !window.confirm("Stop accepting hotspots and repeaters? Every station is disconnected once you save and restart.")) {
      dmrEnabled.checked = true;
    }
    refreshDMRState();
  });
  dmrForwarding.addEventListener("change", function () {
    if (!dmrForwarding.checked && !window.confirm("Turn forwarding off? Stations stay logged in but nobody hears anybody once you save and restart.")) {
      dmrForwarding.checked = true;
    }
    refreshDMRState();
  });
  ipscSlot2.addEventListener("change", refreshIPSCState);
  ipscPeers.addEventListener("input", refreshIPSCState);
  p25Enabled.addEventListener("change", refreshP25State);
  p25Allowed.addEventListener("input", refreshP25State);
  repeatersEnabled.addEventListener("change", refreshRepeatersState);
  repeatersHeader.addEventListener("change", refreshRepeatersState);
  repeatersAllowed.addEventListener("input", refreshRepeatersState);
  parrotEnabled.addEventListener("change", refreshParrotState);
  parrotTalkgroup.addEventListener("input", refreshParrotState);

  saveButton.addEventListener("click", save);
  document.getElementById("revert").addEventListener("click", function () {
    hide(errorBox);
    hide(savedBox);
    if (loaded) { render(loaded); }
    bar.clean();
  });

  load();
})();
