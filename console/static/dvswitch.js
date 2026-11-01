/*
 * The DVSwitch guide.
 *
 * Fills the guide's settings from /api/join, the endpoint the Join page
 * reads: a node logs in where a hotspot does, so its values are the same
 * values, from the same place. Read once rather than polled; nothing here
 * changes while somebody is reading it.
 */

(function () {
  "use strict";

  var ASK = "Ask whoever runs the network";

  function $(id) {
    return document.getElementById(id);
  }

  /* As join.js: a value to type goes in <code>, and a placeholder must not
   * look like one or it gets pasted verbatim. */
  function setField(el, value, placeholder) {
    if (!el) {
      return;
    }
    if (value) {
      el.textContent = value;
      el.classList.remove("field--missing");
      return;
    }
    el.textContent = placeholder;
    el.classList.add("field--missing");
  }

  function cell(row, value, asCode) {
    var td = document.createElement("td");
    if (asCode) {
      var code = document.createElement("code");
      code.textContent = value;
      td.appendChild(code);
    } else {
      td.textContent = value;
    }
    row.appendChild(td);
  }

  /* txTg is the number as it arrives here. A node is not a hotspot behind a
   * rewrite rule, so "Dial this" from the Join page would be the wrong number
   * whenever the two differ: the arrival number is used when there is one. */
  function renderTalkgroups(list) {
    var body = $("talkgroup-rows");
    if (!body) {
      return;
    }
    body.innerHTML = "";
    if (!list || list.length === 0) {
      var empty = document.createElement("tr");
      var td = document.createElement("td");
      td.colSpan = 3;
      td.className = "fields__missing";
      td.textContent = "No talkgroups are listed for this network yet. " + ASK + ".";
      empty.appendChild(td);
      body.appendChild(empty);
      return;
    }
    list.forEach(function (tg) {
      var row = document.createElement("tr");
      cell(row, String(tg.arrives || tg.dialled), true);
      cell(row, String(tg.timeslot), true);
      cell(row, tg.name || "", false);
      body.appendChild(row);
    });
  }

  /* The P25 route only when this server takes P25 gateways. A port nothing
   * answers on is an instruction that cannot work, so it is not printed. */
  function renderP25(settings) {
    var on = !!settings.p25_port;
    var shown = $("p25-on");
    var off = $("p25-off");
    if (shown) shown.hidden = !on;
    if (off) off.hidden = on;
    var route = $("route-p25");
    if (route) route.classList.toggle("route--off", !on);

    var hosts = $("p25-hosts");
    if (hosts && on) {
      hosts.textContent = "number  " + (settings.address || "address") + "  " + settings.p25_port;
    }
  }

  function render(data, known) {
    var settings = (data && data.settings) || {};
    if (settings.network_name) {
      $("network-name").textContent = settings.network_name;
      document.title = "Bring other radios into " + settings.network_name + " with DVSwitch — QSP";
    }
    setField($("dmr-address"), settings.address, ASK);
    /* 62031 is QSP's own default, not a guess; join.js says the same. */
    setField($("dmr-port"), settings.port ? String(settings.port) : "62031", "");
    renderTalkgroups(settings.talkgroups);
    var listed = $("ids-listed");
    if (listed) {
      listed.hidden = !settings.ids_listed;
    }
    /* Unknown is not off: when this server could not be asked, the P25
     * steps stay, with placeholders, rather than saying it takes no
     * gateways. */
    if (known) {
      renderP25(settings);
    }
  }

  fetch("/api/join", { cache: "no-store" })
    .then(function (response) {
      if (!response.ok) {
        throw new Error("HTTP " + response.status);
      }
      return response.json();
    })
    .then(function (data) {
      render(data, true);
    })
    .catch(function () {
      /* Placeholders that say who to ask, rather than an error: the guide is
       * still worth reading without this server's values in it. */
      render({}, false);
    });
})();
