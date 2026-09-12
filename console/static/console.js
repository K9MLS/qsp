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

    healthBody.innerHTML =
      '<div class="table-scroll" tabindex="0" role="group" aria-label="Health, scrollable"><table class="table">' +
      "<caption>Every subsystem reports its own verdict. " +
      "Unavailable names the phase that brings it.</caption>" +
      "<thead><tr>" +
      '<th scope="col">Subsystem</th><th scope="col">Status</th>' +
      '<th scope="col" class="cell--wrap">Summary</th>' +
      '<th scope="col" class="cell--wrap">Detail</th>' +
      "</tr></thead><tbody>" + rows + "</tbody></table></div>";

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

    if (!payload.enabled) {
      peersCount.textContent = "disabled";
      peersBody.innerHTML = emptyState(
        "The DMR listener is not enabled",
        payload.reason || "Set dmr.enabled in the configuration to accept peers."
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

    peersBody.innerHTML =
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
      "</tr></thead><tbody>" + rows + "</tbody></table></div>";

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
    for (var i = 0; i < list.length; i++) {
      var p = list[i];
      if (typeof p.latitude !== "number" || typeof p.longitude !== "number") {
        continue;
      }
      points.push({
        lat: p.latitude,
        lon: p.longitude,
        label: p.callsign || String(p.id)
      });
    }

    if (mapCount) {
      mapCount.textContent = points.length + " of " + list.length + " located";
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
      if (f.locked_until) {
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
    body.innerHTML =
      '<p class="panel__lede">A hotspot with a wrong password retries every ten ' +
      "seconds. After several failures QSP stops answering that address for a " +
      "while, and starts again on its own.</p>" +
      '<div class="table-scroll" tabindex="0" aria-label="Logins being refused">' +
      '<table class="table"><thead><tr>' +
      '<th scope="col">Address</th><th scope="col">Radio ID</th>' +
      '<th scope="col" class="cell--wrap">Reason</th><th scope="col">Failures</th>' +
      '<th scope="col" class="cell--wrap">State</th>' +
      "</tr></thead><tbody>" + rows + "</tbody></table></div>";
  }

  function metric(value, label, cls) {
    return (
      '<div class="metric ' + (cls || "") + '">' +
      '<div class="metric__value">' + escapeText(value) + "</div>" +
      '<div class="metric__label">' + escapeText(label) + "</div></div>"
    );
  }

  function renderTraffic(payload) {
    if (!trafficBody) {
      return;
    }
    if (!payload.enabled) {
      trafficNote.textContent = "disabled";
      trafficBody.innerHTML = emptyState(
        "The DMR listener is not enabled",
        payload.reason || "Set dmr.enabled in the configuration to accept peers."
      );
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
    var p25 = t.p25;
    var peers = (payload.peers || []).length;

    trafficNote.textContent = "since start";
    trafficBody.innerHTML =
      /* **Both groups are labelled or neither is.** With only P25 named, the
       * row above it read as a total for the server rather than as one
       * listener's counters — which is the same misreading the separate
       * objects in the payload exist to prevent. */
      '<p class="metrics__mode">DMR</p>' +
      '<div class="metrics">' +
      metric(inCount, "datagrams in") +
      metric(frames, "voice frames", frames === 0 ? "metric--muted" : "") +
      metric(t.collisions || 0, "collisions",
        (t.collisions || 0) > 0 ? "metric--warn" : "metric--muted") +
      /* **Only traffic nobody asked for gets amber.** A refusal QSP answered is
       * the protocol working — a keepalive from a peer that has not registered
       * is answered so it logs in again — and counting that beside a stray port
       * scan produced one permanently amber number that looked like a fault and
       * was not. The reasons are below. */
      metric(ignored, "ignored", ignored > 0 ? "metric--warn" : "metric--muted") +
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
    if (ignored > 0) {
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
        trafficBody.innerHTML +=
          '<p class="inline-note">' + lines.join(". ") + ".</p>";
      }
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
     * line up with the row above it cannot be read across. `.metrics` is an
     * auto-fit grid, so three metrics beside four is not a style difference —
     * it is a different number of columns at a different width. Corrected
     * 2026-09-12 from a screenshot.
     *
     * POLLS was a fifth metric and is gone. A five-second keepalive is
     * plumbing, not traffic; "polled 3s ago" on the gateway line is the form
     * of it an operator can act on, and its total is in /healthz.
     *
     * Absent rather than empty when P25 is off, matching IPSC: the payload
     * omits the object, so a server not running P25 says nothing about it
     * instead of showing zeroes. */
    if (p25) {
      var p25In = (p25.polls || 0) + (p25.voice_frames || 0) + (p25.unparsed || 0);
      trafficBody.innerHTML +=
        '<p class="metrics__mode">P25</p>' +
        '<div class="metrics">' +
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

      var gws = p25.gateways || [];
      var p25Lines = [];

      /* A refusal is named rather than counted: the IPSC listener reached
       * 2,144 unnamed refusals before anybody could say which repeater. */
      if ((p25.refused || 0) > 0 && p25.refused_last) {
        p25Lines.push("Last refused: " + escapeText(p25.refused_last));
      }

      if (gws.length === 0) {
        /* Not a fault, and the health check says the same: a reflector nobody
         * has linked to is a working reflector waiting. */
        p25Lines.push("No P25 gateways have linked yet");
      } else {
        gws.forEach(function (g) {
          var bits = [escapeText(g.callsign || "an unnamed gateway")];
          /* Talkgroup and source are omitted until traffic has been heard.
           * Zero is not a talkgroup, and printing it would claim a decode
           * that never happened. */
          if (g.talkgroup) bits.push("TG " + g.talkgroup);
          if (g.source_id) bits.push("last heard " + g.source_id);
          bits.push(g.frames + (g.frames === 1 ? " frame" : " frames"));
          /* Withheld from a public view, so printed only when carried. */
          if (g.address) bits.push(escapeText(g.address));
          bits.push("polled " + g.last_poll_ago_seconds + "s ago");
          return p25Lines.push(bits.join(", "));
        });
      }

      /* **Neutral, not amber.** `.inline-note` alone is
       * `var(--color-degraded)`, so the first version of this drew a gateway
       * working perfectly in the warning colour. Every other amber thing on
       * this page means something is wrong. */
      if (p25Lines.length > 0) {
        trafficBody.innerHTML +=
          '<p class="inline-note inline-note--neutral">' +
          p25Lines.join(". ") + ".</p>";
      }
    }

    /* The case that cost an evening: a peer connected and sending keepalives,
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
      trafficBody.innerHTML +=
        '<p class="inline-note inline-note--neutral">No voice frames yet, only keepalives. ' +
        "If nobody has transmitted, that is exactly what this should look like. " +
        "If somebody has, the sending side is probably not routing a talkgroup " +
        "to this network \u2014 check there.</p>";
    }
  }

  function callRow(call, live) {
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

    var kind = call.group
      ? "TG " + escapeText(call.target)
      : "DM " + escapeText(call.target) +
        (call.target_name ? ' <span class="muted">' + escapeText(call.target_name) + "</span>" : "");

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
    return (
      "<tr>" +
      "<td>" + who + "</td>" +
      '<td class="mono">' + kind + "</td>" +
      '<td class="mono">TS' + escapeText(call.timeslot) + "</td>" +
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
    if (!payload.enabled) {
      callsCount.textContent = "disabled";
      callsBody.innerHTML = emptyState(
        "The DMR listener is not enabled",
        "No transmissions can be observed until peers can connect."
      );
      return;
    }

    var active = payload.active_calls || [];
    var recent = payload.recent_calls || [];

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
    for (i = 0; i < active.length; i++) {
      rows += callRow(active[i], true);
    }
    for (i = 0; i < recent.length; i++) {
      rows += callRow(recent[i], false);
    }

    callsBody.innerHTML =
      '<div class="table-scroll" tabindex="0" role="group" aria-label="Last heard, scrollable"><table class="table">' +
      "<caption>" + callsCaption(payload) + "</caption>" +
      "<thead><tr>" +
      '<th scope="col">Radio ID</th><th scope="col">Target</th>' +
      '<th scope="col">Slot</th><th scope="col">Duration</th>' +
      '<th scope="col">Frames</th><th scope="col">When</th>' +
      "</tr></thead><tbody>" + rows + "</tbody></table></div>";
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
