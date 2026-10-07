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

  /* The same mark the static pages draw inline. It is a path rather than a "?"
   * character so it inherits stroke weight from the brand mark and cannot be
   * substituted by whatever font happens to load. */
  var HINT_MARK =
    '<svg class="hint__mark" viewBox="0 0 16 16" fill="none" stroke="currentColor" ' +
    'stroke-width="1.6" stroke-linecap="round" aria-hidden="true" focusable="false">' +
    '<path d="M5.4 6.2a2.9 2.9 0 1 1 3.9 2.7c-.8.3-1.3 1-1.3 1.9v.3"/>' +
    '<circle cx="8" cy="13.4" r="1" fill="currentColor" stroke="none"/></svg>';

  /* The four lists, in the order they appear. Each names where it lives in the
   * configuration so the form and the document cannot drift apart. */
  var LISTS = [
    {
      id: "acl-ts2",
      label: "Timeslot 2",
      path: ["dmr", "access", "talkgroups", "timeslot_2"],
      noun: "talkgroup",
      hint: "Most club traffic is here. Enter numbers or ranges, one per line: " +
        "9 or 3100-3199.",
      detail: "Timeslot 2 is where most club traffic lives, because a hotspot carries local traffic there by convention. <strong>Leaving this empty on \"allow everything except\" carries every talkgroup</strong>, which is what a new instance does and is right for a club whose members are known. Facing the internet, switch to \"allow only\" and name the numbers."
    },
    {
      id: "acl-ts1",
      label: "Timeslot 1",
      path: ["dmr", "access", "talkgroups", "timeslot_1"],
      noun: "talkgroup",
      hint: "Usually wide-area or linked traffic.",
      detail: "Timeslot 1 usually carries wide-area or linked traffic. A club that bridges nothing often leaves this alone. The two timeslots are independent paths on the same radio channel, so a talkgroup allowed here is not allowed on timeslot 2 unless it is listed there too."
    },
    {
      id: "acl-registration",
      label: "Registration",
      path: ["dmr", "access", "registration"],
      noun: "repeater or hotspot ID",
      hint: "Checked at login, before the password. A hotspot ID is nine digits; " +
        "a repeater's is seven.",
      detail: "Checked when a hotspot or repeater logs in, <strong>before its password is examined</strong>, so a refused ID never reaches the credential path at all. That also means the log says which of the two refused it, and \"not permitted here\" and \"wrong password\" are very different messages to somebody trying to get on."
    },
    {
      id: "acl-subscribers",
      label: "Banned radios",
      path: ["dmr", "access", "subscribers"],
      noun: "radio ID",
      /* A ban list and nothing else (0448): there is no "allow only" for
       * radios, so the page offers none. */
      banOnly: true,
      hint: "Every radio may transmit except the ones listed here. A banned " +
        "radio does not disconnect the hotspot carrying it.",
      detail: "Checked on every transmission, which is the important difference. <strong>A refused radio is silenced without disconnecting the hotspot carrying it</strong>, so one member cannot knock another off the network by keying up. A hotspot may carry several radios and they are judged separately."
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
  var bar = window.QSPSaveBar(document.getElementById("save-status"));

  /* What each list showed when it was last drawn from the server's document:
   * its mode and its text. **A list still as it was drawn is not written.**
   * Until 0.1.331 all four were written on every save, so a server with no
   * access block at all was given four empty ones by a save that edited
   * nothing, and the Motorola list was rebuilt from whatever lines of it
   * could be read. */
  var shown = {};

  function listNow(spec) {
    var el = document.getElementById(spec.id);
    if (!el) { return ""; }
    var checked = el.querySelector('input[type="radio"]:checked');
    return (checked ? checked.value : "") + "\n" + el.querySelector(".acl__ids").value;
  }

  function ipscNow() {
    var box = document.getElementById("acl-ipsc-ids");
    return box ? box.value : null;
  }

  function mark() {
    LISTS.forEach(function (spec) { shown[spec.id] = listNow(spec); });
    shown.ipsc = ipscNow();
  }

  function changed() {
    if (!loaded) { return false; }
    return LISTS.some(function (spec) { return listNow(spec) !== shown[spec.id]; }) ||
      ipscNow() !== shown.ipsc;
  }

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
  function describe(list, noun, banOnly) {
    var count = list.ids.length;
    if (banOnly) {
      if (count === 0) {
        return "Every radio may transmit. No radio is banned.";
      }
      return "Every radio may transmit except the " + count + " banned " +
        escapeText(noun) + (count === 1 ? "" : "s") + " listed below.";
    }
    if (list.mode === "permit") {
      if (count === 0) {
        return "Nothing is allowed. A permit list with no entries refuses every " +
          escapeText(noun) + ".";
      }
      return count === 1
        ? "Only the " + escapeText(noun) + " listed below is allowed."
        : "Only the " + count + " " + escapeText(noun) + "s listed below are allowed.";
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
    var permit = !spec.banOnly && list.mode === "permit";

    el.innerHTML =
      '<h3 class="acl__title">' + escapeText(spec.label) +
      (spec.detail
        ? ' <button class="hint" type="button" aria-controls="hint-' +
          escapeText(spec.id) + '" aria-label="Explain this">' + HINT_MARK +
          "</button>"
        : "") +
      "</h3>" +
      (spec.detail
        ? '<p class="hint__text" id="hint-' + escapeText(spec.id) + '">' +
          spec.detail + "</p>"
        : "") +
      '<p class="acl__state" id="' + spec.id + '-state">' +
        describe(list, spec.noun, spec.banOnly) + "</p>" +
      (spec.banOnly
        ? ""
        : '<div class="acl__modes" role="radiogroup" aria-label="' +
          escapeText(spec.label) + ' mode">' +
          modeButton(spec.id, "deny", "Allow everything except", !permit) +
          modeButton(spec.id, "permit", "Allow only", permit) +
          "</div>") +
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
    if (spec.banOnly) {
      return { mode: "deny", ids: ids };
    }
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
        state.textContent = describe(list, spec.noun, spec.banOnly);
      }
      if (spec.noun === "talkgroup" && list.mode === "deny" && list.ids.length === 0) {
        open++;
      }
    });
    if (tgSummary) {
      tgSummary.textContent = open === 2 ? "every talkgroup carried" : "restricted";
    }
  }

  /* renderIPSC draws the Motorola repeater list.
   *
   * **It is not one of LISTS and cannot be**, because the shape and the meaning
   * both differ. `ipsc.allowed_peers` is a plain array of radio IDs with no
   * mode, and an empty one *admits everybody* — the opposite of an empty
   * "allow only" list above. Rendering it with the same control would put two
   * different meanings behind one word, which is what the descriptions on this
   * page exist to prevent. */
  function renderIPSC(cfg) {
    var panel = document.getElementById("ipsc-panel");
    var el = document.getElementById("acl-ipsc");
    if (!panel || !el) {
      return;
    }
    var ipsc = cfg.ipsc || {};
    if (!ipsc.enabled) {
      /* Hidden rather than shown empty: an operator running no repeaters
       * should not have to wonder whether a blank list is refusing something. */
      panel.hidden = true;
      return;
    }
    panel.hidden = false;

    var ids = ipsc.allowed_peers || [];
    var named = ipsc.peer_names || {};
    el.innerHTML =
      '<h3 class="acl__title">Allowed repeaters</h3>' +
      '<p class="acl__state" id="acl-ipsc-state">' + describeIPSC(ids) + "</p>" +
      '<label class="field__label" for="acl-ipsc-ids">One radio ID per line, ' +
        "and a callsign after it if you want one</label>" +
      '<textarea class="field__input acl__ids" id="acl-ipsc-ids" rows="4" ' +
        'spellcheck="false">' + escapeText(writeIPSC(ids, named)) + "</textarea>" +
      '<p class="acl__hint">Takes effect on save. A repeater removed here stops ' +
        "being answered without a restart. A Motorola repeater announces no " +
        "callsign and one on a private radio ID is in no registry, so a name " +
        "written here is the only one the console can show.</p>";

    var box = document.getElementById("acl-ipsc-ids");
    if (box) {
      box.addEventListener("input", function () {
        var state = document.getElementById("acl-ipsc-state");
        if (state) {
          state.textContent = describeIPSC(readIPSC());
        }
      });
    }
  }

  /* describeIPSC says in words what the list does, including the case that
   * catches people: empty admits everybody. */
  function describeIPSC(ids) {
    if (ids.length === 0) {
      return "Every repeater that knows the address is admitted. " +
        "On an address the internet can reach, name the repeaters instead.";
    }
    return (ids.length === 1 ? "Only the repeater listed below is answered."
        : "Only the " + ids.length + " repeaters listed below are answered.") +
      " Everything else is ignored and counted.";
  }

  /* parseIPSC reads the Motorola list: one radio ID to a line, and a
   * callsign after it if the operator wants one.
   *
   * **A line may carry a callsign after the ID.** The two are stored in
   * different places — the ID decides admission, the name decides only what an
   * operator reads — but asking somebody to keep two lists in step by hand is
   * how one of them goes stale. One field, two destinations.
   *
   * **A line that does not begin with an ID is reported, not skipped.** It
   * was skipped, as "the tolerance a half-finished line deserves", and the
   * save went ahead without it: a callsign typed before its ID, or an ID
   * with a letter O in it, took that repeater off the allow list with the
   * answer "saved" (found 2026-10-07, E5). */
  function parseIPSC() {
    var box = document.getElementById("acl-ipsc-ids");
    var out = { ids: [], names: {}, problems: [] };
    if (!box) {
      return out;
    }
    box.value.split("\n").forEach(function (line, index) {
      var t = line.trim();
      if (t === "") {
        return;
      }
      var parts = t.split(/[\s,]+/);
      if (!/^[0-9]+$/.test(parts[0]) || Number(parts[0]) === 0) {
        out.problems.push("Allowed repeaters, line " + (index + 1) + ": \u201c" + t +
          "\u201d does not begin with a radio ID. The ID comes first, then the callsign.");
        return;
      }
      var id = Number(parts[0]);
      out.ids.push(id);
      var name = parts.slice(1).join(" ").trim();
      if (name !== "") {
        out.names[String(id)] = name;
      }
    });
    return out;
  }

  /* readIPSC is the IDs as typed so far, for the sentence above the box. */
  function readIPSC() {
    return parseIPSC().ids;
  }

  /* writeIPSC turns the two stored shapes back into the one field they were
   * typed in. A name whose repeater is no longer listed is dropped rather than
   * carried invisibly: the server refuses to save one, and showing it here
   * would offer an operator a line they cannot keep. */
  function writeIPSC(ids, names) {
    return ids
      .map(function (id) {
        var name = names[String(id)];
        return name ? id + " " + name : String(id);
      })
      .join("\n");
  }

  function render(cfg) {
    LISTS.forEach(function (spec) {
      renderList(spec, at(cfg, spec.path));
    });
    renderIPSC(cfg);
    refreshDescriptions();

    /* The lists are drawn here, after hints.js has already run, so their hint
     * buttons need wiring now. Wiring is idempotent, so re-rendering on a
     * revert does not double them up. */
    if (window.QSPHints) {
      window.QSPHints.wire(document);
    }
    mark();
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
         * four empty lists to save over what is there. */
        return r.json().catch(function () { return {}; }).then(function (body) {
          if (!r.ok || !body.config) {
            throw new Error(body.error || "This server answered " + r.status +
              " and not its configuration.");
          }
          return body;
        });
      })
      .then(function (body) {
        if (!body) {
          return;
        }
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

  /* collect is the document to save: the one loaded, with each list that
   * was edited replaced. It returns what could not be read beside it. */
  function collect() {
    /* A copy, so a refused save leaves the page showing what the operator
     * typed rather than a half-applied document. */
    var next = JSON.parse(JSON.stringify(loaded));
    var problems = [];

    LISTS.forEach(function (spec) {
      if (listNow(spec) === shown[spec.id]) { return; }
      /* Over what was there, so anything else the list carries stays. */
      var was = spec.path.reduce(function (node, key) {
        return node && typeof node === "object" ? node[key] : undefined;
      }, loaded);
      var now = readList(spec);
      var out = was && typeof was === "object" ? JSON.parse(JSON.stringify(was)) : {};
      out.mode = now.mode;
      out.ids = now.ids;
      setAt(next, spec.path, out);
    });

    var box = document.getElementById("acl-ipsc-ids");
    if (box && ipscNow() !== shown.ipsc && next.ipsc && next.ipsc.enabled) {
      var typed = parseIPSC();
      problems = problems.concat(typed.problems);
      next.ipsc.allowed_peers = typed.ids;
      next.ipsc.peer_names = typed.names;
    }
    return { next: next, problems: problems };
  }

  function save() {
    hide(errorBox);
    hide(savedBox);
    errorFields.innerHTML = "";

    var collected = collect();
    var next = collected.next;
    var box = document.getElementById("acl-ipsc-ids");
    if (box) { box.removeAttribute("aria-invalid"); }
    if (collected.problems.length) {
      errorText.textContent = "The list of allowed repeaters could not be read, so " +
        "nothing was saved and no repeater has been dropped from it.";
      collected.problems.forEach(function (message) {
        var li = document.createElement("li");
        li.textContent = message;
        errorFields.appendChild(li);
      });
      show(errorBox);
      bar.failed("Not saved: " + collected.problems[0]);
      box.setAttribute("aria-invalid", "true");
      if (box.scrollIntoView) { box.scrollIntoView({ block: "center" }); }
      box.focus({ preventScroll: true });
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
      body: JSON.stringify({ config: next, base: loaded, summary: "access control" })
    })
      .then(function (r) {
        return r.json().catch(function () { return {}; })
          .then(function (body) { return { status: r.status, body: body }; });
      })
      .then(function (res) {
        saveButton.disabled = false;

        if (res.status === 200) {
          loaded = next;
          /* Redrawn from what was saved, so every list is again "as drawn"
           * and the next save starts from here. */
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
          /* Every problem at once, each with its fix. An operator should see
           * all of it rather than finding the next one on each attempt. */
          res.body.fields.forEach(function (f) {
            var li = document.createElement("li");
            li.textContent = f.field + ": " + f.problem +
              (f.fix ? " — " + f.fix : "");
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

  saveButton.addEventListener("click", save);
  revertButton.addEventListener("click", function () {
    hide(errorBox);
    hide(savedBox);
    if (loaded) {
      render(loaded);
    }
    bar.clean();
  });

  /* The join link, built from the address this page was reached by.
   *
   * An operator reading this arrived by the same route their members will, so
   * the browser already knows the address that works — including a proxy, a
   * hostname, or a port that a value in the configuration would not. */
  var joinURL = document.getElementById("join-url");
  var copyJoin = document.getElementById("copy-join");
  var copyNote = document.getElementById("copy-note");

  if (joinURL) {
    joinURL.textContent = window.location.origin + "/join";
  }

  if (copyJoin) {
    copyJoin.addEventListener("click", function () {
      var text = joinURL ? joinURL.textContent : "";
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(text).then(function () {
          copyNote.textContent = "Copied.";
        }).catch(function () {
          /* Clipboard access is refused on an insecure origin, which is a
           * club on a LAN over plain HTTP — the ordinary case rather than an
           * error. Say what to do instead of failing silently. */
          copyNote.textContent = "Could not copy automatically. Select the address and copy it.";
        });
        return;
      }
      copyNote.textContent = "This browser will not copy for us. Select the address and copy it.";
    });
  }

  /* ---- A member's own password --------------------------------------- */

  function el(id) { return document.getElementById(id); }
  function show(e) { if (e) { e.hidden = false; } }
  function hide(e) { if (e) { e.hidden = true; } }

  function credentialPeer() {
    var raw = (el("credential-peer") || {}).value || "";
    return raw.trim();
  }

  function credentialCall(method) {
    var peer = credentialPeer();
    hide(el("credential-error"));
    hide(el("credential-note"));
    hide(el("credential-result"));

    if (!peer) {
      el("credential-error").textContent = "Enter the radio ID first.";
      show(el("credential-error"));
      return;
    }

    fetch("/api/peers/" + encodeURIComponent(peer) + "/password", {
      method: method,
      headers: { Accept: "application/json" },
      credentials: "same-origin"
    }).then(function (r) {
      return r.json().then(function (b) {
        if (!r.ok) { throw new Error((b && b.error) || "that did not work"); }
        return b;
      });
    }).then(function (b) {
      if (b.password) {
        /* Shown once and never fetched again: it lives in a file on the server
         * and the console cannot read it back. */
        el("credential-password").textContent = b.password;
        show(el("credential-result"));
        return;
      }
      if (b.reason) {
        el("credential-note").textContent = b.reason;
        show(el("credential-note"));
      }
    }).catch(function (e) {
      el("credential-error").textContent = e.message;
      show(el("credential-error"));
    });
  }

  if (el("credential-issue")) {
    el("credential-issue").addEventListener("click", function () {
      credentialCall("POST");
    });
  }
  if (el("credential-revoke")) {
    el("credential-revoke").addEventListener("click", function () {
      credentialCall("DELETE");
    });
  }

  load();
})();
