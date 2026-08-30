/* QSP bridges and schedule.
 *
 * Reads /api/config, edits the bridge list and the schedule, posts the whole
 * document back. The server compares it with what is running and records the
 * difference; both apply within a second, with no restart.
 */
(function () {
  "use strict";

  var DAYS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

  var loading = document.getElementById("loading");
  var signedOut = document.getElementById("signed-out");
  var readOnly = document.getElementById("read-only");
  var readOnlyReason = document.getElementById("read-only-reason");
  var errorBox = document.getElementById("error");
  var errorText = document.getElementById("error-text");
  var errorFields = document.getElementById("error-fields");
  var savedBox = document.getElementById("saved");
  var savedText = document.getElementById("saved-text");
  var form = document.getElementById("form");
  var saveButton = document.getElementById("save");

  var bridgesEl = document.getElementById("bridges");
  var windowsEl = document.getElementById("windows");
  var bridgeCount = document.getElementById("bridge-count");
  var windowCount = document.getElementById("window-count");

  var loaded = null;
  var bridges = [];
  var windows = [];

  function show(el) { if (el) { el.hidden = false; } }
  function hide(el) { if (el) { el.hidden = true; } }

  function escapeText(value) {
    return String(value === null || value === undefined ? "" : value)
      .replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;").replace(/'/g, "&#39;");
  }

  /* scheduled names the bridges the schedule controls.
   *
   * **A bridge named by a window is off outside its windows**, whatever its own
   * enabled flag says. Showing that flag as if it decided anything would be a
   * lie the operator only discovers when their net does not open. */
  function scheduled() {
    var names = {};
    windows.forEach(function (w) {
      if (w.bridge) {
        names[w.bridge] = true;
      }
    });
    return names;
  }

  function renderBridges() {
    var controlled = scheduled();
    var html = "";

    for (var i = 0; i < bridges.length; i++) {
      var b = bridges[i];
      var endpoints = "";
      for (var j = 0; j < b.endpoints.length; j++) {
        var e = b.endpoints[j];
        endpoints +=
          '<div class="tg-row">' +
          '<label class="tg-row__field tg-row__field--small"><span class="field__label">Peer</span>' +
          '<input class="field__input" data-bridge="' + i + '" data-endpoint="' + j +
          '" data-key="peer" type="number" inputmode="numeric" placeholder="any" value="' +
          escapeText(e.peer || "") + '"></label>' +
          '<label class="tg-row__field tg-row__field--small"><span class="field__label">Talkgroup</span>' +
          '<input class="field__input" data-bridge="' + i + '" data-endpoint="' + j +
          '" data-key="talkgroup" type="number" inputmode="numeric" value="' +
          escapeText(e.talkgroup || "") + '"></label>' +
          '<label class="tg-row__field tg-row__field--small"><span class="field__label">Slot</span>' +
          '<select class="field__input" data-bridge="' + i + '" data-endpoint="' + j +
          '" data-key="timeslot">' +
          '<option value="1"' + (e.timeslot === 1 ? " selected" : "") + ">1</option>" +
          '<option value="2"' + (e.timeslot !== 1 ? " selected" : "") + ">2</option>" +
          "</select></label>" +
          '<button class="button button--quiet tg-row__remove" data-remove-endpoint="' +
          i + ":" + j + '" type="button">Remove</button>' +
          "</div>";
      }

      var note = controlled[b.name]
        ? '<p class="acl__hint">The schedule controls this bridge, so it is off ' +
          "outside its windows whatever this setting says.</p>"
        : "";

      html +=
        '<div class="acl">' +
        '<h3 class="acl__title">' +
        '<input class="field__input field__input--inline" data-bridge="' + i +
        '" data-key="name" type="text" value="' + escapeText(b.name) + '" ' +
        'aria-label="Bridge name"></h3>' +
        '<label class="acl__mode"><input type="checkbox" data-bridge="' + i +
        '" data-key="enabled"' + (b.enabled ? " checked" : "") + "> Enabled</label>" +
        note +
        endpoints +
        '<div class="page__actions">' +
        '<button class="button button--quiet" data-add-endpoint="' + i +
        '" type="button">Add an endpoint</button>' +
        '<button class="button button--quiet" data-remove-bridge="' + i +
        '" type="button">Remove this bridge</button>' +
        "</div></div>";
    }

    bridgesEl.innerHTML = html || '<p class="acl__hint">No bridges. Peers on a ' +
      "shared talkgroup already hear each other without one.</p>";
    bridgeCount.textContent = bridges.length +
      (bridges.length === 1 ? " bridge" : " bridges");
    wireBridges();
  }

  function wireBridges() {
    var fields = bridgesEl.querySelectorAll("[data-key]");
    for (var i = 0; i < fields.length; i++) {
      fields[i].addEventListener("change", function (event) {
        var el = event.currentTarget;
        var b = bridges[parseInt(el.getAttribute("data-bridge"), 10)];
        var key = el.getAttribute("data-key");
        var epIndex = el.getAttribute("data-endpoint");

        if (epIndex === null) {
          b[key] = key === "enabled" ? el.checked : el.value;
          if (key === "name") {
            /* Renaming a bridge changes what the schedule controls, so the
             * notes above have to be redrawn. */
            renderBridges();
          }
          return;
        }
        var e = b.endpoints[parseInt(epIndex, 10)];
        e[key] = parseInt(el.value, 10) || 0;
      });
    }

    bind(bridgesEl, "data-add-endpoint", function (v) {
      bridges[parseInt(v, 10)].endpoints.push({ peer: 0, talkgroup: 0, timeslot: 2 });
      renderBridges();
    });
    bind(bridgesEl, "data-remove-bridge", function (v) {
      bridges.splice(parseInt(v, 10), 1);
      renderBridges();
    });
    bind(bridgesEl, "data-remove-endpoint", function (v) {
      var parts = v.split(":");
      bridges[parseInt(parts[0], 10)].endpoints.splice(parseInt(parts[1], 10), 1);
      renderBridges();
    });
  }

  function bind(root, attribute, handler) {
    var els = root.querySelectorAll("[" + attribute + "]");
    for (var i = 0; i < els.length; i++) {
      els[i].addEventListener("click", function (event) {
        handler(event.currentTarget.getAttribute(attribute));
      });
    }
  }

  function renderWindows() {
    var html = "";
    for (var i = 0; i < windows.length; i++) {
      var w = windows[i];

      var days = "";
      for (var d = 0; d < 7; d++) {
        days +=
          '<label class="day"><input type="checkbox" data-window="' + i +
          '" data-day="' + d + '"' + (w.days.indexOf(d) >= 0 ? " checked" : "") +
          "> " + DAYS[d] + "</label>";
      }

      var options = '<option value="">Choose a bridge</option>';
      for (var b = 0; b < bridges.length; b++) {
        options += '<option value="' + escapeText(bridges[b].name) + '"' +
          (bridges[b].name === w.bridge ? " selected" : "") + ">" +
          escapeText(bridges[b].name) + "</option>";
      }

      html +=
        '<div class="acl">' +
        '<label class="acl__mode"><input type="checkbox" data-window="' + i +
        '" data-key="enabled"' + (w.enabled ? " checked" : "") + "> Enabled</label>" +
        '<div class="tg-row">' +
        '<label class="tg-row__field"><span class="field__label">Bridge</span>' +
        '<select class="field__input" data-window="' + i + '" data-key="bridge">' +
        options + "</select></label>" +
        '<label class="tg-row__field tg-row__field--small"><span class="field__label">Starts</span>' +
        '<input class="field__input" data-window="' + i + '" data-key="start" type="time" value="' +
        escapeText(w.start) + '"></label>' +
        '<label class="tg-row__field tg-row__field--small"><span class="field__label">For</span>' +
        '<input class="field__input" data-window="' + i + '" data-key="duration" type="text" ' +
        'placeholder="1h" value="' + escapeText(w.duration) + '"></label>' +
        '<label class="tg-row__field"><span class="field__label">Timezone</span>' +
        '<input class="field__input" data-window="' + i + '" data-key="timezone" type="text" ' +
        'spellcheck="false" placeholder="America/Chicago" value="' +
        escapeText(w.timezone) + '"></label>' +
        "</div>" +
        '<div class="days">' + days + "</div>" +
        '<div class="page__actions">' +
        '<button class="button button--quiet" data-remove-window="' + i +
        '" type="button">Remove this window</button></div>' +
        "</div>";
    }

    windowsEl.innerHTML = html || '<p class="acl__hint">No windows. Bridges are ' +
      "on or off by their own setting.</p>";
    windowCount.textContent = windows.length +
      (windows.length === 1 ? " window" : " windows");
    wireWindows();
  }

  function wireWindows() {
    var fields = windowsEl.querySelectorAll("[data-key]");
    for (var i = 0; i < fields.length; i++) {
      fields[i].addEventListener("change", function (event) {
        var el = event.currentTarget;
        var w = windows[parseInt(el.getAttribute("data-window"), 10)];
        var key = el.getAttribute("data-key");
        w[key] = key === "enabled" ? el.checked : el.value;
        if (key === "bridge") {
          /* Which bridges the schedule controls has changed. */
          renderBridges();
        }
      });
    }

    var days = windowsEl.querySelectorAll("[data-day]");
    for (var j = 0; j < days.length; j++) {
      days[j].addEventListener("change", function (event) {
        var el = event.currentTarget;
        var w = windows[parseInt(el.getAttribute("data-window"), 10)];
        var day = parseInt(el.getAttribute("data-day"), 10);
        var at = w.days.indexOf(day);
        if (el.checked && at < 0) {
          w.days.push(day);
          w.days.sort(function (a, b) { return a - b; });
        } else if (!el.checked && at >= 0) {
          w.days.splice(at, 1);
        }
      });
    }

    bind(windowsEl, "data-remove-window", function (v) {
      windows.splice(parseInt(v, 10), 1);
      renderWindows();
      renderBridges();
    });
  }

  function render(cfg) {
    var dmr = cfg.dmr || {};

    bridges = (dmr.bridges || []).map(function (b) {
      return {
        name: b.name || "",
        enabled: !!b.enabled,
        endpoints: (b.endpoints || []).map(function (e) {
          return {
            peer: e.peer || 0,
            talkgroup: e.talkgroup || 0,
            timeslot: e.timeslot || 2
          };
        })
      };
    });

    windows = (dmr.schedule || []).map(function (w) {
      return {
        bridge: w.bridge || "",
        days: (w.days || []).slice(),
        start: w.start || "",
        duration: w.duration || "",
        timezone: w.timezone || "",
        enabled: !!w.enabled
      };
    });

    renderBridges();
    renderWindows();
  }

  function collect() {
    var next = JSON.parse(JSON.stringify(loaded));
    if (!next.dmr) { next.dmr = {}; }

    next.dmr.bridges = bridges.map(function (b) {
      return {
        name: b.name,
        enabled: b.enabled,
        endpoints: b.endpoints.map(function (e) {
          var out = { talkgroup: e.talkgroup, timeslot: e.timeslot || 2 };
          /* A zero peer means every peer carrying the talkgroup, which the
           * model expresses by the field being absent rather than zero. */
          if (e.peer) {
            out.peer = e.peer;
          }
          return out;
        })
      };
    });

    next.dmr.schedule = windows.map(function (w) {
      return {
        bridge: w.bridge,
        days: w.days,
        start: w.start,
        duration: w.duration,
        timezone: w.timezone,
        enabled: w.enabled
      };
    });
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
    errorFields.innerHTML = "";

    var next = collect();
    saveButton.disabled = true;

    fetch("/api/config", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      credentials: "same-origin",
      body: JSON.stringify({ config: next, summary: "bridges and schedule" })
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
              " applied, saved as version " + res.body.version + ".";
          show(savedBox);
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

  document.getElementById("add-bridge").addEventListener("click", function () {
    bridges.push({
      name: "",
      enabled: false,
      /* Two endpoints, because a bridge with fewer is refused by validation and
       * an operator should not have to discover that by pressing save. */
      endpoints: [
        { peer: 0, talkgroup: 0, timeslot: 2 },
        { peer: 0, talkgroup: 0, timeslot: 2 }
      ]
    });
    renderBridges();
  });

  document.getElementById("add-window").addEventListener("click", function () {
    windows.push({
      bridge: bridges.length ? bridges[0].name : "",
      days: [],
      start: "20:00",
      duration: "1h",
      timezone: "",
      enabled: true
    });
    renderWindows();
    renderBridges();
  });

  saveButton.addEventListener("click", save);
  document.getElementById("revert").addEventListener("click", function () {
    hide(errorBox);
    hide(savedBox);
    if (loaded) { render(loaded); }
  });

  load();
})();
