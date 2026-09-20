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

  /* Suspended while the operator is typing in the contact box, for the reason
     the links page learned: a page that redraws on a timer cannot hold a text
     field. */
  var editing = false;

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

    if (!editing) {
      el("sessions-lifetime").value = duration(x.lifetime_seconds);
    }
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
          button.dataset.armed = "no";
          button.textContent = button.dataset.idle;
          button.classList.remove("button--danger");
        }, 5000);
        return;
      }
      button.disabled = true;
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

    var failing = (health.results || []).filter(function (r) {
      return r.status && r.status !== "passing";
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

    if (!editing) {
      el("callsigns-enabled").value = c.enabled ? "on" : "off";
      el("callsigns-contact").value = c.contact || "";
    }
    show(el("block-callsigns"));
  }

  /* Backup and restore (ADR-0054).
   *
   * **The export is a download and the import is a paste**, because they are
   * different kinds of act: one produces a file an operator keeps, and the
   * other is a decision with consequences that wants looking at first. */
  show(el("block-backup"));

  var restoreDoc = el("restore-document");
  if (restoreDoc) {
    restoreDoc.addEventListener("focus", function () { editing = true; });
    restoreDoc.addEventListener("blur", function () { editing = false; });
  }

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
      editing = false;
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

  var contact = el("callsigns-contact");
  if (contact) {
    contact.addEventListener("focus", function () { editing = true; });
    contact.addEventListener("blur", function () { editing = false; });
  }

  var lifetime = el("sessions-lifetime");
  if (lifetime) {
    lifetime.addEventListener("focus", function () { editing = true; });
    lifetime.addEventListener("blur", function () { editing = false; });
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
        editing = false;
        load();
      }).catch(function (e) {
        say(el("sessions-error"), e.message);
      }).then(function () {
        saveSessions.disabled = false;
        saveSessions.textContent = "Save";
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
        editing = false;
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
