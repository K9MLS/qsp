/* The first administrator (ADR-0056).
 *
 * **This page exists so the first thing an operator does is not in a
 * terminal.** ADR-0026 required `qsp adduser` and gave a real reason: a server
 * with an empty database belongs to whoever loads it first. The answer is not
 * to remove the page but to guard it — a one-time token, printed once at
 * startup, and not needed from the machine itself.
 */
(function () {
  "use strict";

  var form = document.getElementById("form");
  var done = document.getElementById("done");
  var created = document.getElementById("created");
  var tokenField = document.getElementById("token-field");
  var submit = document.getElementById("submit");

  load();

  function el(id) { return document.getElementById(id); }

  function fail(message) {
    var box = el("error");
    el("error-text").textContent = message;
    box.hidden = false;
  }

  function load() {
    fetch("/api/setup", { headers: { Accept: "application/json" }, credentials: "same-origin" })
      .then(function (r) {
        /* **404 is the ordinary answer once setup is finished**, not an error.
         * The endpoint refuses rather than reporting, so that somebody probing
         * cannot tell a configured QSP from anything else. */
        if (r.status === 404) { done.hidden = false; return null; }
        if (!r.ok) { throw new Error("This server did not answer."); }
        return r.json();
      })
      .then(function (body) {
        if (!body) { return; }
        if (!body.needed) { done.hidden = false; return; }
        if (body.token_required) {
          tokenField.hidden = false;
          el("token-note").textContent = body.note || "";
          el("token").required = true;
        }
        form.hidden = false;
        el("username").focus();
      })
      .catch(function (e) { fail(e.message); });
  }

  if (submit) {
    submit.addEventListener("click", function () {
      el("error").hidden = true;
      submit.disabled = true;
      submit.textContent = "Creating";

      fetch("/api/setup", {
        method: "POST",
        headers: { "Content-Type": "application/json", Accept: "application/json" },
        credentials: "same-origin",
        body: JSON.stringify({
          token: el("token").value.trim(),
          username: el("username").value.trim(),
          password: el("password").value
        })
      }).then(function (r) {
        return r.json().then(function (b) {
          if (!r.ok) { throw new Error(b.error || "could not create the account"); }
          return b;
        });
      }).then(function (b) {
        form.hidden = true;
        el("who").textContent = b.username;
        created.hidden = false;
      }).catch(function (e) {
        fail(e.message);
      }).then(function () {
        submit.disabled = false;
        submit.textContent = "Create administrator";
      });
    });
  }
})();
