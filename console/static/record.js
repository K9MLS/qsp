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
        "<td>" + (c.group ? "TG " : "to ") + c.target + "</td>" +
        "<td>TS" + c.timeslot + "</td>" +
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
    fetch("/api/calls?hours=" + encodeURIComponent(value("window", "12")) + "&limit=1000",
      { headers: { Accept: "application/json" }, credentials: "same-origin" })
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
      .catch(function () {
        hide(loading);
        count.textContent = "\u2014";
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
