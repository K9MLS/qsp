/* The call record.
 *
 * **This exists for net control.** A station taking check-ins who misses a
 * callsign reads it back here afterwards. The live list on the overview could
 * not do that: fifty entries in memory, gone on every restart, which is a
 * display and works as one right up until somebody depends on it. See ADR-0033.
 */
(function () {
  "use strict";

  var loading = document.getElementById("loading");
  var signedOut = document.getElementById("signed-out");
  var form = document.getElementById("form");
  var list = document.getElementById("record");
  var count = document.getElementById("record-count");

  function show(el) { if (el) { el.hidden = false; } }
  function hide(el) { if (el) { el.hidden = true; } }
  function value(id, fallback) {
    var el = document.getElementById(id);
    return el && el.value ? el.value : fallback;
  }

  function escapeText(v) {
    return String(v === undefined || v === null ? "" : v)
      .replace(/&/g, "&amp;").replace(/</g, "&lt;")
      .replace(/>/g, "&gt;").replace(/"/g, "&quot;");
  }

  /* Local time, because somebody reading a net back is thinking in the clock on
   * their wall, not in UTC. */
  function when(iso) {
    var d = new Date(iso);
    if (isNaN(d.getTime())) { return iso; }
    return d.toLocaleString();
  }

  function seconds(n) {
    if (!n || n < 1) { return "<1s"; }
    if (n < 60) { return Math.round(n) + "s"; }
    return Math.floor(n / 60) + "m" + Math.round(n % 60) + "s";
  }

  function render(entries) {
    var voiceOnly = value("voice-only", "no") === "yes";
    var shown = [];
    for (var i = 0; i < entries.length; i++) {
      if (voiceOnly && !entries[i].voice) { continue; }
      shown.push(entries[i]);
    }

    count.textContent = shown.length + (shown.length === 1 ? " CALL" : " CALLS");

    if (shown.length === 0) {
      list.innerHTML =
        '<div class="empty"><p class="empty__title">Nothing in this window</p>' +
        '<p class="empty__body">No transmission was carried in the period ' +
        "selected above.</p></div>";
      return;
    }

    var rows = "";
    for (var j = 0; j < shown.length; j++) {
      var c = shown[j];
      /* The callsign when the registry knows it, and always the radio ID: the
       * ID is what the record is about, and a name is a convenience that can be
       * wrong or missing. */
      var who = c.callsign
        ? '<strong>' + escapeText(c.callsign) + "</strong> " +
          '<span class="record__id">' + c.source + "</span>"
        : '<span class="record__id">' + c.source + "</span>";

      rows +=
        '<tr><td>' + escapeText(when(c.started)) + "</td>" +
        "<td>" + who + "</td>" +
        "<td>" + (c.group ? "TG " : "to ") + c.target +
          /* Where a P25 call came into QSP, and that it was heard and not
           * relayed when another station had the turn. */
          (c.via ? ' <span class="record__id">via ' + escapeText(c.via) + "</span>" : "") +
          (c.not_carried ? ' <span class="record__id">(not carried)</span>' : "") + "</td>" +
        /* P25 has no timeslots, so its rows say the mode where a DMR row
         * says the slot. */
        "<td>" + (c.timeslot ? "TS" + c.timeslot : escapeText(c.mode || "\u2014")) + "</td>" +
        "<td>" + (c.voice ? seconds(c.seconds) : "text") + "</td>" +
        "<td>" + escapeText(c.end_reason || "") + "</td></tr>";
    }

    list.innerHTML =
      '<div class="table-scroll"><table class="table"><thead><tr>' +
      "<th>When</th><th>Who</th><th>Called</th><th>Slot</th>" +
      "<th>Length</th><th>Ended</th>" +
      "</tr></thead><tbody>" + rows + "</tbody></table></div>";
  }

  var entries = [];

  function load() {
    /* "calls" must be in the answer: a refusal read as one showed an empty
     * record, as if nobody had transmitted. See get.js. */
    window.QSPGet("/api/calls?hours=" + encodeURIComponent(value("window", "12")) + "&limit=1000", "calls")
      .then(function (body) {
        if (body === null) {
          hide(loading);
          hide(form);
          show(signedOut);
          return;
        }
        hide(loading);
        hide(signedOut);
        window.QSPLoaded();
        entries = body.calls || [];
        if (body.reason) {
          list.innerHTML =
            '<div class="empty"><p class="empty__title">No record is kept</p>' +
            '<p class="empty__body">' + escapeText(body.reason) + "</p></div>";
          count.textContent = "\u2014";
          show(form);
          return;
        }
        render(entries);
        show(form);
      })
      .catch(function (e) {
        hide(loading);
        hide(form);
        count.textContent = "\u2014";
        window.QSPLoadFailed(e);
      });
  }

  var win = document.getElementById("window");
  if (win) { win.addEventListener("change", load); }
  var vo = document.getElementById("voice-only");
  /* Filtering is done here rather than by asking again: the rows are already
   * loaded, and a round trip to hide text messages would be slower and no more
   * correct. */
  if (vo) { vo.addEventListener("change", function () { render(entries); }); }

  load();
})();
