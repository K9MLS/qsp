/*
 * QSP console shell behaviour.
 *
 * Two responsibilities in this phase: report the real health of the instance,
 * and maintain the event stream connection.
 *
 * Nothing here invents data. When a value is unknown the UI says so rather than
 * showing a plausible number.
 *
 * The stream protocol is described in internal/server/events.go. The important
 * contract: a "resync" event means this client's view may be incomplete and it
 * must discard local state and re-fetch. It is never ignored.
 */
(function () {
  "use strict";

  var HEALTH_POLL_MS = 15000;
  var RECONNECT_MIN_MS = 1000;
  var RECONNECT_MAX_MS = 30000;

  var PEER_POLL_MS = 10000;

  var healthPill = document.getElementById("health-pill");
  var healthText = document.getElementById("health-text");
  var streamState = document.getElementById("stream-state");

  var trafficBody = document.getElementById("traffic-body");
  var trafficNote = document.getElementById("traffic-note");

  var routingEmpty = document.getElementById("routing-empty");


  var mapBody = document.getElementById("map-body");
  var mapCount = document.getElementById("map-count");
  var peerMap = null;

  var healthBody = document.getElementById("health-body");
  var healthCount = document.getElementById("health-count");

  var callsBody = document.getElementById("calls-body");
  var callsCount = document.getElementById("calls-count");

  var peersBody = document.getElementById("peers-body");
  var peersCount = document.getElementById("peers-count");
  var knownPeerIds = {};
  var firstPeerLoad = true;

  var lastEventId = null;
  var reconnectDelay = RECONNECT_MIN_MS;
  var source = null;

  var STATUS_CLASSES = [
    "status--healthy",
    "status--degraded",
    "status--failing",
    "status--unavailable"
  ];

  function setHealth(status, label) {
    if (!healthPill || !healthText) {
      return;
    }
    STATUS_CLASSES.forEach(function (cls) {
      healthPill.classList.remove(cls);
    });
    healthPill.classList.add("status--" + status);
    healthText.textContent = label;
  }

  // renderHealth shows every subsystem's own verdict.
  //
  // The report was already being fetched every few seconds and everything but
  // the top-level status thrown away, while the console's Health link sent the
  // operator to a page of raw JSON. A subsystem that is unavailable says which
  // phase brings it, which is a roadmap the operator can read rather than a
  // fault they cannot act on.
  function renderHealth(report) {
    if (!healthBody) {
      return;
    }
    var results = (report && report.results) || [];
    if (!results.length) {
      healthBody.innerHTML =
        '<div class="empty"><h3 class="empty__title">No checks</h3>' +
        '<p class="empty__body">This instance registered no health checks.</p></div>';
      if (healthCount) {
        healthCount.textContent = "\u2014";
      }
      return;
    }

    var running = 0;
    var rows = "";
    for (var i = 0; i < results.length; i++) {
      var r = results[i];
      var status = r.status || "unavailable";
      if (status === "healthy") {
        running++;
      }
      rows +=
        "<tr><td>" + escapeText(r.name || "") + "</td>" +
        '<td><span class="status status--' + escapeText(status) + '">' +
        escapeText(status) + "</span></td>" +
        '<td class="cell--wrap">' + escapeText(r.summary || "") + "</td>" +
        '<td class="cell--wrap">' + escapeText(detailText(r.detail)) + "</td></tr>";
    }

    redraw(healthBody,
      '<div class="table-scroll" tabindex="0" role="group" aria-label="Health, scrollable"><table class="table">' +
      "<caption>Every subsystem reports its own verdict. " +
      "Unavailable names the phase that brings it.</caption>" +
      "<thead><tr>" +
      '<th scope="col">Subsystem</th><th scope="col">Status</th>' +
      '<th scope="col" class="cell--wrap">Summary</th>' +
      '<th scope="col" class="cell--wrap">Detail</th>' +
      "</tr></thead><tbody>" + rows + "</tbody></table></div>");

    if (healthCount) {
      healthCount.textContent = running + " of " + results.length + " healthy";
    }
  }

  // detailText flattens a check's detail map into one readable line.
  function detailText(detail) {
    if (!detail) {
      return "";
    }
    var parts = [];
    var keys = Object.keys(detail).sort();
    for (var i = 0; i < keys.length; i++) {
      parts.push(keys[i] + " " + detail[keys[i]]);
    }
    return parts.join(", ");
  }

  function setStream(label) {
    if (streamState) {
      streamState.textContent = "Event stream: " + label;
    }
  }

  function refreshHealth() {
    fetch("/healthz", { headers: { Accept: "application/json" } })
      .then(function (response) {
        return response.json();
      })
      .then(function (report) {
        var status = report && report.status ? report.status : "unavailable";
        setHealth(status, status);
        renderHealth(report);
      })
      .catch(function () {
        // The instance is unreachable. Say that, rather than leaving a stale
        // status on screen implying everything is fine.
        setHealth("failing", "Unreachable");
      });
  }

  /* redraw replaces what a panel shows, and keeps the reader's place in it.
   *
   * **Every refresh rebuilt every table**, every few seconds: a table scrolled
   * sideways on a phone went back to its first column, and one a keyboard
   * user had tabbed into lost its focus, before either could be read
   * (2026-10-07, F3). Nothing changed is now not redrawn at all, and what is
   * redrawn keeps each scrolling table's position and its focus. */
  function redraw(container, html) {
    /* The same markup, and still the markup this put there: something else
     * may have drawn over it since, an empty state say. */
    if (container.qspDrawn === html && container.qspFirst === container.firstChild) {
      return;
    }
    var before = container.querySelectorAll(".table-scroll");
    var places = [];
    var focused = -1;
    /* Anything else focused that has an id is found again by it: a keyboard
     * user on the stations summary was dropped to the top of the page by
     * every refresh. */
    var active = document.activeElement;
    var focusedId = active && active.id && container.contains(active) ? active.id : "";
    for (var i = 0; i < before.length; i++) {
      places.push({ left: before[i].scrollLeft, top: before[i].scrollTop });
      if (before[i] === document.activeElement || before[i].contains(document.activeElement)) {
        focused = i;
      }
    }
    container.innerHTML = html;
    container.qspDrawn = html;
    container.qspFirst = container.firstChild;
    var after = container.querySelectorAll(".table-scroll");
    for (var j = 0; j < after.length && j < places.length; j++) {
      after[j].scrollLeft = places[j].left;
      after[j].scrollTop = places[j].top;
    }
    if (focused >= 0 && after[focused]) {
      after[focused].focus({ preventScroll: true });
    } else if (focusedId) {
      var again = document.getElementById(focusedId);
      if (again && container.contains(again)) {
        again.focus({ preventScroll: true });
      }
    }
  }

  /* Escape text before it reaches innerHTML. Callsigns arrive from the
   * network and are attacker-controlled; a peer could otherwise announce a
   * callsign containing markup. */
  function escapeText(value) {
    return String(value === null || value === undefined ? "" : value)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;")
      .replace(/'/g, "&#39;");
  }

  function emptyState(title, body) {
    return (
      '<div class="empty">' +
      '<svg class="empty__icon" viewBox="0 0 32 32" fill="none" stroke="currentColor" ' +
      'stroke-width="1.5" stroke-linecap="round" aria-hidden="true" focusable="false">' +
      '<path d="M16 6v6"/><circle cx="16" cy="14" r="2.6" fill="currentColor" stroke="none"/>' +
      '<path d="M10.6 19.4a7.6 7.6 0 0 1 0-10.8M21.4 8.6a7.6 7.6 0 0 1 0 10.8"/></svg>' +
      '<h3 class="empty__title">' + escapeText(title) + "</h3>" +
      '<p class="empty__body">' + escapeText(body) + "</p></div>"
    );
  }

  function statusPill(peer) {
    var cls = peer.ready ? "status--healthy" : "status--degraded";
    var glyph = peer.ready
      ? '<path d="M2.6 6.2 5 8.6l4.4-4.8"/>'
      : '<circle cx="6" cy="6" r="4.2"/><path d="M6 3.8v2.6"/>';
    return (
      '<span class="status ' + cls + '">' +
      '<svg class="status__glyph" viewBox="0 0 12 12" fill="none" stroke="currentColor" ' +
      'stroke-width="1.6" stroke-linecap="round" aria-hidden="true" focusable="false">' +
      glyph + "</svg><span>" + escapeText(peer.state) + "</span></span>"
    );
  }

  function renderPeers(payload) {
    if (!peersBody) {
      return;
    }

    lastTraffic = payload;
    if (!payload.enabled) {
      peersCount.textContent = "disabled";
      peersBody.innerHTML = emptyState(
        "The DMR listener is not enabled",
        payload.reason || "Turn on Accept hotspots and repeaters in Network settings."
      );
      return;
    }

    var list = payload.peers || [];
    peersCount.textContent = list.length === 1 ? "1 connected" : list.length + " connected";

    if (list.length === 0) {
      peersBody.innerHTML = emptyState(
        "No peers are connected",
        "QSP is listening. Point a hotspot or repeater at it as a custom DMR " +
          "master and it will appear here."
      );
      knownPeerIds = {};
      firstPeerLoad = false;
      return;
    }

    /* **A column that is empty on every row is noise, not information.**
     * Subscription is off on most networks, and with it off every Homebrew
     * peer receives everything, so the talkgroup column reads as a dash
     * forever — while remaining the answer to "why can I not hear that
     * talkgroup" the moment subscription is on. Shown when there is something
     * to show, rather than deleted and rebuilt later. */
    var anyAttachments = false;
    for (var n = 0; n < list.length; n++) {
      if ((list[n].attachments || []).length > 0) {
        anyAttachments = true;
        break;
      }
    }

    /* The address is withheld from an unauthenticated caller, so the column is
     * withheld with it: ten dashes under a heading invite somebody to report a
     * fault that is not there. */
    var anyAddress = false;
    for (var m = 0; m < list.length; m++) {
      if (list[m].address) {
        anyAddress = true;
        break;
      }
    }

    var rows = "";
    var seen = {};
    for (var i = 0; i < list.length; i++) {
      var p = list[i];
      seen[p.id] = true;
      /* Highlight only genuinely new arrivals, and never on first paint —
       * animating the whole table on load would depict nothing real. */
      var isNew = !firstPeerLoad && !knownPeerIds[p.id];
      rows +=
        '<tr class="' + (isNew ? "is-new" : "") + '">' +
        '<td class="callsign">' + peerCallsign(p) + "</td>" +
        '<td class="mono">' + escapeText(p.id) + "</td>" +
        "<td>" + protocolPill(p) + "</td>" +
        "<td>" + statusPill(p) + "</td>" +
        '<td class="mono">' + escapeText(p.connected_for || "—") + "</td>" +
        '<td class="mono">' + escapeText(p.idle_for) + "</td>" +
        '<td class="mono">' + peerColourCode(p) + "</td>" +
        (anyAttachments
          ? '<td class="cell--wrap">' + peerAttachments(p) + "</td>"
          : "") +
        '<td class="cell--wrap">' + peerPlace(p) + "</td>" +
        (anyAddress ? '<td class="mono">' + escapeText(p.address) + "</td>" : "") +
        "</tr>";
    }

    redraw(peersBody,
      '<div class="table-scroll" tabindex="0" role="group" aria-label="Connected peers, scrollable"><table class="table">' +
      /* **Three sentences of explanation moved to the hint beside the
       * heading.** They were true and they sat above the table permanently,
       * costing a line of the panel on every load to say something an operator
       * needs once. hints.js is the console's existing answer to that: a
       * button that reveals a paragraph in the flow, chosen over a floating
       * tooltip because hover does not exist on a touch screen and is not
       * reachable from a keyboard.
       *
       * A caption is kept because a table without one is a table a screen
       * reader announces as nothing in particular, and the sign-in line is
       * kept only when it says something — telling a signed-in administrator
       * that addresses are for signed-in administrators is noise, and telling
       * a signed-out one why the column is missing is the answer to their
       * question. */
      "<caption>Peers currently registered with this master." +
      (anyAddress ? "" : " Sign in to see peer addresses.") +
      "</caption>" +
      "<thead><tr>" +
      "<th scope=\"col\">Callsign</th><th scope=\"col\">Radio ID</th>" +
      "<th scope=\"col\">Link</th>" +
      "<th scope=\"col\">State</th><th scope=\"col\">Connected</th>" +
      "<th scope=\"col\">Idle</th><th scope=\"col\">CC</th>" +
      (anyAttachments
        ? "<th scope=\"col\" class=\"cell--wrap\">Talkgroups</th>"
        : "") +
      "<th scope=\"col\" class=\"cell--wrap\">Location</th>" +
      (anyAddress ? "<th scope=\"col\">Address</th>" : "") +
      "</tr></thead><tbody>" + rows + "</tbody></table></div>");

    knownPeerIds = seen;
    firstPeerLoad = false;
  }

  /* isIPSC reports whether a peer arrived over IP Site Connect.
   *
   * Peers with no protocol field are Homebrew: an older server that predates
   * the field cannot be serving Motorola repeaters, because the two shipped
   * together. */
  function isIPSC(p) {
    return p.protocol === "ipsc";
  }

  /* protocolPill names which listener a peer belongs to.
   *
   * Without it the two are indistinguishable in one table, and a repeater with
   * no callsign and no talkgroups reads as a misconfigured hotspot. */
  function protocolPill(p) {
    if (isIPSC(p)) {
      return '<span class="pill pill--ipsc" ' +
        'title="Motorola IP Site Connect. A repeater receives every talkgroup ' +
        'and filters by its own codeplug.">IPSC</span>';
    }
    return '<span class="pill pill--homebrew" title="Homebrew / MMDVM">Homebrew</span>';
  }

  /* peerCallsign distinguishes a callsign nobody sent from one QSP does not
   * know.
   *
   * IP Site Connect carries no callsign at all, so an em dash there would be a
   * blank meaning "not applicable" wearing the costume of "not set". */
  function peerCallsign(p) {
    if (p.callsign && p.callsign_source === "registry") {
      /* Looked up, not announced, and shown differently because the two are
       * different claims. A Homebrew peer states its callsign at login; this
       * is QSP matching a radio ID against a public registry that can be
       * stale, or that describes the operator rather than the repeater. */
      return '<span class="callsign callsign--guessed" ' +
        'title="Looked up from the RadioID registry; IP Site Connect carries no callsign">' +
        escapeText(p.callsign) + "</span>";
    }
    if (p.callsign && p.callsign_source === "operator") {
      /* **Neither announced nor looked up: written down.** A repeater on a
       * private radio ID is in no registry, so this is the only way its
       * callsign ever appears — and it is the person who owns the repeater
       * saying what it is, which is a stronger claim than a public database
       * and a weaker one than the peer stating it itself. Marked, because
       * three claims shown identically are one claim with two lies in it. */
      return '<span class="callsign callsign--named" ' +
        'title="Named in this network\u2019s settings; IP Site Connect carries no callsign">' +
        escapeText(p.callsign) + "</span>";
    }
    if (p.callsign) {
      return escapeText(p.callsign);
    }
    if (isIPSC(p)) {
      return '<span class="muted" title="IP Site Connect carries no callsign, ' +
        'and this ID is not in the RadioID subscriber registry">not sent</span>';
    }
    return '<span class="muted">—</span>';
  }

  /* peerColourCode shows a repeater's colour code, learned from its own
   * traffic.
   *
   * A repeater that has never transmitted has told QSP nothing to mirror and is
   * being signed with the configured default, which is worth seeing rather than
   * guessing at. */
  function peerColourCode(p) {
    if (p.color_code) {
      return escapeText(p.color_code);
    }
    if (isIPSC(p)) {
      return '<span class="muted" title="Learned when the repeater first transmits">not heard yet</span>';
    }
    return '<span class="muted">—</span>';
  }

  /* peerAttachments lists the talkgroups a peer is receiving.
   *
   * **"Why can I not hear that talkgroup" is the most common question on any
   * DMR network**, and until now the answer was a list QSP held and did not
   * display.
   *
   * An em dash when subscription is off, because then every peer receives
   * everything and a list of talkgroups would imply a limit that does not
   * exist. Static ones are marked: the difference between "you cannot drop
   * this" and "this lapses if you stop using it" is the difference between a
   * member being confused and a member being told. */
  function peerAttachments(p) {
    var list = p.attachments || [];
    if (list.length === 0) {
      if (isIPSC(p)) {
        /* A repeater receives everything and filters by its own codeplug, so
         * QSP has nothing to list and no way to learn it. */
        return '<span class="muted" title="A repeater receives everything and filters by its codeplug">all, filtered at the repeater</span>';
      }
      return '<span class="muted">—</span>';
    }
    var parts = [];
    for (var i = 0; i < list.length; i++) {
      var a = list[i];
      parts.push(
        '<span class="tg' + (a.static ? " tg--static" : "") + '" title="' +
        (a.static ? "Configured; a disconnect does not drop it"
                  : "Attached by transmitting; lapses when unused") +
        '">' + a.talkgroup + "<span class=\"tg__slot\">TS" + a.timeslot + "</span></span>"
      );
    }
    return parts.join(" ");
  }

  // peerPlace renders what a peer says about where it is.
  //
  // **It says, and QSP does not know.** These fields are free text from a
  // station QSP does not control and nothing verifies them, so the place name
  // is shown as given and coordinates only appear when the server could parse
  // them into something plausible. A hotspot that has never been configured
  // announces nothing, and an em dash is the honest answer for it.
  function peerPlace(p) {
    var name = (p.location || "").trim();
    var located = typeof p.latitude === "number" && typeof p.longitude === "number";
    if (!name && !located) {
      return "—";
    }
    if (!located) {
      return escapeText(name);
    }
    /* A pill reading "Map" rather than the raw numbers.
     *
     * Four decimal places of latitude and longitude next to a place name is a
     * row that no longer scans: the eye stops on digits nobody reads, and the
     * one useful thing about them — that they open a map — was carried by the
     * underline alone. The coordinates go in the title, where somebody who
     * wants them can find them. */
    var coords = p.latitude.toFixed(4) + ", " + p.longitude.toFixed(4);
    var pin = mapLink(p.latitude, p.longitude, coords);
    if (!name) {
      return pin;
    }
    return escapeText(name) + " " + pin;
  }

  // mapLink turns coordinates into a link to a map.
  //
  // **QSP fetches no tiles and embeds no map**, which is ADR-0025: every slippy
  // map pulls tiles from somebody else's server, and a tile URL compiled into
  // self-hosted software is the same request from every install — the pattern
  // OpenStreetMap asks applications not to create and blocks when they do. A
  // link costs nothing until somebody clicks it, at which point it is an
  // ordinary navigation to a site they chose to visit.
  function mapLink(lat, lon, label) {
    var url =
      "https://www.openstreetmap.org/?mlat=" + encodeURIComponent(lat) +
      "&mlon=" + encodeURIComponent(lon) +
      "#map=13/" + encodeURIComponent(lat) + "/" + encodeURIComponent(lon);
    return '<a class="pill" href="' + escapeText(url) +
      '" target="_blank" rel="noopener noreferrer" title="' + escapeText(label) +
      '">Map<svg class="pill__icon" viewBox="0 0 12 12" fill="none" ' +
      'stroke="currentColor" stroke-width="1.4" stroke-linecap="round" ' +
      'aria-hidden="true" focusable="false">' +
      '<path d="M4.5 2h5.5v5.5M10 2 5 7"/>' +
      '<path d="M8 8.5V10H2V4h1.5"/></svg></a>';
  }

  // serverLabel names a server's pin. The word is in the label because a
  // different shape alone is a cue somebody has to learn.
  function serverLabel(name, self) {
    var what = self ? "this server" : "server";
    return name ? name + " (" + what + ")" : (self ? "This server" : "A linked server");
  }

  // renderMap draws the peers that announced a usable position.
  //
  // A peer with no coordinates, or coordinates the server could not parse, is
  // absent rather than placed somewhere: a pin in the wrong place is believed,
  // while a missing one prompts somebody to ask. The count says how many are
  // shown out of how many are connected, so the gap is visible rather than
  // silent.
  function renderMap(payload) {
    if (!mapBody) {
      return;
    }
    var list = (payload && payload.peers) || [];
    var points = [];
    /* Counted by what is drawn: a server that dialled this one is a peer in
       the list and a server on the map, and the line above the map says
       "servers" for every pin that is labelled one. */
    var located = 0;
    var stations = 0;
    var drawn = 0;
    for (var i = 0; i < list.length; i++) {
      var p = list[i];
      /* A peer with a link name is a QSP server that dialled this one. It
         has always been plotted, as though it were a hotspot. */
      var linked = !!p.link_name;
      if (!linked) {
        stations++;
      }
      if (typeof p.latitude !== "number" || typeof p.longitude !== "number") {
        continue;
      }
      if (linked) {
        drawn++;
      } else {
        located++;
      }
      points.push({
        lat: p.latitude,
        lon: p.longitude,
        label: linked ? serverLabel(p.network || p.callsign, false) : (p.callsign || String(p.id)),
        kind: linked ? "server" : "station"
      });
    }

    /* This server, and the servers it dialled that said where they are. Not
       counted among the peers: "3 of 4 located" is about who is connected. */
    var servers = (payload && payload.servers) || [];
    for (var s = 0; s < servers.length; s++) {
      var srv = servers[s];
      if (typeof srv.latitude !== "number" || typeof srv.longitude !== "number") {
        continue;
      }
      drawn++;
      points.push({
        lat: srv.latitude,
        lon: srv.longitude,
        label: serverLabel(srv.name || srv.callsign, !!srv.self),
        kind: "server"
      });
    }

    if (mapCount) {
      mapCount.textContent = located + " of " + stations + " located" +
        (drawn ? ", " + drawn + (drawn === 1 ? " server" : " servers") : "");
    }

    if (!points.length) {
      peerMap = null;
      // Two different problems used to render as one sentence: a hotspot
      // nobody has configured, and a hotspot configured wrongly. Only the
      // operator can tell them apart, and only if something says which.
      var refused = [];
      for (var j = 0; j < list.length; j++) {
        if (list[j].position_refused) {
          refused.push((list[j].callsign || list[j].id) + " " + list[j].position_refused);
        }
      }
      if (refused.length) {
        mapBody.innerHTML = emptyState(
          "No peer has a usable position",
          refused.join(". ") + "."
        );
      } else {
        mapBody.innerHTML = emptyState(
          "No peer has announced a position",
          "A hotspot sends its latitude and longitude when it registers. Set them " +
            "in the hotspot's configuration and it appears here."
        );
      }
      return;
    }

    if (!peerMap) {
      // The map is created on first use rather than at load, so an instance
      // whose peers never announce a position fetches no tiles at all.
      mapBody.innerHTML = '<div class="map-frame"></div>';
      peerMap = new window.QSPMap.Map(
        mapBody.querySelector(".map-frame"),
        (payload && payload.map) || {}
      );
    }
    peerMap.show(points);
  }

  // renderRefused shows what QSP is turning away.
  //
  // **Nothing surfaced this before.** Forty failed logins from one address in
  // six minutes looked, from here, like the dropped counter going up — and the
  // operator found out because the member messaged them. It stays hidden when
  // there is nothing to say, because a panel that reports "all well" every day
  // is one nobody reads on the day it matters.
  function renderRefused(payload) {
    var panel = document.getElementById("refused");
    var body = document.getElementById("refused-body");
    var count = document.getElementById("refused-count");
    if (!panel || !body) {
      return;
    }

    var list = (payload && payload.refused) || [];
    if (!list.length) {
      panel.hidden = true;
      return;
    }
    panel.hidden = false;

    var blocked = 0;
    var rows = "";
    for (var i = 0; i < list.length; i++) {
      var f = list[i];
      var state = "retrying";
      /* Only a lockout still to come. The server sent year one for "none"
       * until 0.1.336, and a lockout that has ended is not one. */
      var until = f.locked_until ? Date.parse(f.locked_until) : NaN;
      if (until > Date.now()) {
        blocked++;
        state = "ignored until " + escapeText(new Date(f.locked_until).toLocaleTimeString());
      }
      rows +=
        "<tr>" +
        '<td class="mono">' + escapeText(f.address) + "</td>" +
        '<td class="mono">' + escapeText(f.repeater_id || "—") + "</td>" +
        '<td class="cell--wrap">' + escapeText(f.reason) + "</td>" +
        '<td class="mono">' + escapeText(f.failures) + "</td>" +
        '<td class="cell--wrap">' + state + "</td>" +
        "</tr>";
    }

    count.textContent = blocked ? blocked + " ignored" : list.length + " failing";
    redraw(body,
      '<p class="panel__lede">A hotspot with a wrong password retries every ten ' +
      "seconds. After several failures QSP stops answering that address for a " +
      "while, and starts again on its own.</p>" +
      '<div class="table-scroll" tabindex="0" aria-label="Logins being refused">' +
      '<table class="table"><thead><tr>' +
      '<th scope="col">Address</th><th scope="col">Radio ID</th>' +
      '<th scope="col" class="cell--wrap">Reason</th><th scope="col">Failures</th>' +
      '<th scope="col" class="cell--wrap">State</th>' +
      "</tr></thead><tbody>" + rows + "</tbody></table></div>");
  }

  function metric(value, label, cls) {
    return (
      '<div class="metric ' + (cls || "") + '">' +
      '<div class="metric__value">' + escapeText(value) + "</div>" +
      '<div class="metric__label">' + escapeText(label) + "</div></div>"
    );
  }

  /* The drop advisory's life, which is ten seconds from when it appears.
   *
   * **The note and the amber counter expire together**, and that is the whole
   * point of this. The comment on the note below records why it exists: a
   * permanently amber IGNORED read as a fault and had nothing to explain it, so
   * the note was added as the counter's explanation. Dismissing the
   * explanation while leaving the counter amber would restore exactly that
   * problem — an unexplained amber number, which is where this started.
   *
   * So recent trouble is amber with a reason beside it, and older trouble is a
   * quiet number. The count itself is never hidden: it is still there, still
   * correct, and no longer shouting.
   *
   * It comes back whenever something else is turned away, because a second
   * event is news even if the first was read and dismissed.
   */
  var NOTE_LIFETIME_MS = 10000;
  /* **Two advisories live this way**: IGNORED's, and since 0.1.349
   * COLLISIONS's, which was amber for as long as the server ran — 748 of them
   * on 2026-10-27, three key-overs the day before, explained nowhere on the
   * page. Each has its own lifetime, because one being read says nothing
   * about the other. */
  var dropNote = { cls: "inline-note--drops", seen: -1, dismissed: false, timer: null };
  var collisionNote = { cls: "inline-note--collisions", seen: -1, dismissed: false, timer: null };

  /* dismissNote starts the dissolve and removes the element after it.
   *
   * The element is removed rather than left hidden, so that a panel rendered
   * again while the animation is running does not find a stale note to
   * re-animate. */
  function dismissNote(state) {
    state.timer = null;
    state.dismissed = true;

    var note = trafficBody
      ? trafficBody.querySelector("." + state.cls)
      : null;
    if (!note) {
      return;
    }
    note.classList.add("is-dissolving");

    /* **A timer rather than the animationend event.** Reduced motion replaces
     * the animation with `display: none`, and an animation that never runs
     * never ends — so waiting for the event would leave the element in the
     * document for ever on exactly the machines that asked for less movement.
     */
    window.setTimeout(function () {
      if (note.parentNode) {
        note.parentNode.removeChild(note);
      }
      /* Re-render so the counter loses its amber in the same moment the
       * reason goes, rather than on whatever refresh happens next. */
      if (lastTraffic) {
        renderTraffic(lastTraffic);
      }
    }, 600);
  }

  /* noteFor arms a note's dismissal and reports whether it should be drawn
   * at all. */
  function noteFor(state, count) {
    if (count > state.seen) {
      /* Something new happened: news, even if the previous note was read and
       * dismissed. */
      state.seen = count;
      state.dismissed = false;
      if (state.timer !== null) {
        window.clearTimeout(state.timer);
      }
      state.timer = window.setTimeout(function () {
        dismissNote(state);
      }, NOTE_LIFETIME_MS);
      return true;
    }
    return !state.dismissed;
  }

  function noteDropsFor(ignored) {
    return noteFor(dropNote, ignored);
  }

  /* A moment as the note says it: the time alone today, with the date on any
   * other day, because "last at 4:36 PM" read the next morning is the
   * misreading the time is there to prevent. */
  function whenSaid(iso) {
    var d = new Date(iso);
    var now = new Date();
    var time = { hour: "numeric", minute: "2-digit" };
    if (d.toDateString() === now.toDateString()) {
      return d.toLocaleTimeString([], time);
    }
    return d.toLocaleString([], {
      month: "short", day: "numeric", hour: "numeric", minute: "2-digit"
    });
  }

  /* collisionLines says what COLLISIONS counted: one line per thing that
   * happened, newest first, at most two.
   *
   * **A run is the unit, not a frame.** The server joins a key-over's fifty
   * frames a second into one note, and this joins notes with the same
   * sentence, so "3 key-overs" is three times somebody talked over Zello,
   * which is what an operator can do something about. Two lines, because the
   * question is "what was it" and the latest answers it; the counter beside
   * it is still the whole number. */
  function collisionLines(notes, peerList) {
    /* **A callsign where the console knows one.** The server names a peer by
     * its ID because that is what routing has; the operator reading this
     * knows "K9MLS" and has to look "3121380" up. Only peers registered now
     * are known here, so one that has gone keeps its number, which is still
     * true. */
    var callsigns = {};
    (peerList || []).forEach(function (p) {
      if (p && p.id && p.callsign) callsigns[p.id] = p.callsign;
    });
    function named(summary) {
      return escapeText(summary).replace(/\bpeer[ ](\d+)/g, function (all, id) {
        return callsigns[id] ? escapeText(callsigns[id]) + " (" + id + ")" : all;
      });
    }
    var groups = [];
    var bySummary = {};
    (notes || []).forEach(function (n) {
      var g = bySummary[n.summary];
      if (!g) {
        g = bySummary[n.summary] = { summary: n.summary, keyOver: n.key_over, n: 0, at: n.at };
        groups.push(g);
      }
      g.n += 1;
      /* Compared as times: Go writes fractions of a second only when there
       * are some, so two of its timestamps do not sort as strings. */
      if (Date.parse(n.at) > Date.parse(g.at)) g.at = n.at;
    });
    groups.sort(function (a, b) { return Date.parse(b.at) - Date.parse(a.at); });
    return groups.slice(0, 2).map(function (g) {
      var noun = g.keyOver
        ? (g.n === 1 ? "key-over" : "key-overs")
        : (g.n === 1 ? "refusal" : "refusals");
      return (
        '<span class="inline-note__when">' +
        g.n + " " + noun + (g.n === 1 ? " at " : ", last at ") +
        escapeText(whenSaid(g.at)) + "</span> \u2014 " + named(g.summary) + "."
      );
    });
  }

  /* Seconds as a person says them: "12s", "4m", "2h 14m", "3d 2h". The
   * second unit is dropped once the first is in days, and never shown as
   * zero, so "2h" is not written "2h 0m". */
  function spoken(seconds) {
    var s = Math.max(0, Math.round(seconds || 0));
    if (s < 60) { return s + "s"; }
    var m = Math.floor(s / 60);
    if (m < 60) { return m + "m"; }
    var h = Math.floor(m / 60);
    if (h < 24) { return h + "h" + (m % 60 ? " " + (m % 60) + "m" : ""); }
    var d = Math.floor(h / 24);
    return d + "d" + (h % 24 ? " " + (h % 24) + "h" : "");
  }

  /* **The P25 stations, folded to one line.** Until 0.1.344 every gateway
   * and repeater was a sentence in one paragraph, joined with full stops: two
   * repeaters filled four lines of the Overview with figures read perhaps one
   * visit in twenty. Folded, the line answers what is asked on every visit —
   * is everything linked, is anything wrong, who spoke last — and opening it
   * gives one row per station with the figures in columns.
   *
   * **Nothing that needs doing is folded away.** A station with something
   * wrong is counted in an amber chip on the line itself, and the sentence
   * saying what to change is drawn below it whether the section is open or
   * not: hiding the reason audio goes one way would make the fold the fault.
   *
   * **Its state is the operator's, not the refresh's.** The panel is redrawn
   * on every refresh, and a section that closed itself every few seconds
   * would be worse than none. Whether it is open lives here and in the
   * browser's storage, and the markup is drawn to match. */
  var STATIONS_KEY = "qsp.overview.p25-stations";
  var stationsOpen = (function () {
    try {
      return window.localStorage.getItem(STATIONS_KEY) === "open";
    } catch (e) {
      return false;
    }
  })();

  /* The section is redrawn from markup, so the listener is on the panel and
   * catches toggle on the way down: the event does not bubble. */
  if (trafficBody) {
    trafficBody.addEventListener("toggle", function (e) {
      if (!e.target || e.target.id !== "p25-stations") { return; }
      stationsOpen = e.target.open;
      try {
        window.localStorage.setItem(STATIONS_KEY, stationsOpen ? "open" : "closed");
      } catch (err) {
        /* Storage refused: it stays as chosen until the page is reloaded. */
      }
    }, true);
  }

  function chip(text, cls) {
    return '<span class="pill stations__chip' + (cls ? " " + cls : "") + '">' + text + "</span>";
  }

  /* One repeater as a table row, and whatever about it needs saying. */
  function repeaterRow(r) {
    var name = r.type ? escapeText(r.type) : "Motorola repeater";
    if (r.site) { name += " · site " + r.site; }
    var where = r.router ? escapeText(r.router) : "";

    var link;
    if (!r.up) {
      link = '<span class="pill pill--warn">opening</span>';
    } else if (r.rtrt_disabled) {
      link = '<span class="pill pill--warn">up · RT/RT off</span>';
    } else {
      link = '<span class="pill pill--good">up' +
        (r.up_for_seconds ? " " + spoken(r.up_for_seconds) : "") + "</span>";
    }

    /* Zero is not a talkgroup or a radio, so neither is printed until a
     * transmission has said who it was. */
    var heard = "—";
    if (r.transmitting) {
      heard = '<span class="stations__live"><span class="live-dot" aria-hidden="true"></span>' +
        (r.source_id ? r.source_id + " · " : "") + "transmitting now</span>";
    } else if (r.source_id) {
      heard = r.source_id + (r.talkgroup ? " · TG " + r.talkgroup : "") +
        (r.heard ? " · " + spoken(r.last_heard_ago_seconds) + " ago" : "");
    }

    return "<tr>" +
      '<th scope="row"><span class="stations__name">' + name + "</span>" +
      (where ? '<span class="stations__where">' + where + "</span>" : "") + "</th>" +
      "<td>" + link + "</td>" +
      "<td>" + heard + "</td>" +
      '<td class="num">' + (r.calls || 0) + "</td>" +
      '<td class="num">' + (r.frames || 0) + " · " + (r.relayed || 0) + " · " + (r.sent || 0) + "</td>" +
      "</tr>";
  }

  /* One gateway as a row. A gateway has no calls of its own to count and
   * carries nothing onward, so those cells say so rather than show zero. */
  function gatewayRow(g) {
    var heard = "—";
    if (g.source_id) {
      heard = g.source_id + (g.talkgroup ? " · TG " + g.talkgroup : "");
    }
    return "<tr>" +
      '<th scope="row"><span class="stations__name">' +
      escapeText(g.callsign || "an unnamed gateway") + "</span>" +
      '<span class="stations__where">P25 gateway' +
      (g.address ? " · " + escapeText(g.address) : "") + "</span></th>" +
      '<td><span class="pill pill--good">polled ' + spoken(g.last_poll_ago_seconds) + " ago</span></td>" +
      "<td>" + heard + "</td>" +
      '<td class="num">—</td>' +
      '<td class="num">' + (g.frames || 0) + " · — · " + (g.sent || 0) + "</td>" +
      "</tr>";
  }

  function stationsMarkup(p25) {
    var reps = p25.repeaters || [];
    var gws = p25.gateways_off ? [] : (p25.gateways || []);
    var chips = [];
    var warnings = [];
    var rows = "";

    if (reps.length > 0) {
      /* The count first, because "is everything linked" is the question an
       * administrator opens this page with. */
      var linked = reps.filter(function (r) { return r.up; }).length;
      chips.push(chip('<span class="stations__dot" aria-hidden="true"></span>' +
        linked + " of " + reps.length + " " +
        (reps.length === 1 ? "repeater" : "repeaters") + " linked",
        linked === reps.length ? "pill--good" : "pill--warn"));
    }
    if (!p25.gateways_off) {
      /* Not a fault: a reflector nobody has linked to is one waiting. */
      chips.push(gws.length === 0 ? chip("No gateways linked") :
        chip(gws.length + (gws.length === 1 ? " gateway" : " gateways") + " linked", "pill--good"));
    }

    var trouble = 0;
    reps.forEach(function (r) {
      rows += repeaterRow(r);
      if (!r.up || r.rtrt_disabled) { trouble++; }
      /* Found on 2026-10-22 with a packet capture, after an hour of looking
       * at a link that reported itself healthy: a repeater whose calls are
       * marked RT/RT disabled is linked and heard, and no other repeater
       * transmits a word it sends. */
      if (r.rtrt_disabled) {
        warnings.push("The Motorola repeater" +
          (r.router ? " through " + escapeText(r.router) : (r.site ? " at site " + r.site : "")) +
          " marks its calls RT/RT disabled, so other repeaters will not " +
          "transmit them. Set RT/RT Configuration to ENABLED in its codeplug.");
      }
    });
    gws.forEach(function (g) { rows += gatewayRow(g); });

    if (trouble > 0) {
      chips.push(chip(trouble + (trouble === 1 ? " needs" : " need") + " attention", "pill--warn"));
    }

    /* Who spoke last, from the repeaters, which are the stations that say
     * when. A transmission in progress beats any that has finished. */
    var latest = null;
    reps.forEach(function (r) {
      if (!r.source_id) { return; }
      if (r.transmitting) {
        if (!latest || !latest.transmitting) { latest = r; }
        return;
      }
      if (!r.heard || (latest && latest.transmitting)) { return; }
      if (!latest || r.last_heard_ago_seconds < latest.last_heard_ago_seconds) { latest = r; }
    });
    if (latest) {
      chips.push(latest.transmitting
        ? chip('<span class="live-dot" aria-hidden="true"></span>' +
          latest.source_id + " transmitting now", "pill--good")
        : chip("Last heard " + latest.source_id + " · " + spoken(latest.last_heard_ago_seconds) + " ago"));
    }

    /* A refusal is named rather than counted: the IPSC listener reached
     * 2,144 unnamed refusals before anybody could say which repeater. */
    if ((p25.refused || 0) > 0 && p25.refused_last) {
      chips.push(chip("Last refused " + escapeText(p25.refused_last), "pill--warn"));
    }
    /* One call at a time crosses between gateways and repeaters. Losing the
     * floor is contention, not a fault, so it is counted quietly. */
    if ((p25.held_calls || 0) > 0 || (p25.held_frames || 0) > 0) {
      var lost = [];
      if (p25.held_calls) {
        lost.push(p25.held_calls + (p25.held_calls === 1 ? " call" : " calls"));
      }
      if (p25.held_frames) {
        lost.push(p25.held_frames + (p25.held_frames === 1 ? " gateway frame" : " gateway frames"));
      }
      chips.push('<span class="pill stations__chip" title="Not carried because another station was talking">' +
        "Held " + lost.join(" and ") + "</span>");
    }

    if (chips.length === 0) {
      return "";
    }

    var html = '<details class="stations" id="p25-stations"' + (stationsOpen ? " open" : "") + ">" +
      '<summary class="stations__summary" id="p25-stations-summary">' +
      '<svg class="stations__chevron" viewBox="0 0 16 16" fill="none" stroke="currentColor" ' +
      'stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
      '<path d="M6 3.5 10.5 8 6 12.5"/></svg>' +
      '<span class="stations__label">Stations</span>' + chips.join("") + "</summary>";
    if (rows) {
      html += '<div class="stations__body"><div class="table-scroll" tabindex="0" ' +
        'aria-label="P25 gateways and repeaters"><table class="table stations__table"><thead><tr>' +
        '<th scope="col">Station</th><th scope="col">Link</th><th scope="col">Last heard</th>' +
        '<th scope="col" class="num">Calls</th>' +
        '<th scope="col" class="num" title="Voice frames heard from it, carried onward, and sent to it">' +
        "Heard · carried · sent</th>" +
        "</tr></thead><tbody>" + rows + "</tbody></table></div></div>";
    } else {
      html += '<p class="stations__empty">No stations to list yet.</p>';
    }
    html += "</details>";
    warnings.forEach(function (w) {
      html += '<p class="inline-note" data-p25-warning>' + w + "</p>";
    });
    return html;
  }

  var lastTraffic = null;

  /* The P25 row and the lines under it, as markup.
   *
   * **Its own function since P25 could be the only mode.** It was drawn
   * inside the DMR branch, so a server running P25 and no DMR showed "the
   * DMR listener is not enabled" where its gateways and repeaters should
   * have been. Both branches call this now. */
  function p25Markup(p25) {
    var html = "";
    var p25In = (p25.polls || 0) + (p25.voice_frames || 0) + (p25.unparsed || 0);
    html +=
      '<div class="metrics-row">' +
      '<div class="metrics-row__mode">P25</div>' +
      metric(p25In, "datagrams in") +
      metric(p25.voice_frames || 0, "voice frames",
        (p25.voice_frames || 0) === 0 ? "metric--muted" : "") +
      metric(p25.refused || 0, "refused",
        (p25.refused || 0) > 0 ? "metric--warn" : "metric--muted") +
      /* Unrecognised datagrams are expected in principle and are not a
       * fault — three captures are not the whole protocol — so this is
       * muted rather than amber, matching the health check's reasoning.
       * Measured at zero against a live P25Gateway. */
      metric(p25.unparsed || 0, "unparsed", "metric--muted") +
      "</div>";

    html += stationsMarkup(p25);
    return html;
  }

  function renderTraffic(payload) {
    if (!trafficBody) {
      return;
    }
    lastTraffic = payload;
    if (!payload.enabled) {
      /* A server running P25 alone has traffic to show and no DMR row to
       * show it under. */
      var only = payload.traffic && payload.traffic.p25;
      if (only) {
        trafficNote.textContent = "since start";
        redraw(trafficBody, p25Markup(only));
        return;
      }
      trafficNote.textContent = "disabled";
      redraw(trafficBody, emptyState(
        "The DMR listener is not enabled",
        payload.reason || "Turn on Accept hotspots and repeaters in Network settings."
      ));
      return;
    }

    var t = payload.traffic || {};
    var ipsc = t.ipsc || null;
    var inCount = t.datagrams_in || 0;

    /* **One network, one question.** The panel carried ten figures and an
     * operator glancing at it asks four things: is anything reaching me, is
     * audio moving, is something being turned away, and why was a transmission
     * refused. Datagrams out and answered both shadow datagrams in; forwarded
     * is a permanent zero without a bridge; and a text burst count is not a
     * message count, so 22 answers nothing anybody asked.
     *
     * The two listeners are summed here rather than in the payload. The API
     * keeps them apart, so nothing is lost for debugging and /healthz still
     * reports each socket; only the glance is simplified. Which protocol a
     * peer arrived on is in the table below, where it belongs. */
    var frames = (t.frames_accepted || 0) + (ipsc ? ipsc.voice_frames || 0 : 0);
    var ignored = (t.ignored || 0) + (ipsc ? ipsc.ignored || 0 : 0);

    /* **Decided before the markup is built**, because the counter's colour and
     * the note's presence are one decision and asking twice would let them
     * disagree — an amber number with no reason beside it, or a reason beside
     * a muted number. */
    var showDrops = ignored > 0 && noteDropsFor(ignored);
    /* **The same rule for COLLISIONS**, and one condition more: there must be
     * something to say. A public view has the number and not the sentences
     * (they are an operator's), so there it is a quiet number, which is all
     * it can honestly be. */
    var collisions = t.collisions || 0;
    var collided = collisions > 0 ? collisionLines(t.recent_collisions, payload.peers) : [];
    var showCollisions = collided.length > 0 && noteFor(collisionNote, collisions);

    var p25 = t.p25;
    var peers = (payload.peers || []).length;

    trafficNote.textContent = "since start";
    /* Built whole and drawn once, through redraw: assembled in the panel with
     * `innerHTML +=` it was parsed four times a refresh, and every refresh
     * replaced the stations section under an operator's pointer even when
     * nothing in it had changed. */
    var html =
      /* **Both groups are labelled or neither is.** With only P25 named, the
       * row above it read as a total for the server rather than as one
       * listener's counters — which is the same misreading the separate
       * objects in the payload exist to prevent.
       *
       * The label is the first cell of the row rather than a band above it:
       * two bands were ~44px of chrome, and they separated the two rows so
       * that comparing DMR with P25 — the only reason to show both — took
       * scanning across a divider. */
      '<div class="metrics-row">' +
      '<div class="metrics-row__mode">DMR</div>' +
      metric(inCount, "datagrams in") +
      metric(frames, "voice frames", frames === 0 ? "metric--muted" : "") +
      metric(collisions, "collisions",
        showCollisions ? "metric--warn" : "metric--muted") +
      /* **Only traffic nobody asked for gets amber.** A refusal QSP answered is
       * the protocol working — a keepalive from a peer that has not registered
       * is answered so it logs in again — and counting that beside a stray port
       * scan produced one permanently amber number that looked like a fault and
       * was not. The reasons are below. */
      /* **Amber only while the reason is on screen.** See noteDropsFor: an
       * amber number that outlives its explanation is the fault this note was
       * built to fix, so the two end together. */
      metric(
        ignored,
        "ignored",
        ignored > 0 && showDrops ? "metric--warn" : "metric--muted"
      ) +
      "</div>";

    /* **Only when something was actually turned away, and then one line.**
     *
     * The first version of this listed every drop note verbatim: three journal
     * lines, 120 characters each, taking a third of the panel — and all three
     * were *answered*, which is the protocol working. It appeared after every
     * restart, permanently, while IGNORED read 0 and had nothing to explain.
     *
     * The counter that raises the question is `ignored`, so this explains that
     * counter and nothing else. When it is zero there is no question and the
     * panel says nothing.
     *
     * Grouped by source, because eighteen frames of one transmission is one
     * fact. The time is the part that matters: "25 ignored" read at breakfast
     * looks like a morning event, and those 25 were one transmission at 23:29
     * the night before. A cumulative counter with no time axis invites exactly
     * that reading.
     *
     * Signed-in only — recent_drops is withheld from unauthenticated callers
     * because the reasons name addresses — so this is absent on a public view
     * rather than empty, which is correct. */
    /* Before IGNORED's note, in the order of the counters. Each line is its
     * own paragraph, so two things that happened read as two. */
    if (showCollisions) {
      html +=
        '<div class="inline-note inline-note--collisions">' +
        collided.map(function (line) {
          return '<p class="inline-note__line">' + line + "</p>";
        }).join("") +
        "</div>";
    }

    if (ignored > 0 && showDrops) {
      var silent = (t.recent_drops || []).filter(function (d) {
        return !d.answered;
      });
      var bySource = {};
      silent.forEach(function (d) {
        var key = d.from || "an unknown source";
        if (!bySource[key]) bySource[key] = { n: 0, at: d.at };
        bySource[key].n += 1;
        if (d.at > bySource[key].at) bySource[key].at = d.at;
      });
      var lines = Object.keys(bySource).map(function (key) {
        var g = bySource[key];
        return (
          g.n +
          (g.n === 1 ? " datagram from " : " datagrams from ") +
          escapeText(key) +
          " turned away, last at " +
          escapeText(new Date(g.at).toLocaleTimeString())
        );
      });
      if (lines.length > 0) {
        html +=
          '<p class="inline-note inline-note--drops">' +
          lines.join(". ") +
          ".</p>";
      }
    }

    /* **Placed under the DMR row, and named for it.** It was the last thing
     * in the box when DMR was the only row; once P25 had lines of its own
     * beneath, it read as a remark about them, directly under a P25 voice
     * frame count that contradicted it.
     *
     * The case that cost an evening: a peer connected and sending keepalives,
     * whose voice frames never arrive. The peer table looks healthy and Last
     * heard looks empty, which is indistinguishable from nobody talking.
     *
     * **And that is the point — it really is indistinguishable.** A hotspot
     * sends the same keepalives whether its owner is misconfigured or simply
     * not talking, so nothing here can tell the two apart. The earlier wording
     * picked one and stated it: "its transmissions are not reaching QSP". On a
     * quiet club network, and for several minutes after every restart, that is
     * an alarm about a fault that does not exist, and an operator who learns to
     * disbelieve one warning stops reading all of them.
     *
     * So the hint names both possibilities and asserts neither. The threshold
     * is a few minutes of keepalives rather than one, which keeps it out of the
     * window after a restart while still appearing early enough to help
     * somebody setting a hotspot up for the first time. */
    /* **frames counts both listeners**, which is what makes this hint safe to
     * show. Before it did, a network whose only traffic was Motorola repeaters
     * saw this note while working perfectly, and it named a hotspot that had
     * nothing to do with anything. A hint that is confidently wrong is worse
     * than no hint: an operator who learns to disbelieve one warning stops
     * reading all of them. */
    if (peers > 0 && inCount > 30 && frames === 0) {
      html +=
        '<p class="inline-note inline-note--neutral">DMR: no voice frames yet, only keepalives. ' +
        "If nobody has transmitted, that is exactly what this should look like. " +
        "If somebody has, the sending side is probably not routing a talkgroup " +
        "to this network \u2014 check there.</p>";
    }

    /* **P25, which had nowhere to be shown until 2026-09-12.**
     *
     * The listener computed a gateway's talkgroup and the last radio heard
     * through it on every voice frame since it was built, and no page read
     * either: Gateways() had exactly one caller in the tree, the health check.
     * An operator had to curl /healthz to find out whether his own radio had
     * been heard, which is how this was found.
     *
     * **Four columns in the same order as DMR**, because a row that does not
     * line up with the row above it cannot be read across. The grid declares
     * an explicit column count for exactly that reason: it was `auto-fit`,
     * so four metrics gave four columns and three gave three wider ones, and
     * the two rows did not align. Corrected 2026-09-12 from a screenshot.
     *
     * POLLS was a fifth metric and is gone. A five-second keepalive is
     * plumbing, not traffic; "polled 3s ago" on the gateway line is the form
     * of it an operator can act on, and its total is in /healthz.
     *
     * Absent rather than empty when P25 is off, matching IPSC: the payload
     * omits the object, so a server not running P25 says nothing about it
     * instead of showing zeroes. */
    if (p25) {
      html += p25Markup(p25);
    }
    redraw(trafficBody, html);

  }

  /* showSlot is false when no call in the list has a timeslot, which is a
   * network running P25 alone: the column would be a dash on every row. */
  function callRow(call, live, showSlot) {
    /* The callsign when QSP knows it, with the number kept beside it: the
     * number is what somebody programmed into a radio and what they will search
     * for, and a callsign alone would make a list nobody can cross-reference. */
    var name = call.source_name
      ? '<span class="callsign">' + escapeText(call.source_name) + "</span>" +
        ' <span class="mono muted">' + escapeText(call.source) + "</span>"
      : '<span class="callsign">' + escapeText(call.source) + "</span>";
    var who = live
      ? '<span class="live-dot" aria-hidden="true"></span> ' + name
      : name;

    /* A P25 call shorter than one voice unit ends before it has said who it
     * is. Zero is "not said", and printing it as an ID would name nobody. */
    if (!call.source) {
      name = '<span class="muted">unknown</span>';
      who = live ? '<span class="live-dot" aria-hidden="true"></span> ' + name : name;
    }

    var kind = call.group
      ? (call.target ? "TG " + escapeText(call.target) : "\u2014")
      : "DM " + escapeText(call.target) +
        (call.target_name ? ' <span class="muted">' + escapeText(call.target_name) + "</span>" : "");
    /* Where a P25 call came into QSP: which repeater, or which hotspot. */
    if (call.via) {
      kind += ' <span class="muted">via ' + escapeText(call.via) + "</span>";
    }

    /* **A data burst is not a failed transmission.** A text message is a
     * handful of one-frame bursts, each with its own stream ID, and marking
     * every one "no terminator" was a false alarm — a single burst has no
     * terminator and is not meant to. Worse, fifty of them buried the voice
     * traffic this list exists to show.
     *
     * Voice is what "no terminator" means something about, so the warning is
     * kept for voice and data is labelled for what it is. */
    var flags = "";
    if (!call.voice) {
      flags = ' <span class="tag tag--data" title="data rather than voice: a text ' +
        'message, position report, or registration">data</span>';
    } else if (call.lost) {
      flags = ' <span class="tag tag--lost" title="ended without a terminator">no terminator</span>';
    }
    /* One call at a time is carried between P25 gateways and repeaters. This
     * one was heard and lost the turn. */
    if (call.not_carried) {
      flags += ' <span class="tag tag--data" title="heard, and not relayed: ' +
        'another station was talking">not carried</span>';
    }
    /* Named only on a server running both modes; the server leaves it out
     * otherwise. */
    var mode = call.mode
      ? ' <span class="tag tag--data">' + escapeText(call.mode) + "</span>"
      : "";
    return (
      '<tr data-call="' + escapeText(callKey(call, live)) + '">' +
      "<td>" + who + mode + "</td>" +
      '<td class="mono">' + kind + "</td>" +
      (showSlot
        ? '<td class="mono">' + (call.timeslot ? "TS" + escapeText(call.timeslot) : "\u2014") + "</td>"
        : "") +
      '<td class="mono">' + escapeText(call.duration) + "</td>" +
      '<td class="mono">' + escapeText(call.frames) + "</td>" +
      '<td class="mono">' + escapeText(live ? "now" : call.ago || "—") + flags + "</td>" +
      "</tr>"
    );
  }

  // callsCaption says what this instance does with what it hears.
  //
  // It was static text reading "Nothing is forwarded", which was true when
  // written and became a lie the day the master learned to repeat. An
  // instance that had been relaying for eleven hours still displayed it.
  /* Which call a row is, across refreshes. A finished call is fixed by when
   * it ended; a live one has not ended, so it is the talker and the target
   * until it does, and then becomes a different row, which is what it is. */
  function callKey(call, live) {
    return [call.source, call.target, call.group ? "g" : "p", call.mode || "",
      call.via || "", live ? "live" : (call.ended_at || call.ago || "")].join("|");
  }

  /* **Where the reader is in Last heard, by call rather than by pixel.**
   *
   * The window holds fifty calls and refreshes every few seconds with the
   * newest at the top. Put back at the same scroll offset, the list would
   * slide under somebody reading call thirty by one row for every call that
   * arrived, and the line they were on would walk away from them. So the
   * call at the top of their view is noted before the redraw and put back in
   * the same place after it. At the very top, the top is kept, so the newest
   * call is what they see arrive. */
  function callsPlace(win) {
    if (!win || win.scrollTop <= 1) {
      return { top: true };
    }
    var head = win.querySelector("thead");
    var edge = win.getBoundingClientRect().top + (head ? head.getBoundingClientRect().height : 0);
    var rows = win.querySelectorAll("tbody tr");
    for (var i = 0; i < rows.length; i++) {
      var box = rows[i].getBoundingClientRect();
      if (box.bottom > edge + 1) {
        return { key: rows[i].getAttribute("data-call"), offset: box.top - edge };
      }
    }
    return { top: false };
  }

  function restoreCallsPlace(win, place) {
    /* At the top there is no call to hold: redraw has already put the
     * window back at zero, where the newest call arrives. */
    if (!win || !place || !place.key) { return; }
    var rows = win.querySelectorAll("tbody tr");
    for (var i = 0; i < rows.length; i++) {
      if (rows[i].getAttribute("data-call") === place.key) {
        var head = win.querySelector("thead");
        var edge = win.getBoundingClientRect().top + (head ? head.getBoundingClientRect().height : 0);
        win.scrollTop += rows[i].getBoundingClientRect().top - edge - place.offset;
        return;
      }
    }
    /* The call has aged out of the fifty: redraw's own offset stands. */
  }

  /* Whether there is more below, which the fade at the window's foot says.
   * Set on every scroll and after every redraw, so the fade is gone the
   * moment the last row is in view and back when there is something under
   * it again. */
  function markMore(win) {
    if (!win) { return; }
    var more = win.scrollTop + win.clientHeight < win.scrollHeight - 2;
    if (win.getAttribute("data-more") !== String(more)) {
      win.setAttribute("data-more", String(more));
    }
    /* "Scroll for more" is true while there is more below, and not at the
     * end, where the same words would send somebody looking for rows that
     * are not there. */
    var hint = callsBody ? callsBody.querySelector(".calls__more") : null;
    if (hint) {
      hint.hidden = !more;
    }
  }

  if (callsBody) {
    /* scroll does not bubble; the window is replaced on every redraw, so the
     * listener is on the panel and catches it on the way down. */
    callsBody.addEventListener("scroll", function (e) {
      if (e.target && e.target.classList && e.target.classList.contains("calls-window")) {
        markMore(e.target);
      }
    }, true);
  }

  function callsCaption(payload) {
    if (payload && payload.forwarding) {
      return "Transmissions observed by this master, and relayed to other peers " +
        "on the same talkgroup.";
    }
    return "Transmissions observed by this master. Nothing is forwarded.";
  }

  // showRouting reveals the forwarding-is-off notice only when it is true.
  //
  // The notice was unconditional markup. Hiding it when forwarding is on
  // matters more than it sounds: an operator reading "Forwarding is off" on a
  // working master will go looking for a fault that is not there.
  function showRouting(payload) {
    if (!routingEmpty) {
      return;
    }
    var off = !payload || !payload.enabled || !payload.forwarding;
    routingEmpty.style.display = off ? "" : "none";
  }

  function renderCalls(payload) {
    if (!callsBody) {
      return;
    }
    lastTraffic = payload;
    var active = payload.active_calls || [];
    var recent = payload.recent_calls || [];

    /* Disabled means no listener at all. A server running P25 alone has no
     * DMR listener and still has calls, so it is asked before this is said. */
    var p25Running = !!(payload.traffic && payload.traffic.p25);
    if (!payload.enabled && !p25Running) {
      callsCount.textContent = "disabled";
      callsBody.innerHTML = emptyState(
        "The DMR listener is not enabled",
        "No transmissions can be observed until peers can connect."
      );
      return;
    }

    if (active.length === 0 && recent.length === 0) {
      callsCount.textContent = "quiet";
      callsBody.innerHTML = emptyState(
        "Nothing heard yet",
        "When a connected peer keys up, the transmission appears here."
      );
      return;
    }

    callsCount.textContent =
      active.length > 0
        ? active.length === 1
          ? "1 transmitting"
          : active.length + " transmitting"
        : recent.length + " recent";

    var rows = "";
    var i;
    var showSlot = active.concat(recent).some(function (c) { return !!c.timeslot; });
    for (i = 0; i < active.length; i++) {
      rows += callRow(active[i], true, showSlot);
    }
    for (i = 0; i < recent.length; i++) {
      rows += callRow(recent[i], false, showSlot);
    }

    var total = active.length + recent.length;
    var before = callsBody.querySelector(".calls-window");
    var place = callsPlace(before);
    /* The caption is the table's name for a screen reader, and is drawn above
     * the window as well: inside it, it would scroll away with the rows. */
    redraw(callsBody,
      '<p class="calls__meta"><span>' + callsCaption(payload) + "</span>" +
      '<span class="calls__count">' + total + (total === 1 ? " call" : " calls") +
      ', newest first<span class="calls__more" hidden> · scroll for more</span></span></p>' +
      '<div class="table-scroll calls-window" tabindex="0" role="group" ' +
      'aria-label="Last heard, ' + total + (total === 1 ? " call" : " calls") + ', scrollable">' +
      '<table class="table">' +
      '<caption class="sr-only">' + callsCaption(payload) + "</caption>" +
      "<thead><tr>" +
      '<th scope="col">Radio ID</th><th scope="col">Target</th>' +
      (showSlot ? '<th scope="col">Slot</th>' : "") + '<th scope="col">Duration</th>' +
      '<th scope="col">Frames</th><th scope="col">When</th>' +
      "</tr></thead><tbody>" + rows + "</tbody></table>" +
      '<div class="calls-window__fade" aria-hidden="true"></div></div>');
    var win = callsBody.querySelector(".calls-window");
    if (win !== before) {
      restoreCallsPlace(win, place);
    }
    markMore(win);
  }

  function refreshPeers() {
    fetch("/api/peers", { headers: { Accept: "application/json" } })
      .then(function (response) {
        return response.json();
      })
      .then(function (payload) {
        renderPeers(payload);
        renderCalls(payload);
        renderTraffic(payload);
        renderMap(payload);
        renderRefused(payload);
        showRouting(payload);
      })
      .catch(function () {
        if (trafficBody) {
          trafficNote.textContent = "unknown";
          trafficBody.innerHTML = "";
        }
        if (peersBody) {
          peersCount.textContent = "unknown";
          peersBody.innerHTML = emptyState(
            "Cannot reach QSP",
            "The peer list could not be loaded. It may be stale or wrong."
          );
        }
      });
  }

  /*
   * Discard local state and re-fetch. There is no local state to discard in
   * this phase, so the handler refreshes health and records that a resync
   * happened. When views hold state, they clear it here.
   */
  function resync(reason) {
    setStream("resynchronised");
    if (reason && window.console && window.console.info) {
      window.console.info("QSP resync: " + reason);
    }
    /* Local state is discarded: after a gap we cannot know which peers are
     * genuinely new, so the next render must not animate arrivals. */
    knownPeerIds = {};
    firstPeerLoad = true;
  refreshHealth();
    refreshPeers();
  }

  function connect() {
    if (source) {
      source.close();
    }

    var url = "/api/events";
    if (lastEventId !== null) {
      url += "?last_event_id=" + encodeURIComponent(lastEventId);
    }

    source = new EventSource(url);

    source.addEventListener("open", function () {
      reconnectDelay = RECONNECT_MIN_MS;
      setStream("connected");
    });

    source.addEventListener("resync", function (event) {
      var reason = "";
      try {
        reason = JSON.parse(event.data).reason;
      } catch (err) {
        reason = "unspecified";
      }
      resync(reason);
    });

    source.addEventListener("health.changed", function (event) {
      if (event.lastEventId) {
        lastEventId = event.lastEventId;
      }
      refreshHealth();
    });

    /* Peer changes are event-driven; the poll below is only a safety net for a
     * stalled stream, not the primary update path. */
    source.addEventListener("peer.connected", function (event) {
      if (event.lastEventId) {
        lastEventId = event.lastEventId;
      }
      refreshPeers();
    });

    source.addEventListener("peer.disconnected", function (event) {
      if (event.lastEventId) {
        lastEventId = event.lastEventId;
      }
      refreshPeers();
    });

    source.addEventListener("call.started", function (event) {
      if (event.lastEventId) {
        lastEventId = event.lastEventId;
      }
      refreshPeers();
    });

    source.addEventListener("call.ended", function (event) {
      if (event.lastEventId) {
        lastEventId = event.lastEventId;
      }
      refreshPeers();
    });

    source.addEventListener("message", function (event) {
      if (event.lastEventId) {
        lastEventId = event.lastEventId;
      }
    });

    source.addEventListener("error", function () {
      // EventSource reconnects on its own, but without our Last-Event-ID
      // parameter. Closing and reconnecting manually preserves the position so
      // the server can tell us whether we missed anything.
      setStream("reconnecting");
      source.close();
      window.setTimeout(connect, reconnectDelay);
      reconnectDelay = Math.min(reconnectDelay * 2, RECONNECT_MAX_MS);
    });
  }

  refreshHealth();
  refreshPeers();
  window.setInterval(refreshHealth, HEALTH_POLL_MS);
  /* Idle times tick upward with no event to announce it, so the table needs a
   * slow refresh even when nothing has changed. */
  window.setInterval(refreshPeers, PEER_POLL_MS);
  connect();

})();
