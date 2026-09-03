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
        '<td class="cell--wrap">' + peerAttachments(p) + "</td>" +
        '<td class="cell--wrap">' + peerPlace(p) + "</td>" +
        '<td class="mono">' + escapeText(p.address) + "</td>" +
        "</tr>";
    }

    peersBody.innerHTML =
      '<div class="table-scroll" tabindex="0" role="group" aria-label="Connected peers, scrollable"><table class="table">' +
      "<caption>Peers currently registered with this master. " +
      "IP Site Connect repeaters announce no callsign, location or talkgroups; " +
      "those columns read &ldquo;not sent&rdquo; rather than being empty.</caption>" +
      "<thead><tr>" +
      "<th scope=\"col\">Callsign</th><th scope=\"col\">Radio ID</th>" +
      "<th scope=\"col\">Link</th>" +
      "<th scope=\"col\">State</th><th scope=\"col\">Connected</th>" +
      "<th scope=\"col\">Idle</th><th scope=\"col\">CC</th>" +
      "<th scope=\"col\" class=\"cell--wrap\">Talkgroups</th>" +
      "<th scope=\"col\" class=\"cell--wrap\">Location</th>" +
      "<th scope=\"col\">Address</th>" +
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
      return '<span class="pill pill--ipsc" title="Motorola IP Site Connect">IPSC</span>';
    }
    return '<span class="pill pill--homebrew" title="Homebrew / MMDVM">Homebrew</span>';
  }

  /* peerCallsign distinguishes a callsign nobody sent from one QSP does not
   * know.
   *
   * IP Site Connect carries no callsign at all, so an em dash there would be a
   * blank meaning "not applicable" wearing the costume of "not set". */
  function peerCallsign(p) {
    if (p.callsign && p.callsign_looked_up) {
      /* Looked up, not announced, and shown differently because the two are
       * different claims. A Homebrew peer states its callsign at login; this
       * is QSP matching a radio ID against a public registry that can be
       * stale, or that describes the operator rather than the repeater. */
      return '<span class="callsign callsign--guessed" ' +
        'title="Looked up from the RadioID registry; IP Site Connect carries no callsign">' +
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
    var frames = t.frames_accepted || 0;
    var ipscFrames = ipsc ? ipsc.voice_frames || 0 : 0;
    var peers = (payload.peers || []).length;

    trafficNote.textContent = "since start";
    trafficBody.innerHTML =
      '<div class="metrics">' +
      metric(inCount, "datagrams in") +
      metric(t.datagrams_out || 0, "datagrams out") +
      /* **Two numbers, because they mean different things.** A refusal QSP
       * answered is the protocol working — a keepalive from a peer that has not
       * registered is answered so it logs in again — and counting it beside a
       * stray port scan produced one permanently amber number that looked like
       * a fault and was not. Only traffic nobody asked for gets amber, and even
       * then only the count; the reasons are below. */
      metric(t.answered || 0, "answered", "metric--muted") +
      metric(t.ignored || 0, "ignored", (t.ignored || 0) > 0 ? "metric--warn" : "metric--muted") +
      metric(frames, ipsc ? "voice frames (homebrew)" : "voice frames",
        frames === 0 ? "metric--muted" : "") +
      metric(t.frames_forwarded || 0, "forwarded", (t.frames_forwarded || 0) === 0 ? "metric--muted" : "") +
      metric(t.collisions || 0, "collisions", (t.collisions || 0) > 0 ? "metric--warn" : "metric--muted") +
      /* The Motorola listener's own figures, beside the DMR listener's rather
       * than added into them. The two count different things and one total
       * would imply they do not. */
      (ipsc
        ? metric(ipscFrames, "voice frames (ipsc)", ipscFrames === 0 ? "metric--muted" : "") +
          metric(ipsc.text_bursts || 0, "text bursts (ipsc)",
            (ipsc.text_bursts || 0) === 0 ? "metric--muted" : "") +
          metric(ipsc.ignored || 0, "ipsc ignored",
            (ipsc.ignored || 0) > 0 ? "metric--warn" : "metric--muted")
        : "") +
      "</div>";

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
    /* **The IPSC frames have to count here too.** Before they did, a network
     * whose only traffic was Motorola repeaters showed this note while working
     * perfectly, and it named a hotspot that had nothing to do with anything.
     * A hint that is confidently wrong is worse than no hint: an operator who
     * learns to disbelieve one warning stops reading all of them. */
    if (peers > 0 && inCount > 30 && frames === 0 && ipscFrames === 0) {
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
