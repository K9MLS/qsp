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
  var parrotEnabled = document.getElementById("parrot-enabled");
  var parrotTalkgroup = document.getElementById("parrot-talkgroup");
  var parrotTimeslot = document.getElementById("parrot-timeslot");
  var parrotState = document.getElementById("parrot-state");

  var loaded = null;

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

    var parrot = (cfg.dmr && cfg.dmr.parrot) || {};
    parrotEnabled.checked = !!parrot.enabled;
    parrotTalkgroup.value = parrot.talkgroup || "";
    parrotTimeslot.value = String(parrot.timeslot || 2);
    refreshParrotState();
  }

  function collect() {
    var next = JSON.parse(JSON.stringify(loaded));
    if (!next.dmr) { next.dmr = {}; }

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
            restartNote.textContent =
              "Restart QSP for these to take effect: " + restart.join(", ") + ".";
            show(restartNote);
          }
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

  document.getElementById("add-talkgroup").addEventListener("click", function () {
    rows.push({ name: "", dialled: 0, arrives: 0, timeslot: 2 });
    renderTalkgroups();
  });

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
