/* QSP access control.
 *
 * Reads /api/config, edits four lists, and posts the whole configuration back.
 * The whole document rather than a patch: the server compares it with what is
 * running and records the difference, so a page that sent fragments would have
 * to reason about what it had not touched. See
 * docs/adr/ADR-0027-configuration-writes.md.
 */
(function () {
  "use strict";

  /* The four lists, in the order they appear. Each names where it lives in the
   * configuration so the form and the document cannot drift apart. */
  var LISTS = [
    {
      id: "acl-ts2",
      label: "Timeslot 2",
      path: ["dmr", "access", "talkgroups", "timeslot_2"],
      noun: "talkgroup",
      hint: "Most club traffic is here. Enter numbers or ranges, one per line: " +
        "9 or 3100-3199."
    },
    {
      id: "acl-ts1",
      label: "Timeslot 1",
      path: ["dmr", "access", "talkgroups", "timeslot_1"],
      noun: "talkgroup",
      hint: "Usually wide-area or linked traffic."
    },
    {
      id: "acl-registration",
      label: "Registration",
      path: ["dmr", "access", "registration"],
      noun: "repeater or hotspot ID",
      hint: "Checked at login, before the password. A hotspot ID is nine digits; " +
        "a repeater's is seven."
    },
    {
      id: "acl-subscribers",
      label: "Subscribers",
      path: ["dmr", "access", "subscribers"],
      noun: "radio ID",
      hint: "Checked on every transmission. A refused radio does not disconnect " +
        "the hotspot carrying it."
    }
  ];

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
  var tgSummary = document.getElementById("tg-summary");
  var saveButton = document.getElementById("save");
  var revertButton = document.getElementById("revert");

  /* The configuration as loaded, kept so Discard can restore it and so a save
   * sends everything this page did not touch back unchanged. */
  var loaded = null;

  function show(el) { if (el) { el.hidden = false; } }
  function hide(el) { if (el) { el.hidden = true; } }

  function escapeText(value) {
    return String(value === null || value === undefined ? "" : value)
      .replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;").replace(/'/g, "&#39;");
  }

  /* at walks a path into the configuration, creating nothing.
   *
   * A missing access block is a real state — it means everything is permitted,
   * which is what an instance that has never been configured looks like — so
   * this answers with a default rather than failing. */
  function at(cfg, path) {
    var node = cfg;
    for (var i = 0; i < path.length; i++) {
      if (!node || typeof node !== "object") {
        return { mode: "deny", ids: [] };
      }
      node = node[path[i]];
    }
    if (!node || typeof node !== "object") {
      return { mode: "deny", ids: [] };
    }
    return { mode: node.mode || "deny", ids: node.ids || [] };
  }

  /* setAt writes a list back, creating the objects along the way. */
  function setAt(cfg, path, value) {
    var node = cfg;
    for (var i = 0; i < path.length - 1; i++) {
      if (!node[path[i]] || typeof node[path[i]] !== "object") {
        node[path[i]] = {};
      }
      node = node[path[i]];
    }
    node[path[path.length - 1]] = value;
  }

  /* describe says in words what a list does.
   *
   * **This is the reason the page exists.** "mode: deny, ids: []" is correct
   * and tells an operator nothing; working out that it permits everything took
   * a conversation. */
  function describe(list, noun) {
    var count = list.ids.length;
    if (list.mode === "permit") {
      if (count === 0) {
        return "Nothing is allowed. A permit list with no entries refuses every " +
          escapeText(noun) + ".";
      }
      return "Only the " + count + " " + escapeText(noun) +
        (count === 1 ? "" : "s") + " listed below are allowed.";
    }
    if (count === 0) {
      return "Everything is allowed. Nothing is blocked.";
    }
    return "Everything is allowed except the " + count + " " + escapeText(noun) +
      (count === 1 ? "" : "s") + " listed below.";
  }

  function renderList(spec, list) {
    var el = document.getElementById(spec.id);
    if (!el) {
      return;
    }
    var permit = list.mode === "permit";

    el.innerHTML =
      '<h3 class="acl__title">' + escapeText(spec.label) + "</h3>" +
      '<p class="acl__state" id="' + spec.id + '-state">' +
        describe(list, spec.noun) + "</p>" +
      '<div class="acl__modes" role="radiogroup" aria-label="' +
        escapeText(spec.label) + ' mode">' +
        modeButton(spec.id, "deny", "Allow everything except", !permit) +
        modeButton(spec.id, "permit", "Allow only", permit) +
      "</div>" +
      '<label class="field__label" for="' + spec.id + '-ids">' +
        "One " + escapeText(spec.noun) + " or range per line</label>" +
      '<textarea class="field__input acl__ids" id="' + spec.id + '-ids" rows="4" ' +
        'spellcheck="false">' + escapeText(list.ids.join("\n")) + "</textarea>" +
      '<p class="acl__hint">' + escapeText(spec.hint) + "</p>";

    var radios = el.querySelectorAll('input[type="radio"]');
    for (var i = 0; i < radios.length; i++) {
      radios[i].addEventListener("change", refreshDescriptions);
    }
    el.querySelector(".acl__ids").addEventListener("input", refreshDescriptions);
  }

  function modeButton(id, value, label, checked) {
    return '<label class="acl__mode"><input type="radio" name="' + id + '-mode" value="' +
      value + '"' + (checked ? " checked" : "") + "> " + escapeText(label) + "</label>";
  }

  /* readList turns the form back into a list. */
  function readList(spec) {
    var el = document.getElementById(spec.id);
    var checked = el.querySelector('input[type="radio"]:checked');
    var text = el.querySelector(".acl__ids").value;

    var ids = [];
    text.split("\n").forEach(function (line) {
      var trimmed = line.trim();
      if (trimmed) {
        ids.push(trimmed);
      }
    });
    return { mode: checked ? checked.value : "deny", ids: ids };
  }

  /* refreshDescriptions keeps the plain-English line in step with the form, so
   * the consequence of a change is visible before it is saved rather than
   * after. */
  function refreshDescriptions() {
    var open = 0;
    LISTS.forEach(function (spec) {
      var list = readList(spec);
      var state = document.getElementById(spec.id + "-state");
      if (state) {
        state.textContent = describe(list, spec.noun);
      }
      if (spec.noun === "talkgroup" && list.mode === "deny" && list.ids.length === 0) {
        open++;
      }
    });
    if (tgSummary) {
      tgSummary.textContent = open === 2 ? "every talkgroup carried" : "restricted";
    }
  }

  function render(cfg) {
    LISTS.forEach(function (spec) {
      renderList(spec, at(cfg, spec.path));
    });
    refreshDescriptions();
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
        if (!body) {
          return;
        }
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

    /* A copy, so a refused save leaves the page showing what the operator
     * typed rather than a half-applied document. */
    var next = JSON.parse(JSON.stringify(loaded));
    LISTS.forEach(function (spec) {
      setAt(next, spec.path, readList(spec));
    });

    saveButton.disabled = true;
    fetch("/api/config", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      credentials: "same-origin",
      body: JSON.stringify({ config: next, summary: "access control" })
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
          /* Every problem at once, each with its fix. An operator should see
           * all of it rather than finding the next one on each attempt. */
          res.body.fields.forEach(function (f) {
            var li = document.createElement("li");
            li.textContent = f.field + ": " + f.problem +
              (f.fix ? " — " + f.fix : "");
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

  saveButton.addEventListener("click", save);
  revertButton.addEventListener("click", function () {
    hide(errorBox);
    hide(savedBox);
    if (loaded) {
      render(loaded);
    }
  });

  /* Who is signed in, in the topbar, the same as the console. */
  var authState = document.getElementById("auth-state");
  fetch("/api/session", { headers: { Accept: "application/json" } })
    .then(function (r) { return r.json(); })
    .then(function (body) {
      if (body && body.authenticated) {
        authState.innerHTML = '<span class="topbar__who">' +
          escapeText(body.username) + "</span>";
      } else {
        authState.innerHTML = '<a class="topbar__link" href="/signin">Sign in</a>';
      }
    })
    .catch(function () { authState.innerHTML = ""; });

  load();
})();
