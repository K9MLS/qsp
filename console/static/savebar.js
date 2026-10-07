/* QSP save bar: where a page's Save button lives, and where its answer is.
 *
 * **A save's answer was somewhere the operator was not looking.** Save was at
 * the foot of a page and "saved" or "refused" was drawn at the head of it, as
 * much as five screens away, so a refused save looked exactly like a
 * successful one: the button came back and nothing else moved (found
 * 2026-10-07, E8).
 *
 * The bar stays at the foot of the window. It says three things, beside the
 * button that causes them: that there are changes not yet saved, that a save
 * worked, and that one did not. When one did not, the reason is brought into
 * view as well, because the reason is longer than a line.
 *
 * It also asks before the page is left with changes unsaved.
 */
window.QSPSaveBar = function (status) {
  "use strict";

  var bar = status ? status.parentNode : null;
  var dirty = false;
  var KINDS = ["dirty", "saving", "saved", "failed"];

  function say(kind, text) {
    if (!status) { return; }
    status.textContent = text || "";
    for (var i = 0; i < KINDS.length; i++) {
      bar.classList.remove("savebar--" + KINDS[i]);
    }
    if (kind) { bar.classList.add("savebar--" + kind); }
  }

  /* reveal brings a notice into view and moves focus to it, so somebody
   * using a screen reader is told as well as somebody looking. */
  function reveal(el) {
    if (!el) { return; }
    if (!el.hasAttribute("tabindex")) { el.setAttribute("tabindex", "-1"); }
    if (el.scrollIntoView) { el.scrollIntoView({ block: "center" }); }
    if (el.focus) { el.focus({ preventScroll: true }); }
  }

  /* watch asks isDirty after anything on the page changes. A message about
   * the last save stands until the next edit. */
  function watch(root, isDirty) {
    function check() {
      var now = !!isDirty();
      if (now === dirty) { return; }
      dirty = now;
      say(dirty ? "dirty" : "", dirty ? "Changes not saved yet." : "");
    }
    root.addEventListener("input", check);
    root.addEventListener("change", check);
    /* Buttons that add or remove a row change the page with no input event. */
    root.addEventListener("click", function () { setTimeout(check, 0); });
    return check;
  }

  window.addEventListener("beforeunload", function (event) {
    if (!dirty) { return; }
    event.preventDefault();
    event.returnValue = "";
  });

  return {
    watch: watch,
    reveal: reveal,
    saving: function () { say("saving", "Saving."); },
    saved: function (text) { dirty = false; say("saved", text); },
    failed: function (text) { say("failed", text); },
    clean: function () { dirty = false; say("", ""); }
  };
};
