/* QSP console chrome: who is signed in, and whether the administration links
 * lead anywhere.
 *
 * **Five pages carried five copies of the same /api/session fetch**, and four
 * of them could only report a session, not end one — sign-out existed on the
 * overview page alone. Copies drift: the overview's copy grew a sign-out
 * button and the other four did not, which is not a decision anybody made.
 *
 * **The nav offered four administration pages to a signed-out visitor.**
 * Nothing behind them leaks — every admin page keeps its form hidden until
 * /api/config answers, and /api/config is behind requireSession — so a visitor
 * gets the page furniture and a sign-in notice. That is safe and it is also a
 * dead end presented as a destination: four links that can only ever say no.
 * The group says what it needs now, rather than the page saying it four clicks
 * later.
 *
 * The links stay live. Disabling them would be a lie of a different kind: they
 * work, they are simply useless without a session, and a disabled control that
 * would in fact respond is worse than an honest one that explains itself.
 */
(function () {
  "use strict";

  var authState = document.getElementById("auth-state");
  var adminGroup = document.getElementById("nav-admin-group");

  /* Exposed so a page can redraw the chrome after it changes the session —
   * signing out is the only case, and it happens inside this file, but a page
   * that grows a sign-in form should not have to reload to look right. */
  window.QSPNav = { render: render };

  render();

  function render() {
    fetch("/api/session", {
      headers: { Accept: "application/json" },
      credentials: "same-origin"
    })
      .then(function (r) { return r.json(); })
      .then(function (body) {
        apply(!!(body && body.authenticated), body ? body.username : "");
      })
      .catch(function () {
        /* Unreachable is not signed out. Saying "sign in to change these"
         * because a fetch failed would be an assertion this file cannot
         * support, so it asserts nothing and clears the one thing it knows is
         * stale. */
        if (authState) {
          authState.textContent = "";
        }
      });
  }

  function apply(signedIn, username) {
    if (authState) {
      if (signedIn) {
        authState.innerHTML =
          '<span class="topbar__who">' + escapeText(username) + "</span> " +
          '<button class="topbar__link" id="sign-out" type="button">Sign out</button>';
        var out = document.getElementById("sign-out");
        if (out) {
          out.addEventListener("click", function () {
            fetch("/api/logout", { method: "POST", credentials: "same-origin" })
              .then(render)
              .catch(render);
          });
        }
      } else {
        authState.innerHTML = '<a class="topbar__link" href="/signin">Sign in</a>';
      }
    }

    if (!adminGroup) {
      return;
    }
    var note = document.getElementById("nav-admin-note");
    if (signedIn) {
      if (note) {
        note.parentNode.removeChild(note);
      }
      return;
    }
    if (note) {
      return;
    }
    note = document.createElement("li");
    note.className = "nav__note";
    note.id = "nav-admin-note";
    note.textContent = "Sign in to change these.";
    adminGroup.appendChild(note);
  }

  function escapeText(value) {
    return String(value === undefined || value === null ? "" : value)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  }
})();
