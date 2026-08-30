/* QSP configuration history.
 *
 * Lists what has been saved and restores one. **A restore is a save**: it
 * writes a new version whose contents match an older one, which is what
 * ADR-0027 decided and why configuration_versions rows are never updated or
 * deleted. The history is not rewritten, and a restore can itself be undone.
 */
(function () {
  "use strict";

  var loading = document.getElementById("loading");
  var signedOut = document.getElementById("signed-out");
  var errorBox = document.getElementById("error");
  var errorText = document.getElementById("error-text");
  var errorFields = document.getElementById("error-fields");
  var savedBox = document.getElementById("saved");
  var savedText = document.getElementById("saved-text");
  var restartNote = document.getElementById("restart-note");
  var listPanel = document.getElementById("list-panel");
  var list = document.getElementById("list");
  var versionCount = document.getElementById("version-count");

  var preview = document.getElementById("preview");
  var previewTitle = document.getElementById("preview-title");
  var previewSummary = document.getElementById("preview-summary");
  var previewChanges = document.getElementById("preview-changes");
  var previewRestart = document.getElementById("preview-restart");
  var confirmButton = document.getElementById("confirm-restore");

  /* The version being previewed, with the document to write. */
  var pending = null;

  function show(el) { if (el) { el.hidden = false; } }
  function hide(el) { if (el) { el.hidden = true; } }

  function escapeText(value) {
    return String(value === null || value === undefined ? "" : value)
      .replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;").replace(/'/g, "&#39;");
  }

  function when(iso) {
    var d = new Date(iso);
    return isNaN(d.getTime()) ? iso : d.toLocaleString();
  }

  function renderList(versions) {
    if (!versions.length) {
      list.innerHTML = '<div class="empty"><h3 class="empty__title">' +
        "Nothing saved yet</h3><p class=\"empty__body\">Every change made from " +
        "these pages is recorded here, with who made it.</p></div>";
      versionCount.textContent = "none";
      return;
    }

    var rows = "";
    for (var i = 0; i < versions.length; i++) {
      var v = versions[i];
      /* The newest version is what is running, so restoring it would change
       * nothing. Saying so is more use than a button that does nothing. */
      var action = i === 0
        ? '<span class="muted">running now</span>'
        : '<button class="button button--quiet" data-restore="' + v.number +
          '" type="button">Restore</button>';

      rows +=
        "<tr>" +
        '<td class="mono">' + escapeText(v.number) + "</td>" +
        '<td class="cell--wrap">' + escapeText(when(v.created_at)) + "</td>" +
        '<td class="callsign">' + escapeText(v.author) + "</td>" +
        '<td class="cell--wrap">' + escapeText(v.summary || "—") + "</td>" +
        "<td>" + action + "</td>" +
        "</tr>";
    }

    list.innerHTML =
      '<div class="table-scroll" tabindex="0" aria-label="Configuration versions">' +
      '<table class="table"><thead><tr>' +
      '<th scope="col">Version</th><th scope="col" class="cell--wrap">When</th>' +
      '<th scope="col">By</th><th scope="col" class="cell--wrap">Summary</th>' +
      '<th scope="col">Action</th>' +
      "</tr></thead><tbody>" + rows + "</tbody></table></div>";

    versionCount.textContent = versions.length +
      (versions.length === 1 ? " version" : " versions");

    var buttons = list.querySelectorAll("[data-restore]");
    for (var j = 0; j < buttons.length; j++) {
      buttons[j].addEventListener("click", function (event) {
        openPreview(event.currentTarget.getAttribute("data-restore"));
      });
    }
  }

  /* openPreview fetches a version and shows what restoring it would change.
   *
   * A restore applies within a second, so this is the only chance an operator
   * gets to see the difference — and "restore version 4" means nothing without
   * knowing what version 4 said. */
  function openPreview(number) {
    hide(errorBox);
    hide(savedBox);

    fetch("/api/config/versions/" + encodeURIComponent(number), {
      headers: { Accept: "application/json" },
      credentials: "same-origin"
    })
      .then(function (r) {
        return r.json().then(function (body) { return { status: r.status, body: body }; });
      })
      .then(function (res) {
        if (res.status !== 200) {
          errorText.textContent = (res.body && res.body.error) || "That version could not be read.";
          show(errorBox);
          return;
        }

        pending = res.body;
        previewTitle.textContent = "Restore version " + res.body.number;

        var changes = res.body.changes || [];
        previewSummary.textContent = changes.length === 0
          ? "This version matches what is running, so restoring it would change nothing."
          : "Saved by " + res.body.author + " on " + when(res.body.created_at) +
            ". Restoring it makes " + changes.length +
            (changes.length === 1 ? " change" : " changes") + ":";

        previewChanges.innerHTML = "";
        changes.forEach(function (c) {
          var li = document.createElement("li");
          li.textContent = c.field + ": " + c.from + " → " + c.to;
          previewChanges.appendChild(li);
        });

        var restart = res.body.needs_restart || [];
        if (restart.length) {
          previewRestart.textContent =
            "Needs a restart to take effect: " + restart.join(", ") + ".";
          show(previewRestart);
        } else {
          hide(previewRestart);
        }

        confirmButton.disabled = changes.length === 0;
        show(preview);
        preview.scrollIntoView({ behavior: "smooth", block: "nearest" });
      })
      .catch(function () {
        errorText.textContent = "Cannot reach this instance.";
        show(errorBox);
      });
  }

  function restore() {
    if (!pending) {
      return;
    }
    hide(errorBox);
    errorFields.innerHTML = "";
    confirmButton.disabled = true;

    fetch("/api/config", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      credentials: "same-origin",
      body: JSON.stringify({
        config: pending.config,
        summary: "restored version " + pending.number
      })
    })
      .then(function (r) {
        return r.json().then(function (body) { return { status: r.status, body: body }; });
      })
      .then(function (res) {
        confirmButton.disabled = false;

        if (res.status === 200) {
          hide(preview);
          savedText.textContent = "Version " + pending.number +
            " restored, saved as version " + res.body.version + ".";
          var restart = res.body.needs_restart || [];
          if (restart.length) {
            restartNote.textContent =
              "Restart QSP for these to take effect: " + restart.join(", ") + ".";
            show(restartNote);
          } else {
            hide(restartNote);
          }
          show(savedBox);
          pending = null;
          load();
          return;
        }

        if (res.status === 400 && res.body.fields) {
          /* An old version can fail validation the current build would refuse —
           * a setting that has since gained a rule, say. Saying which is more
           * use than refusing to restore anything. */
          errorText.textContent = res.body.error || "";
          res.body.fields.forEach(function (f) {
            var li = document.createElement("li");
            li.textContent = f.field + ": " + f.problem + (f.fix ? " — " + f.fix : "");
            errorFields.appendChild(li);
          });
          show(errorBox);
          return;
        }

        errorText.textContent = (res.body && res.body.error) || "That could not be restored.";
        show(errorBox);
      })
      .catch(function () {
        confirmButton.disabled = false;
        errorText.textContent = "Cannot reach this instance.";
        show(errorBox);
      });
  }

  function load() {
    fetch("/api/config/versions", {
      headers: { Accept: "application/json" },
      credentials: "same-origin"
    })
      .then(function (r) {
        if (r.status === 401) {
          hide(loading);
          show(signedOut);
          return null;
        }
        return r.json();
      })
      .then(function (body) {
        if (!body) { return; }
        hide(loading);
        renderList(body.versions || []);
        show(listPanel);
      })
      .catch(function () {
        hide(loading);
        errorText.textContent = "Cannot reach this instance.";
        show(errorBox);
      });
  }

  confirmButton.addEventListener("click", restore);
  document.getElementById("cancel-restore").addEventListener("click", function () {
    pending = null;
    hide(preview);
  });

  load();
})();
