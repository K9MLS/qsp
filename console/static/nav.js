/* QSP console chrome: who is signed in, and whether the administration links
 * lead anywhere.
 *
 * **Five pages carried five copies of the same /api/session fetch**, and four
 * of them could only report a session, not end one — sign-out existed on the
 * overview page alone. Copies drift: the overview's copy grew a sign-out
 * button and the other four did not, which is not a decision anybody made.
 *
 * **The administration group is not shown to a signed-out visitor at all.**
 * It used to be listed with a note underneath saying to sign in, on the
 * grounds that hiding a page makes the console lie about what exists. An
 * operator reading the sidebar disagreed, and he is right: seven links that
 * can only ever say no are seven pieces of furniture in the way of the four
 * that work. Nothing behind them leaked either way — every admin page keeps
 * its form hidden until /api/config answers, and /api/config is behind
 * requireSession — so this is about what a sidebar is for, not about secrecy.
 *
 * **The markup ships hidden and this file reveals it**, rather than shipping
 * visible and hiding it once /api/session answers. Revealing on a confirmed
 * session means the default state is the one a visitor should see, there is no
 * flash of administration links on every page load, and a fetch that never
 * returns leaves a signed-out console rather than a signed-in-looking one. The
 * cost is that a signed-in operator sees the group appear a moment after the
 * page, which is the smaller of the two wrong states.
 *
 * One line stays where the group was, because a console with no visible way to
 * administer anything reads as a console that cannot.
 */
(function () {
  "use strict";

  var authState = document.getElementById("auth-state");
  var version = document.getElementById("nav-version");
  var adminGroup = document.getElementById("nav-admin-group");
  var adminHeading = document.getElementById("nav-admin");

  /* Exposed so a page can redraw the chrome after it changes the session —
   * signing out is the only case, and it happens inside this file, but a page
   * that grows a sign-in form should not have to reload to look right. */
  window.QSPNav = { render: render };

  render();

  /* **What is running here, at the foot of every page's navigation.** It lived
   * in a startup log line and, since 0305, on the administration page — so the
   * question asked after every deploy meant a terminal or a click, and it was
   * read out of `journalctl` for two days.
   *
   * It rides on the session request this file already makes, so there is no
   * second fetch and one source for the value.
   *
   * **Shown to everybody, signed in or not.** That is a deliberate choice by
   * the operator rather than an oversight: an exact build number tells somebody
   * probing what this server is, and the judgement is that a console reachable
   * by strangers is not the situation QSP is built for.
   *
   * Text and not a link: it is a fact about the server rather than somewhere to
   * go, and a link at the foot of a navigation reads as one more destination.
   * Empty rather than a placeholder until the answer arrives, because a dash
   * where a version belongs is a thing an operator has to interpret.
   */
  function showVersion(value) {
    if (!version) { return; }
    version.textContent = value ? "Version " + value : "";
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
      adminGroup.hidden = false;
      if (adminHeading) {
        adminHeading.hidden = false;
      }
      if (note) {
        note.parentNode.removeChild(note);
      }
      return;
    }

    adminGroup.hidden = true;
    if (adminHeading) {
      adminHeading.hidden = true;
    }
    if (note) {
      return;
    }
    note = document.createElement("p");
    note.className = "nav__note";
    note.id = "nav-admin-note";
    note.textContent = "Sign in to administer this network.";
    adminGroup.parentNode.insertBefore(note, adminGroup);
  }

  function escapeText(value) {
    return String(value === undefined || value === null ? "" : value)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;");
  }
})();
