/* The administration page (ADR-0055).
 *
 * **Five blocks, each answering a question an administrator asks**, and
 * deliberately not a settings editor. The rule the page is held to: it may edit
 * a setting when it is the page that reports the problem, and — binding harder
 * — if it is not reporting a problem with a setting, it does not get to edit
 * it. Today that is the callsign lookup and nothing else.
 *
 * Several defects found on 2026-09-08 existed because nothing could show them:
 * an identifier that appeared in one log line and scrolled away, a callsign
 * lookup off on one server and on on the other with nothing saying so, and a
 * version read out of the journal all evening.
 */
(function () {
  "use strict";

  var POLL_MS = 10000;

  var loading = document.getElementById("loading");
  var note = document.getElementById("admin-note");

  /* What the refresh last put in each box it fills.
   *
   * **The refresh fills a box only while the box still says what the refresh
   * last put there.** Until 0.1.332 it held off only while a box had the
   * cursor in it. Choose "off" for the callsign lookup, or type a new login
   * length, move to the next box, and within ten seconds the page had put
   * the old value back: Save then saved what the server already had, and
   * said "Saved" (found 2026-10-07, E2). */
  var drawn = {};

  function fill(node, value) {
    if (!node) { return; }
    if (document.activeElement === node) { return; }
    if (node.id in drawn && node.value !== drawn[node.id]) { return; }
    node.value = value;
    drawn[node.id] = node.value;
  }

  /* saved lets the refresh fill these boxes again, from what was saved. */
  function saved(ids) {
    ids.forEach(function (id) { delete drawn[id]; });
  }

  load();
  setInterval(load, POLL_MS);

  function el(id) { return document.getElementById(id); }

  function show(node) { if (node) node.hidden = false; }
  function hide(node) { if (node) node.hidden = true; }

  function say(node, message) {
    if (!node) { return; }
    node.textContent = message;
    node.hidden = !message;
  }

  function escapeText(value) {
    return String(value == null ? "" : value)
      .replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  }

  function fact(label, value) {
    return '<div class="link__fact"><dt>' + escapeText(label) + "</dt><dd>" +
      escapeText(value) + "</dd></div>";
  }

  /* **A duration alone reads as a fault.** A server restarted four minutes ago
   * is correct and looks alarming; the clock time beside it makes a small
   * number legible. */
  function uptime(seconds, startedAt) {
    var since = "";
    var when = new Date(startedAt);
    if (!isNaN(when.getTime())) {
      since = ", since " + when.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
    }
    var s = Math.max(0, Math.floor(seconds));
    if (s < 60) { return s + "s" + since; }
    if (s < 3600) { return Math.floor(s / 60) + "m" + since; }
    if (s < 86400) { return Math.floor(s / 3600) + "h " + Math.floor((s % 3600) / 60) + "m" + since; }
    return Math.floor(s / 86400) + "d " + Math.floor((s % 86400) / 3600) + "h" + since;
  }

  function load() {
    fetch("/api/admin", { headers: { Accept: "application/json" }, credentials: "same-origin" })
      .then(function (r) {
        if (r.status === 401) { throw new Error("Sign in to read this server."); }
        if (!r.ok) { throw new Error("This server did not answer."); }
        return r.json();
      })
      .then(render)
      .catch(function (e) {
        hide(loading);
        say(note, e.message);
      });
  }

  function render(body) {
    hide(loading);
    hide(note);

    renderServer(body.server || {});
    renderAgreement(body.agreement || {});
    renderServices(body.services || {});
    renderCallsigns((body.services || {}).callsigns || {});
    renderSessions(body.sessions || {});
    renderTexts(body.texts || {});
  }

  /* The text block appears only where a text can leave: the server says
   * whether its DMR listener is running, and how long a message may be. */
  function renderTexts(x) {
    if (!x.available) { return; }
    el("text-limit").textContent = "Up to " + x.max_characters +
      " characters. That is what one message carries; a radio may show fewer.";
    if (!x.network) {
      el("text-scope").textContent = "This server does not forward, so a text " +
        "can only go to one hotspot: enter its ID, as the Network page lists it.";
    }
    show(el("block-text"));
  }

  /* The sessions block: what a login lasts, and what that means now.
   *
   * **It reports before it edits**, which is the condition this page is held
   * to. The three facts it states are the ones the configuration file cannot:
   * which value is in force, whether that is the operator's choice or the
   * built-in default, and when the session reading the page ends -- the last
   * being what an operator part-way through a restore actually wants. */
  function renderSessions(x) {
    var parts = [];
    parts.push("A login lasts " + duration(x.lifetime_seconds) +
      (x.default ? ", the built-in default." : ", from this server's configuration."));
    if (x.active) {
      parts.push(x.active === 1 ? "One session is active." :
        x.active + " sessions are active.");
    }
    if (x.expires_in_seconds) {
      parts.push("Yours ends in " + duration(x.expires_in_seconds) + ".");
    }
    say(el("sessions-state"), parts.join(" "));

    fill(el("sessions-lifetime"), duration(x.lifetime_seconds));
  }

  /* Seconds as an operator writes them: 12h, 90m, 45s. */
  function duration(seconds) {
    var n = Number(seconds) || 0;
    if (n % 3600 === 0) { return (n / 3600) + "h"; }
    if (n % 60 === 0) { return (n / 60) + "m"; }
    return n + "s";
  }

  /* And back again, refusing what the server would refuse.
   *
   * **The bounds come from the report, not from this file.** A page carrying
   * its own copy is a page that accepts a value the configuration will not
   * load, which is worse than a round trip. */
  function seconds(text) {
    var m = /^\s*(\d+)\s*([hms])\s*$/i.exec(text || "");
    if (!m) { throw new Error("Write it like 12h, 24h or 90m."); }
    var n = Number(m[1]);
    switch (m[2].toLowerCase()) {
      case "h": return n * 3600;
      case "m": return n * 60;
      default: return n;
    }
  }

  function renderServer(s) {
    var facts = "";
    facts += fact("Name", s.network || "not set");
    facts += fact("Callsign", s.callsign || "not set");
    facts += fact("Identifier", s.identifier || "not generated");
    facts += fact("Version", s.version || "unknown");
    facts += fact("Running for", uptime(s.uptime_seconds, s.started_at));
    el("server-facts").innerHTML = facts;
    show(el("block-server"));
  }

  /* **The block that earns the page.** A server can be several saved changes
   * away from what it is running, and before this that was reported only as a
   * sentence beside whichever link happened to be saved last. */
  function renderAgreement(a) {
    var pill = el("agreement-pill");
    var pending = a.pending || [];

    if (!a.writable) {
      pill.textContent = "Read only";
      pill.className = "pill pill--warn";
      say(el("agreement-summary"), a.not_writable ||
        "this server cannot be configured from here");
      hide(el("agreement-pending"));
      offerRestart();
      show(el("block-agreement"));
      return;
    }

    if (a.agrees) {
      pill.textContent = "In agreement";
      pill.className = "pill pill--good";
      say(el("agreement-summary"),
        "The running server matches its configuration. Nothing is waiting.");
      hide(el("agreement-pending"));
      offerRestart();
      show(el("block-agreement"));
      return;
    }

    pill.textContent = "Awaiting restart";
    pill.className = "pill pill--warn";
    say(el("agreement-summary"), pending.length === 1
      ? "One setting has been saved and is not in effect. Until QSP restarts, " +
        "the server carries on as it was."
      : pending.length + " settings have been saved and are not in effect. Until " +
        "QSP restarts, the server carries on as it was.");

    var list = el("agreement-pending");
    list.innerHTML = pending.map(function (p) {
      return "<li>" + escapeText(p) + "</li>";
    }).join("");
    show(list);

    offerRestart();
    show(el("block-agreement"));
  }

  /* **Always offered here, and only conditionally on the Links page.**
   *
   * The Links page shows a restart control beside the message asking for one,
   * because a button next to a status row is a slip away from being pressed.
   * This page is different: it is where the acts belonging to the server live,
   * and it is where an operator comes looking for a restart whether or not
   * anything is waiting. Hiding it here sends them to find a terminal, which is
   * the failure the page exists to end.
   *
   * Two clicks either way, and the note says what drops. */
  function offerRestart() {
    var host = el("agreement-actions");
    if (!host || host.querySelector("[data-restart]")) { return; }

    var button = document.createElement("button");
    button.className = "button button--quiet";
    button.type = "button";
    button.setAttribute("data-restart", "yes");
    button.textContent = "Restart QSP";
    button.dataset.idle = "Restart QSP";
    button.addEventListener("click", function () {
      /* Two clicks, like every other act that takes traffic off the air. */
      if (button.dataset.armed !== "yes") {
        button.dataset.armed = "yes";
        button.textContent = "Restart now? Everything drops.";
        button.classList.add("button--danger");
        setTimeout(function () {
          /* Not once it has been pressed: the label would go back to
           * "Restart QSP" on a button that is busy restarting it. */
          if (button.disabled) { return; }
          button.dataset.armed = "no";
          button.textContent = button.dataset.idle;
          button.classList.remove("button--danger");
        }, 5000);
        return;
      }
      button.disabled = true;
      button.classList.remove("button--danger");
      button.textContent = "Restarting";
      fetch("/api/restart", {
        method: "POST",
        headers: { Accept: "application/json" },
        credentials: "same-origin"
      }).then(function (r) {
        return r.json().then(function (b) {
          if (!r.ok) { throw new Error(b.error || "could not restart QSP"); }
          return b;
        });
      }).then(function (b) {
        say(note, b.note + " This page will reconnect on its own.");
      }).catch(function (e) {
        button.disabled = false;
        button.textContent = "Restart QSP";
        say(note, e.message);
      });
    });
    host.appendChild(button);
  }

  function renderServices(s) {
    var c = s.callsigns || {};
    var health = s.health || {};
    var facts = "";

    facts += fact("Callsign lookup", c.usable ? "on" : (c.enabled ? "on, cannot run" : "off"));
    facts += fact("Subsystems", health.status || "unknown");

    /* The server's word for a subsystem that is well is "healthy". This
     * compared with "passing", which it never says, so every subsystem was
     * listed as not passing on a server with nothing wrong. */
    var failing = (health.results || []).filter(function (r) {
      return r.status && r.status !== "healthy";
    });
    facts += fact("Not passing", failing.length === 0
      ? "none"
      : failing.map(function (r) { return r.name; }).join(", "));

    el("services-facts").innerHTML = facts;
    show(el("block-services"));
  }

  function renderCallsigns(c) {
    say(el("callsigns-state"), c.why ||
      "Callsign lookup is on and working. Last heard names the radios it sees.");

    fill(el("callsigns-enabled"), c.enabled ? "on" : "off");
    fill(el("callsigns-contact"), c.contact || "");
    show(el("block-callsigns"));
  }

  /* Backup and restore (ADR-0054).
   *
   * **The export is a download and the import is a paste**, because they are
   * different kinds of act: one produces a file an operator keeps, and the
   * other is a decision with consequences that wants looking at first. */
  show(el("block-backup"));


  var readBackup = el("restore");
  if (readBackup) {
    readBackup.addEventListener("click", function () {
      hide(el("restore-error"));
      hide(el("restore-done"));
      hide(el("restore-confirm"));
      restore(false);
    });
  }

  var confirmed = el("restore-confirmed");
  if (confirmed) {
    confirmed.addEventListener("click", function () { restore(true); });
  }

  /* **Asked twice, and the first answer is what it would do.** An import
   * replaces every setting on the server, so the operator sees the date the
   * backup was taken, the credentials it cannot bring back, and what taking its
   * identity means, before anything changes. */
  function restore(confirm) {
    var document_ = (el("restore-document").value || "").trim();
    if (!document_) {
      say(el("restore-error"), "Paste a backup first.");
      return;
    }
    var newIdentity = el("restore-identity-choice").value === "new";

    fetch("/api/admin/restore", {
      method: "POST",
      headers: { "Content-Type": "application/json", Accept: "application/json" },
      credentials: "same-origin",
      body: JSON.stringify({ document: document_, confirm: confirm, new_identity: newIdentity })
    }).then(function (r) {
      return r.json().then(function (b) { return { ok: r.ok, status: r.status, body: b }; });
    }).then(function (res) {
      if (!res.ok && res.status === 428) {
        say(el("restore-summary"), res.body.summary);
        say(el("restore-identity"), res.body.identity);
        el("restore-missing").innerHTML = (res.body.missing_credentials || [])
          .map(function (m) {
            return "<li>" + escapeText(m.name) + " — " + escapeText(m.fix) + "</li>";
          }).join("");
        show(el("restore-confirm"));
        return;
      }
      if (!res.ok) { throw new Error(res.body.error || "could not restore"); }

      hide(el("restore-confirm"));
      el("restore-document").value = "";
      var missing = (res.body.missing_credentials || []).length;
      say(el("restore-done"),
        "Restored. This server is now " + (res.body.identifier || "unnamed") +
        (res.body.replaced ? ", a replacement for the one that made the backup." : ", with a new identity.") +
        (missing ? " " + missing + " credentials are not restored: links and members " +
          "are refused until they are reissued." : "") +
        " QSP has to restart before any of it takes effect.");
      load();
    }).catch(function (e) {
      say(el("restore-error"), e.message);
    });
  }

  /* The full backup (ADR-0065): the settings, every stored credential and the
   * password files, encrypted with a passphrase.
   *
   * **Both halves were built on the server and on no page** until 0.1.320, so
   * the backup that rebuilds a server could be made and restored only by
   * somebody writing the requests by hand.
   *
   * **The passphrase goes in the body of a POST and nowhere else**: not in an
   * address, which proxies log and browsers remember, and not kept in this
   * page a moment longer than the request needs it. */
  var fullBackup = el("full-backup");
  if (fullBackup) {
    fullBackup.addEventListener("click", function () {
      var first = el("full-backup-passphrase");
      var again = el("full-backup-passphrase-again");
      hide(el("full-backup-error"));
      hide(el("full-backup-warning"));
      if (!first.value) {
        say(el("full-backup-error"), "Choose a passphrase first.");
        return;
      }
      /* Refused here, before anything is made: a backup locked with a typing
         mistake is a backup nobody can open, and looks like any other. */
      if (first.value !== again.value) {
        say(el("full-backup-error"), "The two passphrases are not the same. Nothing was made.");
        return;
      }
      fullBackup.disabled = true;
      fetch("/api/admin/full-backup", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "same-origin",
        body: JSON.stringify({ passphrase: first.value })
      }).then(function (r) {
        if (!r.ok) {
          return r.json().then(function (b) { throw new Error(b.error || "That backup could not be made."); });
        }
        var named = /filename="([^"]+)"/.exec(r.headers.get("Content-Disposition") || "");
        var warning = r.headers.get("X-QSP-Passphrase-Warning") || "";
        return r.blob().then(function (file) {
          var link = document.createElement("a");
          link.href = URL.createObjectURL(file);
          link.download = named ? named[1] : "qsp.qspfull";
          document.body.appendChild(link);
          link.click();
          link.remove();
          URL.revokeObjectURL(link.href);
          first.value = "";
          again.value = "";
          say(el("full-backup-warning"), "Downloaded " + link.download + ". " + warning);
        });
      }).catch(function (e) {
        say(el("full-backup-error"), e.message);
      }).then(function () {
        fullBackup.disabled = false;
      });
    });
  }

  /* What was read from the chosen file, kept between the two presses so the
   * second restores exactly what the first described. */
  var fullPending = null;

  var fullRead = el("full-restore");
  if (fullRead) {
    fullRead.addEventListener("click", function () {
      hide(el("full-restore-error"));
      hide(el("full-restore-done"));
      hide(el("full-restore-result"));
      hide(el("full-restore-confirm"));
      fullPending = null;

      var chosen = el("full-restore-file").files[0];
      var passphrase = el("full-restore-passphrase").value;
      if (!chosen) {
        say(el("full-restore-error"), "Choose the backup file first.");
        return;
      }
      if (!passphrase) {
        say(el("full-restore-error"), "Type the passphrase this backup was made with.");
        return;
      }
      /* Read as a data URL because the file is binary and travels as base64;
         the browser does the encoding, at any size the server accepts. */
      var reader = new FileReader();
      reader.onerror = function () {
        say(el("full-restore-error"), "That file could not be read.");
      };
      reader.onload = function () {
        var encoded = String(reader.result);
        fullPending = { document: encoded.slice(encoded.indexOf(",") + 1), passphrase: passphrase };
        fullRestore(false);
      };
      reader.readAsDataURL(chosen);
    });
  }

  var fullConfirmed = el("full-restore-confirmed");
  if (fullConfirmed) {
    fullConfirmed.addEventListener("click", function () { fullRestore(true); });
  }

  function listed(heading, items) {
    if (!items || !items.length) { return ""; }
    return "<li>" + escapeText(heading) + ": " + items.map(escapeText).join(", ") + "</li>";
  }

  /* **Asked twice, as the other restore is.** The first answer is what the
   * server would do with this file, in its own words; nothing changes until
   * the second press. */
  function fullRestore(confirm) {
    if (!fullPending) { return; }
    fetch("/api/admin/full-restore", {
      method: "POST",
      headers: { "Content-Type": "application/json", Accept: "application/json" },
      credentials: "same-origin",
      body: JSON.stringify({
        document: fullPending.document, passphrase: fullPending.passphrase, confirm: confirm
      })
    }).then(function (r) {
      return r.json().then(function (b) { return { ok: r.ok, status: r.status, body: b }; });
    }).then(function (res) {
      if (!res.ok && res.status === 428) {
        say(el("full-restore-summary"), res.body.summary);
        say(el("full-restore-identity"), res.body.identity);
        say(el("full-restore-credentials"), res.body.credentials);
        say(el("full-restore-kept"), res.body.kept);
        el("full-restore-contents").innerHTML =
          listed("Credentials in this backup", res.body.credential_names) +
          listed("Password files in this backup", res.body.password_files) +
          listed("Password files it does not have, and this machine does not either",
            res.body.missing_password_files);
        show(el("full-restore-confirm"));
        return;
      }
      if (!res.ok) { throw new Error(res.body.error || "That backup could not be restored."); }

      hide(el("full-restore-confirm"));
      fullPending = null;
      el("full-restore-passphrase").value = "";
      el("full-restore-file").value = "";
      var restart = (res.body.needs_restart || []).length;
      say(el("full-restore-done"),
        "Restored, as version " + res.body.version + " of this server's settings." +
        (restart ? " QSP has to restart before all of it takes effect." : ""));
      var failed = (res.body.password_files_failed || []).map(function (f) {
        return f.path + " (" + f.error + ")";
      });
      var result = el("full-restore-result");
      result.innerHTML =
        listed("Credentials restored", res.body.credentials) +
        listed("Password files written", res.body.password_files) +
        listed("Password files that could not be written", failed) +
        listed("Password files still missing; what needs them is refused until they are reissued",
          res.body.missing_password_files);
      result.hidden = !result.innerHTML;
      load();
    }).catch(function (e) {
      say(el("full-restore-error"), e.message);
    });
  }

  /* **Arming a destructive button**, which this page needs three times over:
   * restarting, resetting somebody's password, and removing an account. The
   * same shape as the Links page and deliberately a copy rather than shared —
   * two pages, two scripts, and no module system in a console that vendors
   * nothing (ADR-0025).
   *
   * Caught by the gate: an earlier version of this block called `armed` as
   * though it were global, and it is defined in links.js. The script would have
   * thrown at load and taken the administration page's chrome with it. */
  function armed(button, question, act) {
    if (button.dataset.armed !== "yes") {
      button.dataset.armed = "yes";
      button.textContent = question;
      button.classList.add("button--danger");
      setTimeout(function () {
        if (button.disabled) { return; }
        button.dataset.armed = "no";
        button.textContent = button.dataset.idle;
        button.classList.remove("button--danger");
      }, 5000);
      return;
    }
    act();
  }

  /* Administrators (ADR-0056). Everything after the first is here rather than
   * in a shell, because an administrator adding another is already
   * authenticated and that is the check. */
  loadUsers();

  function loadUsers() {
    fetch("/api/users", { headers: { Accept: "application/json" }, credentials: "same-origin" })
      .then(function (r) { return r.ok ? r.json() : null; })
      .then(function (body) {
        if (!body) { return; }
        renderUsers(body.users || []);
        show(el("block-users"));
      })
      .catch(function () { /* the block simply does not appear */ });
  }

  function renderUsers(users) {
    var host = el("users-list");
    if (!host) { return; }

    host.innerHTML = users.map(function (u) {
      var facts = fact("Added", u.created) +
        fact("Last seen", u.last_seen || "never") +
        fact("State", u.locked ? "locked out" : "in use");
      return '<div class="link">' +
        '<div class="link__head">' +
        '<span class="link__name">' + escapeText(u.username) + "</span>" +
        (u.self ? '<span class="pill">you</span>' : "") +
        '<button class="button button--quiet" type="button" data-reset="' +
        escapeText(u.username) + '">Reset password</button>' +
        (users.length > 1
          ? '<button class="button button--quiet" type="button" data-remove-user="' +
            escapeText(u.username) + '">Remove</button>'
          : "") +
        "</div>" +
        '<dl class="link__facts">' + facts + "</dl>" +
        "</div>";
    }).join("");

    /* **No Remove at all on the last administrator.** The server refuses it
     * too, and a button that always fails is a button that teaches an operator
     * to distrust the page. */
    wireUserButtons();
  }

  function wireUserButtons() {
    Array.prototype.forEach.call(
      el("users-list").querySelectorAll("[data-reset]"), function (button) {
        button.dataset.idle = "Reset password";
        button.addEventListener("click", function () {
          var name = button.getAttribute("data-reset");
          armed(button, "Reset " + name + "'s password?", function () {
            send("POST", "/api/users/" + encodeURIComponent(name) + "/password", null,
              function (b) {
                say(el("users-secret"), b.username + "'s new password is " + b.password +
                  ". " + b.note);
                loadUsers();
              });
          });
        });
      });

    Array.prototype.forEach.call(
      el("users-list").querySelectorAll("[data-remove-user]"), function (button) {
        button.dataset.idle = "Remove";
        button.addEventListener("click", function () {
          var name = button.getAttribute("data-remove-user");
          armed(button, "Remove " + name + "?", function () {
            send("DELETE", "/api/users/" + encodeURIComponent(name), null, function (b) {
              say(el("users-secret"), b.note);
              loadUsers();
            });
          });
        });
      });
  }

  var addUser = el("user-add");
  if (addUser) {
    addUser.addEventListener("click", function () {
      hide(el("users-error"));
      hide(el("users-secret"));
      var name = (el("user-name").value || "").trim();
      if (!name) {
        say(el("users-error"), "A callsign, please.");
        return;
      }
      send("POST", "/api/users", { username: name }, function (b) {
        el("user-name").value = "";
        say(el("users-secret"), b.username + "'s password is " + b.password + ". " + b.note);
        loadUsers();
      });
    });
  }

  /* Change my password. The three boxes are checked here first so that a
   * slip is caught before anything is sent, and the server checks again:
   * this is a convenience and that is the rule.
   *
   * **The boxes are emptied whatever happens**, so a password is not left
   * sitting in a page, and the answer is written beside the button and not
   * in the Administrators messages a screen above it. */
  var passwordForm = el("password-form");
  if (passwordForm) {
    passwordForm.addEventListener("submit", function (ev) {
      ev.preventDefault();
      var err = el("password-error");
      var done = el("password-done");
      var button = el("password-save");
      hide(err);
      hide(done);

      var current = el("password-current").value;
      var next = el("password-new").value;
      var again = el("password-again").value;
      if (!current) { say(err, "Type your current password."); return; }
      if (next.length < 12) {
        say(err, "The new password needs at least 12 characters.");
        return;
      }
      if (next !== again) {
        say(err, "The two new passwords are not the same. Type them again.");
        el("password-new").value = "";
        el("password-again").value = "";
        el("password-new").focus();
        return;
      }
      if (next === current) {
        say(err, "The new password is the one you have now. Choose a different one.");
        return;
      }

      button.disabled = true;
      button.textContent = "Changing";
      fetch("/api/account/password", {
        method: "POST",
        headers: { "Content-Type": "application/json", Accept: "application/json" },
        credentials: "same-origin",
        body: JSON.stringify({ current: current, "new": next })
      }).then(function (r) {
        return r.json().catch(function () { return {}; }).then(function (b) {
          if (!r.ok) { throw new Error(sentence(b.error) || "The password was not changed."); }
          return b;
        });
      }).then(function (b) {
        passwordForm.reset();
        say(done, b.note || "Your password is changed.");
      }).catch(function (e) {
        el("password-current").value = "";
        say(err, e.message || "The server did not answer. The password was not changed.");
        el("password-current").focus();
      }).then(function () {
        button.disabled = false;
        button.textContent = "Change password";
      });
    });
  }

  /* The server's messages start in lower case, as its log lines do. On their
   * own beside a button they are a sentence. */
  function sentence(text) {
    text = String(text || "").trim();
    if (!text) { return ""; }
    text = text.charAt(0).toUpperCase() + text.slice(1);
    return /[.!?]$/.test(text) ? text : text + ".";
  }

  function send(method, path, body, then) {
    hide(el("users-error"));
    fetch(path, {
      method: method,
      headers: { "Content-Type": "application/json", Accept: "application/json" },
      credentials: "same-origin",
      body: body ? JSON.stringify(body) : null
    }).then(function (r) {
      return r.json().then(function (b) {
        if (!r.ok) { throw new Error(b.error || "that did not work"); }
        return b;
      });
    }).then(then).catch(function (e) {
      say(el("users-error"), e.message);
    });
  }

  var saveSessions = el("sessions-save");
  if (saveSessions) {
    saveSessions.addEventListener("click", function () {
      hide(el("sessions-error"));
      var wanted;
      try {
        wanted = seconds(el("sessions-lifetime").value);
      } catch (e) {
        say(el("sessions-error"), e.message);
        return;
      }
      saveSessions.disabled = true;
      saveSessions.textContent = "Saving";
      fetch("/api/admin/session-lifetime", {
        method: "PUT",
        headers: { "Content-Type": "application/json", Accept: "application/json" },
        credentials: "same-origin",
        body: JSON.stringify({ seconds: wanted })
      }).then(function (r) {
        return r.json().then(function (b) {
          if (!r.ok) { throw new Error(b.error || "could not save"); }
          return b;
        });
      }).then(function () {
        saved(["sessions-lifetime"]);
        load();
      }).catch(function (e) {
        say(el("sessions-error"), e.message);
      }).then(function () {
        saveSessions.disabled = false;
        saveSessions.textContent = "Save";
      });
    });
  }

  function whole(id) {
    var raw = (el(id).value || "").trim();
    return /^[0-9]+$/.test(raw) ? parseInt(raw, 10) : 0;
  }

  var sendText = el("text-send");
  if (sendText) {
    sendText.addEventListener("click", function () {
      hide(el("text-error"));
      hide(el("text-done"));
      sendText.disabled = true;
      sendText.textContent = "Sending";
      fetch("/api/admin/text", {
        method: "POST",
        headers: { "Content-Type": "application/json", Accept: "application/json" },
        credentials: "same-origin",
        body: JSON.stringify({
          peer: whole("text-peer"),
          talkgroup: whole("text-talkgroup"),
          timeslot: parseInt(el("text-timeslot").value, 10),
          from: whole("text-from"),
          text: el("text-body").value
        })
      }).then(function (r) {
        return r.json().then(function (b) {
          if (!r.ok) { throw new Error(b.error || "could not send"); }
          return b;
        });
      }).then(function (b) {
        say(el("text-done"), b.note);
        if (b.warning) { say(el("text-error"), b.warning); }
      }).catch(function (e) {
        say(el("text-error"), e.message);
      }).then(function () {
        sendText.disabled = false;
        sendText.textContent = "Send";
      });
    });
  }

  var save = el("callsigns-save");
  if (save) {
    save.addEventListener("click", function () {
      hide(el("callsigns-error"));
      hide(el("callsigns-done"));
      save.disabled = true;
      save.textContent = "Saving";
      fetch("/api/admin/callsigns", {
        method: "PUT",
        headers: { "Content-Type": "application/json", Accept: "application/json" },
        credentials: "same-origin",
        body: JSON.stringify({
          enabled: el("callsigns-enabled").value === "on",
          contact: el("callsigns-contact").value.trim()
        })
      }).then(function (r) {
        return r.json().then(function (b) {
          if (!r.ok) { throw new Error(b.error || "could not save"); }
          return b;
        });
      }).then(function (b) {
        saved(["callsigns-enabled", "callsigns-contact"]);
        var c = b.callsigns || {};
        say(el("callsigns-done"), c.usable
          ? "Saved. Callsigns will fill in as radios are heard; the first fetch " +
            "takes a moment."
          : "Saved.");
        load();
      }).catch(function (e) {
        say(el("callsigns-error"), e.message);
      }).then(function () {
        save.disabled = false;
        save.textContent = "Save";
      });
    });
  }
})();
