/* QSPGet reads what a page loads, and says plainly when it could not.
 *
 * **A server's refusal was read as an answer.** A page asked for its data,
 * the server said no — a 500 with the reason in it, a proxy's 502 page — and
 * the page read whatever came back as the data: the record said there were
 * no calls, the history that nothing had ever been saved, the links page that
 * there were no links (found 2026-10-07, F4). An operator looking at an empty
 * page has no way to tell "nothing here" from "the server is broken".
 *
 * Resolves with the body; with null when the visitor is not signed in, which
 * every page answers with its own sign-in notice; and rejects with an Error
 * whose message is the server's own reason when it gave one. `want` names a
 * field the answer must have to be the thing asked for.
 */
(function () {
  "use strict";

  window.QSPGet = function (url, want) {
    return fetch(url, { headers: { Accept: "application/json" }, credentials: "same-origin" })
      .catch(function () {
        throw new Error("This server could not be reached. Check that QSP is running, " +
          "then reload the page.");
      })
      .then(function (r) {
        if (r.status === 401) { return null; }
        return r.json().catch(function () { return null; }).then(function (body) {
          if (!r.ok) {
            throw new Error((body && body.error) ||
              "The server answered " + r.status + " and not what this page asked for.");
          }
          if (!body || (want && !(want in body))) {
            throw new Error("The server's answer is not what this page asked for. " +
              "Reload the page; if it says this again, the server's log will say why.");
          }
          return body;
        });
      });
  };

  /* QSPLoadFailed shows why a page could not load, in the notice every page
   * that uses QSPGet has for it, and QSPLoaded takes it away again. */
  window.QSPLoadFailed = function (e) {
    var box = document.getElementById("load-error");
    var text = document.getElementById("load-error-text");
    if (!box || !text) { return; }
    text.textContent = (e && e.message) || "This page could not be loaded.";
    box.hidden = false;
  };
  window.QSPLoaded = function () {
    var box = document.getElementById("load-error");
    if (box) { box.hidden = true; }
  };
})();
