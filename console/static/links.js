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
    if (!link.open) { return { label: "Closed", kind: "bad" }; }
    if (link.rejected > 0 && link.received === 0) {
      return { label: "Rejecting", kind: "bad" };
    }
    if (!link.ever_received) { return { label: "Nothing yet", kind: "warn" }; }
    return { label: "Carrying", kind: "good" };
  }

  function render(links) {
    text(count, links.length + (links.length === 1 ? " LINK" : " LINKS"));

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
        '<span class="link__name">' + escapeText(l.name) + "</span>" +
        '<span class="pill pill--' + st.kind + '">' + st.label + "</span>" +
        /* **A page that creates a link has to remove one.** Accepting a
         * peering writes an upstream, a bridge and a passphrase file, and
         * until now nothing could undo any of it: an operator whose first
         * attempt went wrong was left with a broken link on this page for
         * good, unless they edited JSON on the server.
         *
         * Two clicks, because removing a link takes a network down and a
         * single button beside a status row is one slip away from doing it. */
        '<button class="button button--quiet link__remove" type="button" ' +
        'data-remove="' + escapeText(l.name) + '">Remove</button>' +
        "</div>" +
        '<dl class="link__facts">' +
        fact("Far end", l.far_end || "not configured") +
        fact("Protocol", l.protocol || "unknown") +
        fact("Announces", l.network_id ? String(l.network_id) : "no network ID") +
        /* Both directions, always. One is not evidence of the other: a link
         * that has sent thousands and received none is working perfectly on a
         * quiet network, or is unauthenticated at the far end. */
        fact("Sent", String(l.sent)) +
        fact("Received", String(l.received)) +
        fact("Rejected", String(l.rejected)) +
        fact("Last heard", l.ever_received ? idle(l.idle_seconds || 0) + " ago" : "never") +
        "</dl>" +
        '<p class="link__summary">' + escapeText(l.summary) + "</p>" +
        (l.advice
          ? '<p class="link__advice">' + escapeText(l.advice) + "</p>"
          : "") +
        "</div>";
    }
    list.innerHTML = html;

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

  function fact(label, value) {
    return '<div class="link__fact"><dt>' + escapeText(label) + "</dt><dd>" +
      escapeText(value) + "</dd></div>";
  }

  function load() {
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
    var pairs = [
      ["offer-callsign", identity.callsign],
      ["offer-netid", identity.network_id],
      ["offer-address", identity.address]
    ];
    pairs.forEach(function (p) {
      var node = el(p[0]);
      if (node && !node.value && p[1]) node.value = p[1];
    });
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
    function done() {
      var was = button.textContent;
      button.textContent = "Copied";
      setTimeout(function () { button.textContent = was; }, 1500);
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

  var offerButton = el("offer");
  if (offerButton) {
    offerButton.addEventListener("click", function () {
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
  var acceptButton = el("accept");
  if (acceptButton) {
    acceptButton.addEventListener("click", function () {
      hide(el("accept-error"));
      hide(el("accept-result"));
      post("/api/links/accept", request(false))
        .then(function () { /* not reached: confirm is false */ })
        .catch(function (e) {
          if (e.message.indexOf("confirmed") >= 0) {
            text(el("accept-summary"),
              "This will add a link and a bridge, and write a passphrase file. " +
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
            text(done, "Peering with " + (b.callsign || "the other network") +
              " is complete. Both ends are configured — there is nothing " +
              "further to send. It appears under Configured links above.");
            show(done);
          }
          return;
        }
        hide(el("accept-confirm"));
        text(el("accept-reciprocal"), b.reciprocal || "");
        show(el("accept-result"));
        text(el("accept-summary"), "");
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
