/* The links page.
 *
 * **Nothing showed a link anywhere before this.** One opened, authenticated,
 * carried audio between two servers, and the only report of any of it was a
 * line in /healthz. An operator watching a console saw peers and talkgroups and
 * no indication that another network existed — which made a working link and a
 * dead one look identical, and cost an afternoon of deciding which it was.
 */
(function () {
  "use strict";

  var loading = document.getElementById("loading");
  var signedOut = document.getElementById("signed-out");
  var form = document.getElementById("form");
  var list = document.getElementById("links");
  var count = document.getElementById("link-count");

  var POLL_MS = 5000;

  /* **A page that redraws every five seconds cannot hold a text box.** The
   * address field is edited in place, and a poll landing mid-keystroke would
   * replace what the operator had typed with what the server still has. */
  var editing = false;

  /* What the instance last told us about itself, so the address suggestion can
     follow the kind of link being offered rather than being filled once. */
  var lastIdentity = null;

  /* Set by anything that reports a restart is needed, so the button appears
     beside the message rather than sitting on a page that does not need it. */
  var needsRestart = false;

  function show(el) { if (el) { el.hidden = false; } }
  function hide(el) { if (el) { el.hidden = true; } }

  function text(el, value) {
    if (el) {
      el.textContent = value;
    }
  }

  function escapeText(value) {
    return String(value === undefined || value === null ? "" : value)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  }

  function idle(seconds) {
    if (seconds < 60) { return seconds + "s"; }
    if (seconds < 3600) { return Math.floor(seconds / 60) + "m"; }
    return Math.floor(seconds / 3600) + "h";
  }

  /* A link's state, in the order an operator asks the questions: is the socket
   * up, has anything ever come back, and is it still coming. */
  function state(link) {
    /* **Configured and running are different questions**, and the page used to
     * ask only the second. Upstreams are built once at startup, so a link
     * accepted since then has no socket and a link removed since then still
     * has one. Two removed links showed here as healthy for three hours while
     * the remover correctly answered 404 for both. */
    if (!link.configured) { return { label: "Removed", kind: "warn" }; }
    /* A link that dialled in is in nobody's configuration here, so the
     * questions below — is it disabled, would a restart open it — are about a
     * document that does not describe it. It is connected or it is not. */
    if (link.inbound) {
      return link.open
        ? { label: "Carrying", kind: "good" }
        : { label: "Registering", kind: "warn" };
    }
    /* **Disabled is not awaiting a restart.** This page said it was, because
     * the reconcile never read the enabled flag: a link turned off in the
     * configuration was labelled "Awaiting restart" and advised to restart QSP
     * to open it. Restarting a live network to discover nothing changed is an
     * expensive way to read a boolean. */
    if (!link.enabled) { return { label: "Disabled", kind: "warn" }; }
    if (!link.open && link.pending_restart) {
      return { label: "Awaiting restart", kind: "warn" };
    }
    if (!link.open) { return { label: "Closed", kind: "bad" }; }
    if (link.rejected > 0 && link.received === 0) {
      return { label: "Rejecting", kind: "bad" };
    }
    if (!link.ever_received) { return { label: "Nothing yet", kind: "warn" }; }
    return { label: "Carrying", kind: "good" };
  }

  function render(links) {
    text(count, links.length + (links.length === 1 ? " LINK" : " LINKS"));

    /* **Recomputed every pass, not remembered.** A restart that has happened
       leaves nothing to restart for, and a button that outlives its reason is
       the same defect as a page that says a link needs a restart after it has
       had one. */
    needsRestart = false;

    if (links.length === 0) {
      list.innerHTML =
        '<div class="empty">' +
        '<p class="empty__title">No links are configured</p>' +
        '<p class="empty__body">A link carries traffic between this network and ' +
        "another. Both administrators have to agree one before either can " +
        "carry anything.</p></div>";
      return;
    }

    var html = "";
    for (var i = 0; i < links.length; i++) {
      var l = links[i];
      var st = state(l);
      html +=
        '<div class="link">' +
        '<div class="link__head">' +
        /* **The far end's name, not this server's own.** ADR-0052 rule 2 says
         * both consoles show a link with the same name, and this satisfied it
         * literally and got it wrong: the heading was the local label, which is
         * whatever the *dialling* administrator called their configuration
         * block. So an administrator on the listening server read a link to
         * somebody else under their own server's name.
         *
         * Three names, not two: a local label names a configuration block on
         * the machine that holds one, a display name is what a server announces
         * about itself, and an identifier is what the network uses. The heading
         * is the display name; the local label follows it when there is one, so
         * the operator who typed it can still find it. */
        '<span class="link__name">' + escapeText(l.network || l.name) + "</span>" +
        (l.network && l.name && l.network !== l.name
          ? '<span class="link__local">' + escapeText(l.name) + "</span>"
          : "") +
        '<span class="pill pill--' + st.kind + '">' + st.label + "</span>" +
        /* **A page that creates a link has to remove one.** Accepting a
         * peering writes an upstream, a bridge and a passphrase file, and
         * until now nothing could undo any of it: an operator whose first
         * attempt went wrong was left with a broken link on this page for
         * good, unless they edited JSON on the server.
         *
         * Two clicks, because removing a link takes a network down and a
         * single button beside a status row is one slip away from doing it. */
        /* **No Remove on a link that dialled in.** It is in nobody's
         * configuration here — this server received a registration, not a
         * document — so there is nothing on this side to delete, and offering
         * the button invites a click that either does nothing or matches
         * something else by name. Removing it is the far end's to do. */
        /* **A link that dialled in can be refused, and until 0291 it could
         * not.** The button was taken off because an inbound link is in
         * nobody's configuration here — true until the offering side began
         * allocating a DMR ID and a password for it, which this console
         * writes and therefore has to be able to withdraw.
         *
         * It does not say Remove, because there is no link here to delete.
         * What it does is stop this server accepting that ID: the far end's
         * configuration is untouched and will keep dialling. */
        (l.inbound
          ? '<button class="button button--quiet link__remove" type="button" ' +
            'data-refuse="' + escapeText(l.announces || "") + '" ' +
            'data-name="' + escapeText(l.name) + '">Stop accepting</button>'
          : '<button class="button button--quiet link__remove" type="button" ' +
            'data-remove="' + escapeText(l.name) + '">Remove</button>') +
        "</div>" +
        '<dl class="link__facts">' +
        (l.inbound
          ? fact("Far end", l.far_end || "not configured")
          : '<div class="link__fact"><dt>Far end</dt><dd>' +
            '<input class="link__address" type="text" value="' +
            escapeText(l.far_end || "") + '" data-address="' + escapeText(l.name) + '">' +
            '<button class="button button--quiet link__save" type="button" ' +
            'data-save="' + escapeText(l.name) + '">Save</button>' +
            "</dd></div>") +
        fact("Protocol", l.protocol || "unknown") +
        /* Whatever the protocol calls it; the server decides, because the page
           * guessed and printed "no network ID" beside a working qsp link. */
          fact("Announces", l.announces || "not announced") +
          (l.inbound ? fact("Direction", "dialled in") : "") +
          (l.network ? fact("Network", l.network) : "") +
        /* Both directions, always. One is not evidence of the other: a link
         * that has sent thousands and received none is working perfectly on a
         * quiet network, or is unauthenticated at the far end. */
        /* **Not measured is not zero.** A link that dialled in is known
         * through the peer table, which records that it is connected and when
         * it was last heard and does not count frames each way the way an
         * outbound link's transport does. Printing 0 reads as "this link has
         * carried nothing", which is the sentence this page exists to stop
         * saying wrongly — it said it beside a link that was carrying. */
        /* **Measured, not inbound.** This asked which direction the link was
         * dialled in, which was a proxy for "does anything count it" and
         * stopped being one when the peer table began counting per peer. A
         * protocol that still does not count sets no flag and gets the dash,
         * which is the honest answer; an inbound link now reports what it is
         * carrying, so both ends of one link can be compared. */
        fact("Sent", l.measured ? String(l.sent) : "—") +
        fact("Received", l.measured ? String(l.received) : "—") +
        fact("Rejected", l.measured ? String(l.rejected) : "—") +
        fact("Last heard", l.ever_received ? idle(l.idle_seconds || 0) + " ago" : "never") +
        "</dl>" +
        '<p class="link__summary">' + escapeText(l.summary) + "</p>" +
        (l.pending_restart
          ? '<p class="link__advice">This link and the running server disagree: ' +
            escapeText(l.pending_restart) + ".</p>"
          : "") +
        (l.advice
          ? (function () {
              /* A row telling the operator to restart is the same instruction
                 the actions give, and the button belongs beside it. */
              if (l.advice.indexOf("restart QSP") >= 0) { needsRestart = true; }
              return '<p class="link__advice">' + escapeText(l.advice) + "</p>";
            })()
          : "") +
        "</div>";
    }
    list.innerHTML = html;

    if (needsRestart) {
      var note = el("links-note");
      if (note && note.hidden) {
        note.textContent = "One or more links are waiting for a restart.";
        note.hidden = false;
      }
      showRestart("links-note");
    }

    wireRefuse();
    wireAddress();

    Array.prototype.forEach.call(list.querySelectorAll("[data-remove]"), function (button) {
      button.addEventListener("click", function () {
        var name = button.getAttribute("data-remove");
        if (button.dataset.armed !== "yes") {
          button.dataset.armed = "yes";
          button.textContent = "Remove " + name + "?";
          button.classList.add("button--danger");
          /* Disarms itself. A button left in a confirming state is one an
           * operator meets later having forgotten what it was asking. */
          setTimeout(function () {
            button.dataset.armed = "no";
            button.textContent = "Remove";
            button.classList.remove("button--danger");
          }, 5000);
          return;
        }
        button.disabled = true;
        button.textContent = "Removing";
        fetch("/api/links/" + encodeURIComponent(name), {
          method: "DELETE",
          headers: { Accept: "application/json" },
          credentials: "same-origin"
        }).then(function (r) {
          return r.json().then(function (b) {
            if (!r.ok) { throw new Error(b.error || "could not remove the link"); }
            return b;
          });
        }).then(function (b) {
          if (b.orphaned_bridges && b.orphaned_bridges.length) {
            /* Bridges an operator wrote themselves are left alone even when
             * they route to the upstream being removed. Silently deleting
             * somebody's hand-written configuration because it referred to
             * something else is a surprise nobody asked for — so they are
             * named instead. */
            fail("links-note", "Removed " + name + ". These bridges still route to it and were left alone: " +
              b.orphaned_bridges.join(", "));
          } else {
            fail("links-note", "Removed " + name + "." +
              restartNote(b, "it stays open and pointed at the far end"));
          }
          load();
        }).catch(function (e) {
          button.disabled = false;
          button.textContent = "Remove";
          fail("links-note", e.message);
        });
      });
    });
  }

  /* Arming a destructive button, which both of these need: removing a link or
   * refusing one takes traffic off a network, and a single button beside a
   * status row is one slip away from doing it. */
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

  /* **One wrong character used to mean removing the link and agreeing it
   * again.** The page could create a link and remove one and could not change
   * the field most likely to be wrong. On 2026-09-08 an offer proposed the
   * OpenBridge port, the accept form wrote it, and the only way back was a
   * fresh invitation, password and access-list entry on the other server — for
   * four characters.
   *
   * Only on an outbound link: a link that dialled in has no address on this
   * side to change. */
  function wireAddress() {
    Array.prototype.forEach.call(list.querySelectorAll("[data-save]"), function (button) {
      var box = list.querySelector('[data-address="' + button.getAttribute("data-save") + '"]');
      if (!box) { return; }
      /* The field is edited in place, so polling must not overwrite what is
         being typed. */
      box.addEventListener("focus", function () { editing = true; });
      box.addEventListener("blur", function () { editing = false; });
      button.addEventListener("click", function () {
        var name = button.getAttribute("data-save");
        button.disabled = true;
        fetch("/api/links/" + encodeURIComponent(name) + "/address", {
          method: "PUT",
          headers: { "Content-Type": "application/json", Accept: "application/json" },
          credentials: "same-origin",
          body: JSON.stringify({ address: box.value.trim() })
        }).then(function (r) {
          return r.json().then(function (b) {
            if (!r.ok) { throw new Error(b.error || "could not change the address"); }
            return b;
          });
        }).then(function (b) {
          editing = false;
          fail("links-note", "The link " + name + " now reaches the far end at " +
            b.address + "." + restartNote(b, "it keeps using the old address"));
          load();
        }).catch(function (e) {
          button.disabled = false;
          fail("links-note", e.message);
        });
      });
    });
  }

  function wireRefuse() {
    Array.prototype.forEach.call(list.querySelectorAll("[data-refuse]"), function (button) {
      button.dataset.idle = "Stop accepting";
      button.addEventListener("click", function () {
        var id = button.getAttribute("data-refuse");
        var name = button.getAttribute("data-name");
        armed(button, "Stop accepting " + name + "?", function () {
          button.disabled = true;
          button.textContent = "Refusing";
          fetch("/api/links/inbound/" + encodeURIComponent(id), {
            method: "DELETE",
            headers: { Accept: "application/json" },
            credentials: "same-origin"
          }).then(function (r) {
            return r.json().then(function (b) {
              if (!r.ok) { throw new Error(b.error || "could not refuse this link"); }
              return b;
            });
          }).then(function (b) {
            /* **Everything this did and did not do.** Revoking a password that
             * was never issued, or refusing an ID a range still permits, both
             * look identical to success from here — and an operator who
             * believes a rogue network is locked out when it is not has been
             * told something worse than nothing. */
            var said = "DMR ID " + b.peer + ": ";
            said += b.refused
              ? "the registration list now refuses it"
              : "the registration list was not changed";
            said += b.password_revoked
              ? ", and its own password is revoked."
              : ", and it had no password of its own — the shared peer password still admits it.";
            if (b.reason) { said += " " + b.reason; }
            said += " A session already established stays up until it times out or QSP restarts.";
            said += restartNote(b, "this server still accepts it");
            fail("links-note", said);
            load();
          }).catch(function (e) {
            button.disabled = false;
            button.textContent = "Stop accepting";
            fail("links-note", e.message);
          });
        });
      });
    });
  }

  /* **A peering opens and closes no sockets.** Upstreams are built once at
   * startup, so an accepted link carries nothing and a removed one keeps its
   * socket until QSP restarts. config.NeedsRestart has named dmr.upstreams all
   * along; this page never asked it, and an operator was left waiting on a
   * link that did not exist yet. */
  function restartNote(body, what) {
    if (!body.needs_restart || !body.needs_restart.length) { return ""; }
    needsRestart = true;
    return " QSP has to be restarted before this takes effect — until then " +
      what + ".";
  }

  /* **The page said "restart QSP" in four places and could not do it.** An
   * upstream is built once at startup, so a link written here carries nothing
   * until the process comes back — and the answer was to find a terminal and
   * remember whether this machine is systemd or Docker Compose. An instruction
   * a page gives is one the page should be able to carry out.
   *
   * Shown only where the page has just said a restart is needed. A restart
   * button on a healthy page is an invitation to press it. */
  function showRestart(where) {
    var host = el(where);
    if (!host || host.querySelector("[data-restart]")) { return; }
    var button = document.createElement("button");
    button.className = "button button--quiet";
    button.type = "button";
    button.setAttribute("data-restart", "yes");
    button.textContent = "Restart QSP";
    button.dataset.idle = "Restart QSP";
    button.addEventListener("click", function () {
      armed(button, "Restart now? Everything drops.", function () {
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
          /* **Says what happens rather than promising an outcome.** QSP exits
           * and something else has to start it; this page cannot see whether
           * anything will, so it does not claim to. */
          fail("links-note", b.note + " This page will reconnect on its own.");
          /* The polls carry on and fail while it is down, which is what
             recovery looks like: when they succeed again, it is back. */
        }).catch(function (e) {
          button.disabled = false;
          button.textContent = "Restart QSP";
          fail("links-note", e.message);
        });
      });
    });
    host.appendChild(button);
  }

  function fact(label, value) {
    return '<div class="link__fact"><dt>' + escapeText(label) + "</dt><dd>" +
      escapeText(value) + "</dd></div>";
  }

  function load() {
    if (editing) { return; }
    fetch("/api/links", { headers: { Accept: "application/json" }, credentials: "same-origin" })
      .then(function (r) {
        if (r.status === 401) {
          hide(loading);
          hide(form);
          show(signedOut);
          return null;
        }
        return r.json();
      })
      .then(function (body) {
        if (!body) { return; }
        hide(loading);
        hide(signedOut);
        render(body.links || []);
        fillOffer(body.identity);
        show(form);
      })
      .catch(function () {
        hide(loading);
        text(count, "\u2014");
      });
  }

  /* ---- Offering and accepting a peering ------------------------------- */

  function el(id) { return document.getElementById(id); }
  function val(id) { var e = el(id); return e ? e.value.trim() : ""; }
  function num(id) { var n = parseInt(val(id), 10); return isNaN(n) ? 0 : n; }

  function fail(id, message) {
    var e = el(id);
    if (e) {
      e.textContent = message;
      e.hidden = false;
    }
    /* **After the text, because setting textContent removes the button.**
       Every message that mentions a restart is followed by the means to do
       one; every other message is not. */
    if (id === "links-note" && needsRestart) { showRestart(id); }
  }

  function post(path, body) {
    return fetch(path, {
      method: "POST",
      headers: { "Content-Type": "application/json", Accept: "application/json" },
      credentials: "same-origin",
      body: JSON.stringify(body)
    }).then(function (r) {
      return r.json().then(function (b) {
        if (!r.ok) { throw new Error((b && b.error) || "that did not work"); }
        return b;
      });
    });
  }

  /* **Fill the offer form in from what the instance already knows.**
   * Everything here was a box an operator had to fill from knowledge the
   * server had all along, and getting any of them wrong produced an error
   * naming a different field. A value already present is not typed again. */
  function fillOffer(identity) {
    if (!identity) return;
    lastIdentity = identity;
    var pairs = [
      ["offer-callsign", identity.callsign],
      ["offer-netid", identity.network_id]
    ];
    pairs.forEach(function (p) {
      var node = el(p[0]);
      if (node && !node.value && p[1]) node.value = p[1];
    });
    fillOfferAddress();
  }

  /* **The address depends on which kind of link is being offered**, and one
   * value was being used for both: a QSP link was prefilled with the
   * OpenBridge port whatever the operator chose. The fix that added a
   * QSP-specific default put it on the fallback used when the box arrives
   * empty — which this page never sends, because the box is prefilled. The
   * corrected function was unreachable and the wrong port shipped anyway.
   *
   * So the suggestion is replaced when the kind changes, unless the operator
   * has typed something of their own. */
  function fillOfferAddress() {
    var node = el("offer-address");
    if (!node || !lastIdentity) { return; }
    var suggestion = offerKind() === "qsp"
      ? lastIdentity.link_address
      : lastIdentity.address;
    if (!suggestion) { return; }
    var wasSuggested = node.value === "" ||
      node.value === lastIdentity.address ||
      node.value === lastIdentity.link_address;
    if (wasSuggested) { node.value = suggestion; }
  }

  /* **Copy buttons, because these are three hundred characters of base64.**
   * The first operator to use this page had to select a token by dragging
   * across a scrolling one-line box and hope they got the ends. Selecting the
   * whole node is what a person means by "copy this".
   *
   * navigator.clipboard needs a secure context, and a console reached over
   * plain HTTP on a LAN is not one — so the selection fallback is not
   * decoration, it is the path most operators will take. */
  function copyText(node, button) {
    var text = node.textContent || "";
    /* **The word changes as well as the icon.** A tick that turns green is
     * nothing to an operator who cannot tell the two greens apart, and this
     * one confirms a three-hundred-character token reached the clipboard —
     * which is the moment they stop checking and send it. */
    function done() {
      var word = button.querySelector(".copyblock__word");
      var was = word ? word.textContent : "";
      button.setAttribute("data-copied", "yes");
      if (word) { word.textContent = "Copied"; }
      setTimeout(function () {
        button.removeAttribute("data-copied");
        if (word) { word.textContent = was; }
      }, 1500);
    }
    if (navigator.clipboard && window.isSecureContext) {
      navigator.clipboard.writeText(text).then(done, function () { select(node); });
      return;
    }
    select(node);
    try { if (document.execCommand("copy")) { done(); } } catch (e) { /* selected, at least */ }
  }

  function select(node) {
    var range = document.createRange();
    range.selectNodeContents(node);
    var sel = window.getSelection();
    sel.removeAllRanges();
    sel.addRange(range);
  }

  Array.prototype.forEach.call(document.querySelectorAll("[data-copy]"), function (button) {
    button.addEventListener("click", function () {
      var node = el(button.getAttribute("data-copy"));
      if (node) { copyText(node, button); }
    });
  });

  /* **One page, two kinds of link, and they are not variations of each other.**
   * A QSP server links as a peer: it dials this one, and the whole of its
   * configuration is a name, an address, a DMR ID and a password. OpenBridge is
   * symmetric, has no connection, and needs an address and a network ID at both
   * ends before either carries anything.
   *
   * Showing every field for both is how this page came to ask an operator for a
   * timeslot on a link that has no endpoint to match, and for a listen address
   * on a link that never binds. So the fields follow the answer to the first
   * question. */
  function offerKind() {
    var e = el("offer-kind");
    return e ? e.value : "qsp";
  }

  function syncOfferFields() {
    var kind = offerKind();
    Array.prototype.forEach.call(
      document.querySelectorAll("[data-kind]"),
      function (node) { node.hidden = node.getAttribute("data-kind") !== kind; }
    );
    /* The same box means different things: a QSP link is dialled on the port
       this server's peers already use, and an OpenBridge peering is sent to on
       a port agreed in advance. */
    text(el("offer-address-label"), kind === "qsp" ? "They dial" : "They send to");
    /* **Two servers on one LAN address each other by LAN address.** A public
     * name leaves the LAN for the router's own address and does not come back,
     * so frames are sent and never arrive, with no rejection anywhere because
     * nothing received them. It has cost this project a morning once and an
     * offer once, and the form is what suggested the name both times. */
    text(el("offer-address-note"), kind === "qsp"
      ? "Where their server dials this one — a guess, filled in from this network's own address. If their server is on this LAN, use this machine's LAN address: a public name leaves the LAN and does not come back."
      : "Where their server sends. Your public name or address, and a UDP port you have open.");
    var addr = el("offer-address");
    if (addr) {
      addr.placeholder = kind === "qsp"
        ? "qsp.example.com:62031"
        : "qsp.example.com:62045";
    }
    fillOfferAddress();
  }

  var offerKindSelect = el("offer-kind");
  if (offerKindSelect) {
    offerKindSelect.addEventListener("change", syncOfferFields);
    syncOfferFields();
  }

  function offerLink() {
    hide(el("offer-error"));
    hide(el("offer-result"));
    post("/api/links/offer-link", {
      address: val("offer-address"),
      repeater_id: num("offer-repeater-id"),
      callsign: val("offer-callsign")
    }).then(function (b) {
      text(el("offer-token"), b.token);
      /* Shown once and never fetched again, exactly as the passphrase is. */
      text(el("offer-pass"), b.password);
      show(el("offer-result"));
      /* **Offering a QSP link writes configuration**, which offering an
       * OpenBridge peering does not: the ID is allowed to register and a
       * password is written for it. An operator who is not told that will not
       * know why their access list grew an entry. */
      var note = "This server will now let DMR ID " + b.repeater_id +
        " register, and has written a password for it alone." +
        restartNote(b, "the far end cannot register yet");
      fail("links-note", note);
      load();
    }).catch(function (e) { fail("offer-error", e.message); });
  }

  var offerButton = el("offer");
  if (offerButton) {
    offerButton.addEventListener("click", function () {
      if (offerKind() === "qsp") { offerLink(); return; }
      hide(el("offer-error"));
      hide(el("offer-result"));
      post("/api/links/offer", {
        talkgroup: num("offer-tg"),
        timeslot: num("offer-slot"),
        address: val("offer-address"),
        network_id: num("offer-netid"),
        /* **The invitation was refused for a field the page never had.**
         * peering.Invitation requires a callsign so the far end knows who is
         * asking, and it was read only from dmr.identity — which
         * config.Default() leaves empty and nothing ever asked for. So every
         * instance failed its first peering with an error naming a box that
         * did not exist, and then told the operator to check two fields that
         * were already correct. */
        callsign: val("offer-callsign")
      }).then(function (b) {
        text(el("offer-token"), b.token);
        /* Shown once and never fetched again. It is not stored anywhere the
         * console can read it back, which is the point of putting it here
         * rather than in the invitation. */
        text(el("offer-pass"), b.passphrase);
        show(el("offer-result"));
      }).catch(function (e) { fail("offer-error", e.message); });
    });
  }

  /* Accepting is two steps on purpose. The first reads the invitation and
   * shows who is asking; the second writes configuration. A peering agreed by
   * one click is one nobody read. */
  /* The token says which kind it is, so the operator is never asked. Reading
   * base64 is not a question anybody should be set. */
  function acceptKind() {
    return val("accept-token").indexOf("QSP-PEER-2.") === 0 ? "qsp" : "openbridge";
  }

  function syncAcceptFields() {
    var qsp = acceptKind() === "qsp";
    Array.prototype.forEach.call(
      document.querySelectorAll("[data-accept-kind]"),
      function (node) { node.hidden = qsp; }
    );
    /* **The password is never optional on a QSP link.** The other server
     * listens and this one dials, so there is no offer held here to look it up
     * from — which is the empty-box dead end that stopped the first peering
     * anybody attempted, and it cannot arise on this path. */
    text(el("accept-pass-note"), qsp
      ? "Sent to you separately from the invitation. A QSP link always needs it."
      : "Leave this empty when you are pasting a reply to an offer you made yourself — there is nothing to type, because your own server generated it.");
  }

  /* **A name is easier to suggest than to repair.** An operator typed "QSP
   * Test Server" into this box, which is a perfectly reasonable thing to type
   * and was also the name of their own server — and it became the name of a
   * link to somebody else's. The console cannot rename a link, so the only way
   * back was hand-editing JSON on the server.
   *
   * So the box is filled in from the invitation: the far end's display name,
   * reduced to something safe to be a file name and a routing target. The
   * operator can still type whatever they like over it. */
  function slug(text) {
    return String(text || "")
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, "-")
      .replace(/^-+|-+$/g, "")
      .slice(0, 32);
  }

  /* The token is base64url of JSON, so the page can read what it was given
     rather than waiting for a round trip to tell it. Anything unreadable is
     simply not a suggestion; the server refuses a malformed token on its own
     terms and says so better than this could. */
  function invitationFields(token) {
    try {
      var body = String(token).trim().split(".")[1];
      if (!body) { return null; }
      var b64 = body.replace(/-/g, "+").replace(/_/g, "/");
      while (b64.length % 4) { b64 += "="; }
      return JSON.parse(atob(b64));
    } catch (e) {
      return null;
    }
  }

  function suggestAcceptName() {
    var box = el("accept-name");
    if (!box || box.value.trim() !== "") { return; }
    var inv = invitationFields(val("accept-token"));
    if (!inv) { return; }
    var suggestion = slug(inv.network) || slug(inv.callsign);
    if (suggestion) { box.value = suggestion; }
  }

  var acceptToken = el("accept-token");
  if (acceptToken) {
    acceptToken.addEventListener("input", function () {
      syncAcceptFields();
      suggestAcceptName();
    });
    syncAcceptFields();
  }

  var acceptButton = el("accept");
  if (acceptButton) {
    acceptButton.addEventListener("click", function () {
      hide(el("accept-error"));
      hide(el("accept-result"));
      syncAcceptFields();
      post("/api/links/accept", request(false))
        .then(function () { /* not reached: confirm is false */ })
        .catch(function (e) {
          if (e.message.indexOf("confirmed") >= 0) {
            text(el("accept-summary"), acceptKind() === "qsp"
              ? "This will add one link to their server and write a password file. " +
                "No bridge, no timeslot and no talkgroup list: everything crosses, " +
                "and this server's own access lists decide what it keeps. Nothing " +
                "is sent to them until you agree."
              : "This will add a link and a bridge, and write a passphrase file. " +
                "Nothing is sent to the other network until you agree.");
            show(el("accept-confirm"));
            return;
          }
          fail("accept-error", e.message);
        });
    });
  }

  var acceptGo = el("accept-go");
  if (acceptGo) {
    acceptGo.addEventListener("click", function () {
      hide(el("accept-error"));
      post("/api/links/accept", request(true)).then(function (b) {
        /* **Nothing to send back when this closes our own offer.** The handler
         * used to build a reciprocal every time, so accepting a reply produced
         * another reply and the page asked the operator to send it — forever.
         * An empty one means both halves are configured and the exchange is
         * over, and the page has to say so rather than showing an empty box. */
        if (b.complete) {
          hide(el("accept-result"));
          var done = el("accept-done");
          if (done) {
            text(done, "The link to " + (b.network || b.callsign || "the other server") +
              " is written. There is nothing to send back — they allowed this " +
              "server when they made the invitation. It appears under Configured " +
              "links above." +
              restartNote(b, "it carries nothing in either direction"));
            show(done);
          }
          return;
        }
        hide(el("accept-confirm"));
        text(el("accept-reciprocal"), b.reciprocal || "");
        show(el("accept-result"));
        text(el("accept-summary"),
          "The link is written." + restartNote(b, "it carries nothing in either direction"));
        load();
      }).catch(function (e) { fail("accept-error", e.message); });
    });
  }

  function request(confirm) {
    return {
      token: val("accept-token"),
      passphrase: (el("accept-pass") || {}).value || "",
      name: val("accept-name"),
      talkgroup: num("accept-tg"),
      timeslot: 2,
      listen: val("accept-listen"),
      address: val("accept-address"),
      network_id: num("accept-netid"),
      confirm: confirm
    };
  }

  load();
  window.setInterval(load, POLL_MS);
})();
