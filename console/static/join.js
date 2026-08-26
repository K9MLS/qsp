/*
 * The join page.
 *
 * No build step and no dependencies, matching the rest of the console. A club
 * member's hotspot is often on a network with no route to the internet, so a
 * page that needs a CDN to render is a page that does not render.
 *
 * Polling rather than SSE. This page is read by people who are simultaneously
 * restarting their hotspot and reloading dashboards; a long-lived connection
 * that has to be re-established on every reload buys nothing here, and a poll
 * is far easier to reason about when it goes wrong.
 */

(function () {
  "use strict";

  var POLL_MS = 3000;

  function $(id) {
    return document.getElementById(id);
  }

  function text(el, value) {
    if (el) {
      el.textContent = value;
    }
  }

  /* Talkgroup rows. The dialled number is the one that matters, so it is first
   * and set in the largest type. "Arrives as" is shown because a member who
   * looks at their hotspot's log will otherwise see a number nobody told them
   * about and reasonably conclude something is wrong. */
  function renderTalkgroups(list) {
    var body = $("talkgroup-rows");
    if (!body) {
      return;
    }
    body.innerHTML = "";

    if (!list || list.length === 0) {
      var empty = document.createElement("tr");
      var cell = document.createElement("td");
      cell.colSpan = 4;
      cell.className = "fields__missing";
      cell.textContent = "No talkgroups are configured on this network yet.";
      empty.appendChild(cell);
      body.appendChild(empty);
      return;
    }

    list.forEach(function (tg) {
      var row = document.createElement("tr");

      var dial = document.createElement("td");
      var dialCode = document.createElement("code");
      dialCode.className = "tg-dial";
      dialCode.textContent = String(tg.dialled);
      dial.appendChild(dialCode);

      var slot = document.createElement("td");
      slot.textContent = "TS" + tg.timeslot;

      var name = document.createElement("td");
      name.textContent = tg.name || "";

      var arrives = document.createElement("td");
      if (tg.arrives && tg.arrives !== tg.dialled) {
        arrives.className = "tg-arrives";
        arrives.textContent = "TG " + tg.arrives;
      } else {
        arrives.className = "fields__missing";
        arrives.textContent = "unchanged";
      }

      row.appendChild(dial);
      row.appendChild(slot);
      row.appendChild(name);
      row.appendChild(arrives);
      body.appendChild(row);
    });
  }

  /* The step that makes this a page rather than a printed sheet: the server
   * says whether it can see you. */
  function renderWaiting(data) {
    var box = $("waiting");
    var label = $("waiting-text");
    if (!box) {
      return;
    }

    if (!data.enabled) {
      box.setAttribute("data-state", "off");
      text(label, "The network is not accepting hotspots at the moment.");
      return;
    }

    if (data.you) {
      box.setAttribute("data-state", "found");
      var who = data.you.callsign
        ? data.you.callsign + " (" + data.you.id + ")"
        : "Radio ID " + data.you.id;
      text(label, "Connected: " + who + ". You are on the network.");
      return;
    }

    box.setAttribute("data-state", "waiting");
    text(label, "Watching for your hotspot to connect…");
  }

  function renderCount(data) {
    var el = $("connected-count");
    if (!el) {
      return;
    }
    if (!data.enabled) {
      text(el, "");
      return;
    }
    var n = data.connected || 0;
    if (n === 0) {
      text(el, "No hotspots are connected right now.");
    } else if (n === 1) {
      text(el, "1 hotspot is connected to this network.");
    } else {
      text(el, n + " hotspots are connected to this network.");
    }
  }

  function render(data) {
    var settings = data.settings || {};

    if (settings.network_name) {
      text($("network-name"), "Join " + settings.network_name);
      document.title = "Join " + settings.network_name + " — QSP";
    }

    text($("field-address"), settings.address || "ask your network admin");
    text($("field-port"), settings.port ? String(settings.port) : "62031");

    var notice = $("disabled-notice");
    if (notice) {
      if (data.enabled) {
        notice.hidden = true;
      } else {
        notice.hidden = false;
        text($("disabled-reason"), data.reason || "");
      }
    }

    renderTalkgroups(settings.talkgroups);
    renderWaiting(data);
    renderCount(data);
  }

  function poll() {
    fetch("/api/join", { cache: "no-store" })
      .then(function (response) {
        if (!response.ok) {
          throw new Error("HTTP " + response.status);
        }
        return response.json();
      })
      .then(render)
      .catch(function () {
        /* Say nothing rather than something wrong. A member who is mid-restart
         * will see failures that resolve on their own, and an alarming banner
         * would send them troubleshooting a problem that does not exist. */
        var box = $("waiting");
        if (box && box.getAttribute("data-state") !== "found") {
          text($("waiting-text"), "Cannot reach the network right now. Retrying…");
        }
      })
      .finally(function () {
        window.setTimeout(poll, POLL_MS);
      });
  }

  poll();
})();
