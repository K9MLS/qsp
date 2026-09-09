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

  var contact = el("callsigns-contact");
  if (contact) {
    contact.addEventListener("focus", function () { editing = true; });
    contact.addEventListener("blur", function () { editing = false; });
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
