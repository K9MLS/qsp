/* QSP console: weather alerts (ADR-0068).
 *
 * **Written for a ham who has never configured anything by hand.** The page is
 * in the order the work is done, every setting says what it is for, and the
 * codes an operator types are checked against the National Weather Service so
 * a typo shows here rather than as quiet weather.
 *
 * Two things are written and read, by two routes:
 *
 *  - the settings, saved together with Save changes through /api/config, where
 *    they are versioned and audited like every other page's;
 *  - what the service is doing, read from /api/weather, and the code check,
 *    posted to /api/weather/zones.
 *
 * This version previews only: nothing on this page puts anything on the air.
 */
(function () {
  "use strict";

  /* The alert types offered as boxes to tick, by the names NWS gives them.
   * Anything else an operator wants goes in "Other alert types", so this is a
   * convenience rather than a limit. */
  var COMMON_EVENTS = [
    "Tornado Warning",
    "Severe Thunderstorm Warning",
    "Flash Flood Warning",
    "Tornado Watch",
    "Severe Thunderstorm Watch",
    "Flash Flood Watch",
    "Flood Warning",
    "Extreme Wind Warning",
    "Winter Storm Warning",
    "Blizzard Warning",
    "Ice Storm Warning",
    "Hurricane Warning",
    "Tropical Storm Warning",
    "Extreme Heat Warning",
    "Red Flag Warning",
    "Dust Storm Warning",
    "Special Weather Statement"
  ];

  var loading = document.getElementById("loading");
  var signedOut = document.getElementById("signed-out");
  var readOnly = document.getElementById("read-only");
  var readOnlyReason = document.getElementById("read-only-reason");
  var unavailable = document.getElementById("unavailable");
  var errorBox = document.getElementById("error");
  var errorText = document.getElementById("error-text");
  var errorFields = document.getElementById("error-fields");
  var savedBox = document.getElementById("saved");
  var savedText = document.getElementById("saved-text");
  var form = document.getElementById("form");
  var saveButton = document.getElementById("save");

  var enabled = document.getElementById("weather-enabled");
  var enabledState = document.getElementById("weather-enabled-state");
  var switchState = document.getElementById("switch-state");
  var zones = document.getElementById("weather-zones");
  var areaState = document.getElementById("area-state");
  var areaAdvice = document.getElementById("weather-area-advice");
  var checkButton = document.getElementById("weather-check");
  var zoneChecks = document.getElementById("weather-zone-checks");
  var eventsBox = document.getElementById("weather-events");
  var eventsOther = document.getElementById("weather-events-other");
  var eventsState = document.getElementById("events-state");
  var talkgroup = document.getElementById("weather-talkgroup");
  var timeslot = document.getElementById("weather-timeslot");
  var sender = document.getElementById("weather-sender");
  var contact = document.getElementById("weather-contact");
  var contactNote = document.getElementById("weather-contact-note");
  var poll = document.getElementById("weather-poll");
  var destination = document.getElementById("weather-destination");
  var activeList = document.getElementById("weather-active");
  var recentList = document.getElementById("weather-recent");
  var seesState = document.getElementById("sees-state");

  var loaded = null;
  var defaultEvents = COMMON_EVENTS.slice(0, 5);

  function show(node) { if (node) { node.hidden = false; } }
  function hide(node) { if (node) { node.hidden = true; } }

  function scrollIntoView(node) {
    if (node && node.scrollIntoView) {
      node.scrollIntoView({ block: "nearest", behavior: "smooth" });
    }
  }

  /* Codes the way people paste them: commas, spaces or new lines, any case. */
  function parseZones(text) {
    var out = [];
    String(text || "").split(/[\s,;]+/).forEach(function (p) {
      var code = p.trim().toUpperCase();
      if (code && out.indexOf(code) < 0) { out.push(code); }
    });
    return out;
  }

  function parseList(text) {
    return String(text || "").split(",")
      .map(function (p) { return p.trim(); })
      .filter(function (p) { return p !== ""; });
  }

  function lower(list) {
    return list.map(function (e) { return String(e).toLowerCase(); });
  }

  /* ---- Settings ---- */

  function renderEvents(chosen) {
    var want = lower(chosen);
    eventsBox.innerHTML = "";
    COMMON_EVENTS.forEach(function (name, i) {
      var label = document.createElement("label");
      label.className = "acl__mode";
      var box = document.createElement("input");
      box.type = "checkbox";
      box.value = name;
      box.id = "weather-event-" + i;
      box.checked = want.indexOf(name.toLowerCase()) >= 0;
      box.addEventListener("change", refreshStates);
      label.appendChild(box);
      label.appendChild(document.createTextNode(" " + name));
      eventsBox.appendChild(label);
    });
    var common = lower(COMMON_EVENTS);
    eventsOther.value = chosen.filter(function (e) {
      return common.indexOf(String(e).toLowerCase()) < 0;
    }).join(", ");
  }

  function chosenEvents() {
    var out = [];
    eventsBox.querySelectorAll("input[type=checkbox]").forEach(function (box) {
      if (box.checked) { out.push(box.value); }
    });
    parseList(eventsOther.value).forEach(function (e) {
      if (lower(out).indexOf(e.toLowerCase()) < 0) { out.push(e); }
    });
    return out;
  }

  function render(cfg) {
    var w = cfg.weather || {};
    var fresh = !w.zones && !w.events && !w.talkgroup;

    enabled.checked = !!w.enabled;
    zones.value = (w.zones || []).join(", ");
    renderEvents(fresh ? defaultEvents : (w.events || []));
    talkgroup.value = w.talkgroup ? String(w.talkgroup) : "2";
    timeslot.value = String(w.timeslot === 1 ? 1 : 2);
    sender.value = w.sender_id ? String(w.sender_id) : "9990";
    contact.value = w.contact || "";

    var lookups = (cfg.dmr && cfg.dmr.callsigns && cfg.dmr.callsigns.contact) || "";
    contact.placeholder = lookups;
    contactNote.textContent = lookups
      ? "Leave it empty to use " + lookups + ", the address radio ID lookups already use."
      : "Required by the National Weather Service.";
    refreshStates();
  }

  function refreshStates() {
    enabledState.textContent = enabled.checked ? "On" : "Off";
    switchState.textContent = enabled.checked ? "On, previewing" : "Off";
    var codes = parseZones(zones.value);
    var n = codes.length;
    areaState.textContent = n === 0 ? "none yet" : n + (n === 1 ? " code" : " codes");
    /* A county code also catches alerts issued by forecast zone, through the
     * counties NWS lists on every alert; a zone code does not catch warnings
     * issued by county. So zones alone are the one arrangement that misses
     * the alerts that matter most, and the page says so before it is saved. */
    var county = codes.some(function (c) { return /^[A-Z]{2}C[0-9]{3}$/.test(c); });
    if (n > 0 && !county) { show(areaAdvice); } else { hide(areaAdvice); }
    var e = chosenEvents().length;
    eventsState.textContent = e === 0 ? "none chosen" : e + " chosen";
  }

  /* What the page can say before the server does, in this page's words. The
   * server validates too, and its refusal is shown as it comes. */
  function pageProblems() {
    var problems = [];
    if (!enabled.checked) { return problems; }
    var codes = parseZones(zones.value);
    if (codes.length === 0) {
      problems.push("Step 1: give at least one county or zone code, such as TXC121.");
    }
    codes.forEach(function (c) {
      if (!/^[A-Z]{2}[CZ][0-9]{3}$/.test(c)) {
        problems.push("Step 1: " + c + " is not a county code (like TXC121) or a zone code (like TXZ103).");
      }
    });
    if (chosenEvents().length === 0) { problems.push("Step 2: tick at least one kind of alert."); }
    var tg = parseInt(talkgroup.value, 10);
    if (!(tg > 0 && tg <= 16777215)) { problems.push("Step 3: choose the talkgroup alerts go to."); }
    var id = parseInt(sender.value, 10);
    if (!(id > 0 && id <= 16777215)) { problems.push("Step 3: choose the ID alerts are sent from."); }
    if (!contact.value.trim() && !contact.placeholder) {
      problems.push("Step 4: give a contact email; the National Weather Service requires one.");
    }
    return problems;
  }

  function collect() {
    var next = JSON.parse(JSON.stringify(loaded));
    next.weather = {
      zones: parseZones(zones.value),
      events: chosenEvents(),
      talkgroup: parseInt(talkgroup.value, 10) || 0,
      timeslot: parseInt(timeslot.value, 10) === 1 ? 1 : 2,
      sender_id: parseInt(sender.value, 10) || 0,
      contact: contact.value.trim()
    };
    next.weather.enabled = enabled.checked;
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
        loaded = body.config;
        return fetch("/api/weather", { headers: { Accept: "application/json" }, credentials: "same-origin" })
          .then(function (r) { return r.json(); })
          .then(function (w) {
            hide(loading);
            if (!w.available) {
              show(unavailable);
              return;
            }
            if (w.default_events && w.default_events.length) { defaultEvents = w.default_events; }
            render(loaded);
            show(form);
            if (!body.writable) {
              readOnlyReason.textContent = body.read_only_reason || "";
              show(readOnly);
              saveButton.disabled = true;
            }
            renderStatus(w.status);
          });
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
      body: JSON.stringify({ config: next, summary: "weather" })
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
              " saved as version " + res.body.version + ". It is in effect now.";
          show(savedBox);
          scrollIntoView(savedBox);
          /* The service polls as soon as it is told of a change; give it a
           * moment to reach NWS before reading what it found. */
          setTimeout(loadStatus, 3000);
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

  /* ---- Checking codes ---- */

  function kindName(kind) {
    return kind === "county" ? "county" : "forecast zone";
  }

  function pillItem(ok, word, text) {
    var li = document.createElement("li");
    var pill = document.createElement("span");
    pill.className = "status status--" + (ok ? "healthy" : "degraded");
    pill.textContent = word;
    li.appendChild(pill);
    li.appendChild(document.createTextNode(" " + text));
    return li;
  }

  function renderChecks(checks) {
    zoneChecks.innerHTML = "";
    (checks || []).forEach(function (c) {
      if (c.ok && c.zone) {
        zoneChecks.appendChild(pillItem(true, "Found",
          c.code + " is " + c.zone.name + (c.zone.state ? ", " + c.zone.state : "") +
          " (" + kindName(c.zone.kind) + ")."));
      } else {
        zoneChecks.appendChild(pillItem(false, "Check this", c.code + ": " + (c.problem || "unknown") + "."));
      }
    });
  }

  function checkCodes() {
    var codes = parseZones(zones.value);
    zoneChecks.innerHTML = "";
    if (codes.length === 0) {
      zoneChecks.appendChild(pillItem(false, "Check this", "Type a county or zone code first, such as TXC121."));
      return;
    }
    checkButton.disabled = true;
    checkButton.textContent = "Checking…";
    fetch("/api/weather/zones", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      credentials: "same-origin",
      body: JSON.stringify({ codes: codes, contact: contact.value.trim() })
    })
      .then(function (r) {
        return r.json().then(function (body) { return { status: r.status, body: body }; });
      })
      .then(function (res) {
        checkButton.disabled = false;
        checkButton.textContent = "Check codes";
        if (res.status !== 200) {
          zoneChecks.appendChild(pillItem(false, "Check this", (res.body && res.body.error) || "The check did not work."));
          return;
        }
        renderChecks(res.body.checks);
      })
      .catch(function () {
        checkButton.disabled = false;
        checkButton.textContent = "Check codes";
        zoneChecks.appendChild(pillItem(false, "Check this", "Cannot reach this instance."));
      });
  }

  /* ---- What QSP sees ---- */

  function when(iso) {
    if (!iso) { return ""; }
    return new Date(iso).toLocaleString();
  }

  function alertItem(a, showDecided) {
    var sending = a.verdict === "send";
    var li = document.createElement("li");
    var pill = document.createElement("span");
    pill.className = "status status--" + (sending ? "healthy" : "unavailable");
    pill.textContent = sending ? "Would send" : "Held";
    li.appendChild(pill);
    var line = " “" + a.text + "” — " + a.event +
      (a.area ? ", " + a.area : "") +
      (a.until ? ", until " + when(a.until) : "") + ".";
    if (!sending && a.reason) { line += " Held: " + a.reason + "."; }
    if (showDecided) { line += " Decided " + when(a.decided_at) + "."; }
    li.appendChild(document.createTextNode(line));
    return li;
  }

  function renderStatus(st) {
    if (!st) { return; }
    if (!st.enabled) {
      poll.textContent = "Off. Nothing is being read from the National Weather Service.";
    } else if (st.last_error) {
      poll.textContent = "Cannot read alerts: " + st.last_error +
        (st.last_success ? ". Last read successfully " + when(st.last_success) + "." : ".");
    } else if (st.last_success) {
      poll.textContent = "Last read from the National Weather Service " + when(st.last_success) +
        ". It is read again every minute.";
    } else {
      poll.textContent = "Waiting for the first read from the National Weather Service.";
    }
    destination.textContent = st.talkgroup
      ? "Would send on talkgroup " + st.talkgroup + ", timeslot " + st.timeslot +
        ", from " + st.sender_id + ". Preview only: nothing is transmitted."
      : "";

    activeList.innerHTML = "";
    (st.active || []).forEach(function (a) { activeList.appendChild(alertItem(a, false)); });
    if (!st.active || st.active.length === 0) {
      var none = document.createElement("li");
      none.textContent = st.enabled && st.last_success
        ? "No alerts for your area right now."
        : "Nothing yet.";
      activeList.appendChild(none);
    }
    recentList.innerHTML = "";
    (st.recent || []).forEach(function (a) { recentList.appendChild(alertItem(a, true)); });
    if (!st.recent || st.recent.length === 0) {
      var nothing = document.createElement("li");
      nothing.textContent = "No alerts have been decided on yet.";
      recentList.appendChild(nothing);
    }
    var count = (st.active || []).length;
    seesState.textContent = count === 0 ? "no alerts" : count + (count === 1 ? " alert" : " alerts");

    /* The service's own check of the saved codes, shown under the field so
     * a code NWS does not know is visible without pressing anything. */
    if (st.zones && st.zones.length &&
        st.zones.map(function (z) { return z.code; }).join(",") === parseZones(zones.value).join(",")) {
      /* Only while the field still holds the saved codes: a check of codes
       * typed and not yet saved must not be replaced by a refresh. */
      renderChecks(st.zones);
    }
  }

  function loadStatus() {
    fetch("/api/weather", { headers: { Accept: "application/json" }, credentials: "same-origin" })
      .then(function (r) { return r.json(); })
      .then(function (w) { if (w && w.available) { renderStatus(w.status); } })
      .catch(function () { /* the next refresh tries again */ });
  }

  enabled.addEventListener("change", refreshStates);
  zones.addEventListener("input", refreshStates);
  eventsOther.addEventListener("input", refreshStates);
  checkButton.addEventListener("click", checkCodes);
  document.getElementById("weather-refresh").addEventListener("click", loadStatus);
  saveButton.addEventListener("click", save);
  document.getElementById("revert").addEventListener("click", function () {
    if (loaded) {
      render(loaded);
      /* The results were for codes that are no longer in the field. */
      zoneChecks.innerHTML = "";
    }
  });

  load();
  setInterval(loadStatus, 60000);
})();
