/* QSP bridges and schedule.
 *
 * Reads /api/config, edits the bridge list and the schedule, posts the whole
 * document back. The server compares it with what is running and records the
 * difference; both apply within a second, with no restart.
 *
 * **What this page does not edit, it sends back as it found it.** Every
 * bridge, endpoint and window keeps the object the server sent (`raw`), and
 * what is posted is that object with this page's own fields laid over it.
 *
 * Until 0.1.329 each was rebuilt from the three or four fields the page
 * knows. An endpoint that is a link to another network, or the Zello
 * channel, is neither a peer, a talkgroup nor a slot, so it came back as
 * "every peer": a bridge to another network was saved as a bridge to
 * nowhere. With the link running the server refused the save for a reason
 * nothing on this page could mend, and with it paused the bridge was quietly
 * rewritten (found 2026-10-07, E1).
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
  var bar = window.QSPSaveBar(document.getElementById("save-status"));

  var bridgesEl = document.getElementById("bridges");
  var windowsEl = document.getElementById("windows");
  var bridgeCount = document.getElementById("bridge-count");
  var windowCount = document.getElementById("window-count");

  var loaded = null;
  var bridges = [];
  var windows = [];

  function show(el) { if (el) { el.hidden = false; } }
  function hide(el) { if (el) { el.hidden = true; } }

  function copy(value) { return JSON.parse(JSON.stringify(value)); }

  /* fixed says what an endpoint is when it is not a peer, or returns null.
   *
   * These are made on other pages and shown here, not edited: this page has
   * no way to say which link, and a box that turned one into "any" is the
   * fault described above. */
  function fixed(e) {
    if (e.raw && e.raw.upstream) {
      return { what: "Link to " + e.raw.upstream, where: "Set up on the Links page." };
    }
    if (e.raw && e.raw.transcoder) {
      return { what: "Audio channel " + e.raw.transcoder, where: "Set up on the Zello page." };
    }
    return null;
  }

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
        var is = fixed(e);
        endpoints +=
          '<div class="tg-row">' +
          (is
            ? '<div class="tg-row__field tg-row__field--fixed">' +
              '<span class="field__label">Goes to</span>' +
              '<span class="tg-row__fixed" data-fixed="' + i + ":" + j + '">' +
              escapeText(is.what) + "</span></div>"
            : '<label class="tg-row__field tg-row__field--small"><span class="field__label">Peer</span>' +
              '<input class="field__input" data-bridge="' + i + '" data-endpoint="' + j +
              '" data-key="peer" type="number" inputmode="numeric" placeholder="any" value="' +
              escapeText(e.peer || "") + '"></label>') +
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
          /* No Remove on one made elsewhere: the page that made it is the
           * one that takes it away, with whatever else it made. */
          (is
            ? '<p class="acl__hint tg-row__note">' + escapeText(is.where) + "</p>"
            : '<button class="button button--quiet tg-row__remove" data-remove-endpoint="' +
              i + ":" + j + '" type="button">Remove</button>') +
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
          var was = b.name;
          b[key] = key === "enabled" ? el.checked : el.value;
          if (key === "name") {
            /* **The schedule names a bridge, so its windows follow the
             * rename.** They were left on the old name: the net's window
             * then opened a bridge that no longer existed, and the renamed
             * one, no longer scheduled, was on all week (E7). Not when
             * another bridge still has the old name; then the windows are
             * that one's. */
            var taken = bridges.some(function (o) { return o !== b && o.name === was; });
            if (was && !taken) {
              windows.forEach(function (w) {
                if (w.bridge === was) { w.bridge = b.name; }
              });
            }
            renderBridges();
            renderWindows();
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
        raw: copy(b),
        name: b.name || "",
        enabled: !!b.enabled,
        endpoints: (b.endpoints || []).map(function (e) {
          return {
            raw: copy(e),
            peer: e.peer || 0,
            talkgroup: e.talkgroup || 0,
            timeslot: e.timeslot || 2
          };
        })
      };
    });

    windows = (dmr.schedule || []).map(function (w) {
      return {
        raw: copy(w),
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

  /* over lays this page's fields on the object the server sent, so that
   * anything else in it goes back untouched and in the same place. */
  function over(raw, fields) {
    var out = raw ? copy(raw) : {};
    for (var k in fields) {
      if (Object.prototype.hasOwnProperty.call(fields, k)) { out[k] = fields[k]; }
    }
    return out;
  }

  function collect() {
    var next = copy(loaded);
    if (!next.dmr) { next.dmr = {}; }

    var outBridges = bridges.map(function (b) {
      return over(b.raw, {
        name: b.name,
        enabled: b.enabled,
        endpoints: b.endpoints.map(function (e) {
          var out = over(e.raw, { talkgroup: e.talkgroup, timeslot: e.timeslot || 2 });
          if (fixed(e)) {
            /* A link or an audio channel: which one, and that it is no
             * peer, are not this page's to change. */
            return out;
          }
          /* A zero peer means every peer carrying the talkgroup. The server
           * writes that as 0 and takes its absence to mean the same, so it
           * goes back the way it came. */
          if (e.peer || (e.raw && "peer" in e.raw)) {
            out.peer = e.peer;
          } else {
            delete out.peer;
          }
          return out;
        })
      });
    });

    var outWindows = windows.map(function (w) {
      return over(w.raw, {
        bridge: w.bridge,
        days: w.days,
        start: w.start,
        duration: w.duration,
        timezone: w.timezone,
        enabled: w.enabled
      });
    });

    /* A server with none says so as null. Sending [] back for it is a
     * difference nobody made. */
    var had = loaded.dmr || {};
    if (outBridges.length || had.bridges) { next.dmr.bridges = outBridges; }
    if (outWindows.length || had.schedule) { next.dmr.schedule = outWindows; }
    return next;
  }

  function changed() {
    return !!loaded && JSON.stringify(collect()) !== JSON.stringify(loaded);
  }

  function load() {
    /* A refusal is an error to show, not an empty page to fill in and
     * save over what is there. See get.js. */
    window.QSPGet("/api/config", "config")
      .then(function (body) {
        if (body === null) {
          hide(loading);
          show(signedOut);
          return;
        }
        window.QSPLoaded();
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
        window.QSPLoadFailed(e);
      });
  }

  function save() {
    hide(errorBox);
    hide(savedBox);
    errorFields.innerHTML = "";

    var next = collect();
    saveButton.disabled = true;
    bar.saving();

    fetch("/api/config", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      credentials: "same-origin",
      /* base is what this page was given, so the server saves only what
         was changed here and keeps what was changed anywhere else. */
      body: JSON.stringify({ config: next, base: loaded, summary: "bridges and schedule" })
    })
      .then(function (r) {
        return r.json().catch(function () { return {}; })
          .then(function (body) { return { status: r.status, body: body }; });
      })
      .then(function (res) {
        saveButton.disabled = false;

        if (res.status === 200) {
          loaded = next;
          /* Redrawn from what was saved, so each row carries the object the
           * server now holds and a second save starts from there. */
          render(loaded);
          var changes = (res.body.changes || []).length;
          savedText.textContent = changes === 0
            ? "Nothing had changed, so nothing was recorded."
            : changes + (changes === 1 ? " change" : " changes") +
              " applied, saved as version " + res.body.version + ".";
          show(savedBox);
          bar.saved(savedText.textContent);
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
    bar.clean();
  });

  load();
})();
