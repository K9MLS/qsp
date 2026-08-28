/* QSP sign-in.
 *
 * A form, three fetches, and no framework — the same terms as the rest of the
 * console. See docs/adr/ADR-0026-authentication.md for what it is talking to.
 */
(function () {
  "use strict";

  var form = document.getElementById("form");
  var signedIn = document.getElementById("signed-in");
  var noAccounts = document.getElementById("no-accounts");
  var errorBox = document.getElementById("error");
  var errorText = document.getElementById("error-text");
  var who = document.getElementById("who");
  var username = document.getElementById("username");
  var password = document.getElementById("password");
  var submit = document.getElementById("submit");
  var logout = document.getElementById("logout");

  function show(el) {
    if (el) {
      el.hidden = false;
    }
  }

  function hide(el) {
    if (el) {
      el.hidden = true;
    }
  }

  function showError(message) {
    errorText.textContent = message;
    show(errorBox);
  }

  /* Whoever is signed in already sees that rather than a form.
   *
   * Landing on a login page while logged in and being asked to log in again is
   * how somebody ends up with two sessions and no idea which one the console is
   * using. */
  function refresh() {
    fetch("/api/session", { headers: { Accept: "application/json" } })
      .then(function (r) { return r.json(); })
      .then(function (body) {
        if (body && body.authenticated) {
          who.textContent = body.username;
          hide(form);
          show(signedIn);
          return;
        }
        hide(signedIn);
        show(form);
        username.focus();
      })
      .catch(function () {
        // The instance is unreachable. Showing the form anyway would let
        // somebody type a password into a page that cannot send it.
        showError("Cannot reach this instance. Check it is running.");
      });
  }

  function signIn() {
    hide(errorBox);
    hide(noAccounts);

    var name = username.value.trim();
    if (!name || !password.value) {
      showError("Enter a callsign and a password.");
      return;
    }

    submit.disabled = true;
    fetch("/api/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      /* Same-origin, so the session cookie the response sets is kept. */
      credentials: "same-origin",
      body: JSON.stringify({ username: name, password: password.value })
    })
      .then(function (r) {
        return r.json().then(function (body) { return { status: r.status, body: body }; });
      })
      .then(function (res) {
        submit.disabled = false;
        /* The password is cleared on every outcome, not only on success: a
         * failed attempt leaves it sitting in a form field on a screen
         * somebody may walk away from. */
        password.value = "";

        if (res.status === 200) {
          window.location.href = "/";
          return;
        }
        if (res.status === 503) {
          show(noAccounts);
          hide(form);
          return;
        }
        /* Everything else is the server's message. It says the same thing for
         * a wrong password and an unknown callsign on purpose, and repeating
         * it verbatim keeps this page from being more specific than the API
         * deliberately is. */
        showError((res.body && res.body.error) || "Sign in failed.");
        password.focus();
      })
      .catch(function () {
        submit.disabled = false;
        showError("Cannot reach this instance. Check it is running.");
      });
  }

  function signOut() {
    fetch("/api/logout", { method: "POST", credentials: "same-origin" })
      .then(function () { refresh(); })
      .catch(function () { showError("Cannot reach this instance."); });
  }

  submit.addEventListener("click", signIn);
  logout.addEventListener("click", signOut);

  /* Enter submits from either field. A login form that ignores the return key
   * is the sort of thing people assume is broken. */
  [username, password].forEach(function (el) {
    el.addEventListener("keydown", function (event) {
      if (event.key === "Enter") {
        event.preventDefault();
        signIn();
      }
    });
  });

  refresh();
})();
