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

  /* Swap the element between a typeable value and a placeholder, changing the
   * styling with it. The <code> box means "type this exactly". */
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
        arrives.textContent = "TG" + "\u00a0" + tg.arrives;
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

  /* Whether QSP heard this member transmit. The page previously ended by
   * telling them silence was normal, which is true and tells them nothing. */
  function renderHeard(data) {
    var box = $("heard");
    var label = $("heard-text");
    if (!box) {
      return;
    }

    if (!data.enabled || !data.you) {
      box.setAttribute("data-state", "waiting");
      text(label, "Connect your hotspot first, then key up.");
      return;
    }

    var call = data.heard;
    if (!call) {
      box.setAttribute("data-state", "waiting");
      text(label, "Nothing heard from you yet. Key up for a few seconds.");
      return;
    }

    box.setAttribute("data-state", "found");
    var when = call.ago ? call.ago + " ago" : "now";
    text(label,
      "Heard you on TG " + call.target + ", TS" + call.timeslot +
      " — " + call.frames + " frames, " + call.duration + ", " + when + ".");
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

    /* A value a member can type goes in <code>. Anything else must not look
     * like one, or it gets pasted into the field verbatim. */
    setField($("field-address"), settings.address, "Ask whoever runs the network");
    /* 62031 is the port QSP listens on by default, not a guess, so it is a
     * value to type even when the server has not told us one. */
    setField($("field-port"), settings.port ? String(settings.port) : "62031", "");

    var notice = $("disabled-notice");
    if (notice) {
      if (data.enabled) {
        notice.hidden = true;
      } else {
        notice.hidden = false;
        text($("disabled-reason"), data.reason || "");
      }
    }

    /* Following six steps against a listener that cannot accept anyone wastes
     * ten minutes and ends in a failure the member cannot explain. Dim the
     * steps so the notice is read as an instruction to stop, not a footnote. */
    var steps = document.querySelector(".steps");
    if (steps) {
      steps.classList.toggle("steps--blocked", !data.enabled);
    }

    renderTalkgroups(settings.talkgroups);
    renderWaiting(data);
    renderHeard(data);
    renderCount(data);
  }


  /* The generated DMRGateway block.
   *
   * **Both choices belong to the member, not to the club.** Which leading digit
   * and which network slot are free is answerable only from /etc/dmrgateway on
   * their own hotspot, and the rewrite happens there before anything reaches
   * QSP — so one member choosing 7 and another choosing 3 affects neither the
   * network nor each other. That is why this is a control on the page rather
   * than a setting an administrator fills in once. */
  function wireGenerator() {
    var radios = document.querySelectorAll('input[name="other-networks"]');
    for (var i = 0; i < radios.length; i++) {
      radios[i].addEventListener("change", onModeChange);
    }
    bindRefresh("prefix");
    bindRefresh("block");

    var copy = $("copy-config");
    if (copy) {
      copy.addEventListener("click", copyBlock);
    }
    onModeChange();
  }

  function bindRefresh(id) {
    var el = $(id);
    if (el) {
      el.addEventListener("change", refreshConfig);
    }
  }

  function multiNetwork() {
    var yes = document.querySelector('input[name="other-networks"][value="yes"]');
    return !!(yes && yes.checked);
  }

  function onModeChange() {
    var multi = multiNetwork();
    show($("single-note"), !multi);
    show($("multi-note"), multi);
    show($("step-generated"), multi);
    if (multi) {
      refreshConfig();
    }
  }

  function show(el, visible) {
    if (el) {
      el.hidden = !visible;
    }
  }

  function value(id, fallback) {
    var el = $(id);
    return el && el.value ? el.value : fallback;
  }

  function refreshConfig() {
    var pre = $("config-block");
    var prefix = value("prefix", "7");
    var block = value("block", "4");

    text($("example-dial"), prefix + "00000" + firstDialled());

    fetch("/api/join/config?prefix=" + encodeURIComponent(prefix) +
      "&block=" + encodeURIComponent(block), { cache: "no-store" })
      .then(function (r) { return r.json(); })
      .then(function (body) {
        if (!body || !body.block) {
          text(pre, (body && body.reason) || "This cannot be generated yet.");
          renderWarnings([]);
          return;
        }
        text(pre, body.block);
        renderWarnings(body.warnings || []);
      })
      .catch(function () {
        text(pre, "Cannot reach this network to generate the block.");
        renderWarnings([]);
      });
  }

  /* The first talkgroup, purely for the worked example above the picker. A
   * number a member recognises makes the prefix obvious in a way a sentence
   * about leading digits does not. */
  function firstDialled() {
    var row = document.querySelector("#talkgroup-rows code");
    return row ? row.textContent : "9";
  }

  function renderWarnings(list) {
    var el = $("config-warnings");
    if (!el) {
      return;
    }
    el.innerHTML = "";
    for (var i = 0; i < list.length; i++) {
      var li = document.createElement("li");
      li.textContent = list[i];
      el.appendChild(li);
    }
  }

  /* Clipboard access is refused in plenty of ordinary situations — an insecure
   * origin is one, and a club instance reached by IP is exactly that. Saying so
   * beats a button that silently does nothing. */
  function copyBlock() {
    var pre = $("config-block");
    var note = $("copy-note");
    if (!pre) {
      return;
    }
    if (!navigator.clipboard) {
      text(note, "This browser will not copy for us. Select the block and copy it.");
      return;
    }
    navigator.clipboard.writeText(pre.textContent).then(function () {
      text(note, "Copied.");
    }).catch(function () {
      text(note, "This browser will not copy for us. Select the block and copy it.");
    });
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

  wireGenerator();
  poll();
})();
