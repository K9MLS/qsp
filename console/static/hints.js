/* QSP field hints.
 *
 * A hint is a button beside a label that reveals a paragraph beneath it. It is
 * not a floating tooltip, deliberately:
 *
 *   - A floating tooltip has to be positioned, and positioning against a
 *     measured box is what cost this project most of a day. A disclosure that
 *     expands in the flow cannot be put in the wrong place.
 *   - Hover is not available on a touch screen and is not reachable from a
 *     keyboard. A button is both.
 *   - The content security policy forbids inline style attributes, so anything
 *     positioned from JavaScript would need care here that it does not need at
 *     all.
 *
 * The markup carries the text. This file only wires the buttons, so a page
 * without any works unchanged and a hint is visible to somebody reading the
 * HTML.
 */
(function () {
  "use strict";

  /* Exposed so a page that renders its own markup can wire the hints inside
   * it. This file runs at load; a form drawn later would otherwise have
   * buttons that do nothing, which is worse than no buttons at all. */
  window.QSPHints = { wire: wireAll };

  wireAll(document);

  function wireAll(root) {
    if (!root) {
      return;
    }
    var buttons = root.querySelectorAll(".hint");
    for (var i = 0; i < buttons.length; i++) {
      wire(buttons[i]);
    }
  }

  function wire(button) {
    var id = button.getAttribute("aria-controls");
    var text = id ? document.getElementById(id) : null;
    if (!text) {
      return;
    }

    /* Closed to start with, whatever the markup says, so a page cannot ship
     * with a hint stuck open and no way to tell. */
    text.hidden = true;
    button.setAttribute("aria-expanded", "false");

    /* Wiring the same button twice would toggle it twice per click, which is
     * a hint that never opens. A page that re-renders its form calls wire
     * again, so this has to be idempotent. */
    if (button.getAttribute("data-wired") === "true") {
      return;
    }
    button.setAttribute("data-wired", "true");

    button.addEventListener("click", function () {
      var open = button.getAttribute("aria-expanded") === "true";
      button.setAttribute("aria-expanded", open ? "false" : "true");
      text.hidden = open;
    });
  }
})();
