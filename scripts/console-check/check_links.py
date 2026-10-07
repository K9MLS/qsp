"""The Links page: what its five-second refresh may and may not do.

Each check names what it protects. To see one fail, undo the thing its
docstring names in console/static/links.js and run this again.
"""
from harness import Page


def link(name, far_end, received=6, **more):
    out = {"name": name, "protocol": "qsp", "far_end": far_end, "announces": "3100001",
           "network": "FarNet", "enabled": True, "open": True, "sent": 5, "received": received,
           "rejected": 0, "measured": True, "ever_received": True, "idle_seconds": 7,
           "summary": "carrying", "configured": True}
    out.update(more)
    return out


def links(received=6, alpha="far.example:62031"):
    return lambda rq: (200, {
        "links": [link("alpha", alpha, received), link("bravo", "other.example:62031", received),
                  link("charlie", "", received, inbound=True)],
        "identity": {"callsign": "W9XYZ", "network_id": 3100, "address": "qsp.example.org:62045",
                     "link_address": "qsp.example.org:62031"}})


def open_page():
    return Page("links", api={("GET", "/api/links"): links()}, clock=True)


def received(p):
    """The first link's Received figure, which every refresh changes."""
    return p.page.evaluate(
        "() => [...document.querySelectorAll('.link__fact')].filter(f => f.querySelector('dt')"
        " && f.querySelector('dt').textContent === 'Received')[0].querySelector('dd').textContent")


def check_the_page_does_refresh():
    """The other checks prove nothing if it does not."""
    p = open_page()
    try:
        assert received(p) == "6"
        p.api[("GET", "/api/links")] = links(received=60)
        p.pass_time(6)
        assert received(p) == "60", "the list was not redrawn: %r" % received(p)
    finally:
        p.close()


def check_a_confirmation_survives_the_refresh():
    """"Remove alpha?" became "Remove" again between the two clicks, for
    Remove and for Stop accepting.

    Break it: have busy() return false."""
    for selector, question, method, path in (
            ('[data-remove="alpha"]', "Remove alpha?", "DELETE", "/api/links/alpha"),
            ('[data-refuse="3100001"]', "Stop accepting charlie?", "DELETE", "/api/links/inbound/3100001")):
        p = open_page()
        try:
            p.page.click(selector)
            assert p.page.inner_text(selector) == question
            p.api[("GET", "/api/links")] = links(received=60)
            p.pass_time(4)  # a refresh falls in here; the confirmation lasts five seconds
            assert p.page.inner_text(selector) == question, \
                "after a refresh the button says %r" % p.page.inner_text(selector)
            p.api[(method, path)] = lambda rq: (200, {"peer": 3100001, "refused": True, "password_revoked": True})
            p.page.click(selector)
            p.page.wait_for_timeout(300)
            assert p.asked(method, path) == 1, "the second click did not do it"
            # Once nothing is being asked, the page catches up.
            p.pass_time(6)
            assert received(p) == "60"
        finally:
            p.close()


def check_a_typed_address_survives_the_refresh_and_is_what_is_saved():
    """Type a new address, click somewhere else, and the next refresh put the
    old one back; Save then saved the old one.

    Break it: remove putBack(typed) from load()."""
    p = open_page()
    try:
        p.page.fill('[data-address="alpha"]', "new.example:62031")
        p.page.focus("#offer-callsign")  # the operator has moved on
        p.api[("GET", "/api/links")] = links(received=60)
        p.pass_time(6)
        assert received(p) == "60", "the list was not redrawn, so nothing was checked"
        assert p.page.input_value('[data-address="alpha"]') == "new.example:62031"
        # A box nobody typed in still follows the server.
        assert p.page.input_value('[data-address="bravo"]') == "other.example:62031"

        p.api[("PUT", "/api/links/alpha/address")] = lambda rq: (200, {"address": "new.example:62031"})
        p.api[("GET", "/api/links")] = links(received=61, alpha="new.example:62031")
        p.page.click('[data-save="alpha"]')
        p.page.wait_for_timeout(300)
        sent = [b for m, path, b in p.requests if m == "PUT" and path == "/api/links/alpha/address"]
        assert sent == ['{"address":"new.example:62031"}'], sent
        # The page is redrawn at once after the save, though a button was busy.
        assert received(p) == "61"
        assert not p.page.is_disabled('[data-save="alpha"]')
    finally:
        p.close()


def check_typing_is_not_interrupted():
    """Break it: drop the activeElement test from busy()."""
    p = open_page()
    try:
        p.page.focus('[data-address="alpha"]')
        p.page.keyboard.type("x")
        p.api[("GET", "/api/links")] = links(received=60)
        p.pass_time(6)
        assert p.page.evaluate("document.activeElement.getAttribute('data-address')") == "alpha", \
            "the cursor was taken out of the box being typed in"
        p.page.keyboard.type("y")
        assert p.page.input_value('[data-address="alpha"]').endswith("xy") or \
            p.page.input_value('[data-address="alpha"]').startswith("xy")
    finally:
        p.close()


def check_a_cleared_offer_box_stays_cleared():
    """The offer form was refilled every five seconds.

    Break it: fill the offer on every refresh, as fillOffer did."""
    p = open_page()
    try:
        assert p.page.input_value("#offer-callsign") == "W9XYZ", "it is filled once, to begin with"
        assert p.page.input_value("#offer-address") == "qsp.example.org:62031"
        p.page.fill("#offer-callsign", "")
        p.page.fill("#offer-address", "")
        p.page.focus("#offer")
        p.pass_time(11)
        assert p.page.input_value("#offer-callsign") == "" and p.page.input_value("#offer-address") == "", \
            "cleared boxes were refilled"
        # The address still follows the kind of link when that is changed.
        p.page.select_option("#offer-kind", "openbridge")
        assert p.page.input_value("#offer-address") == "qsp.example.org:62045"
    finally:
        p.close()


def check_the_address_box_has_a_name():
    """A screen reader announced it as "edit text".

    Break it: remove the aria-label from the address input."""
    p = open_page()
    try:
        assert p.page.get_attribute('[data-address="alpha"]', "aria-label") == "Far end address of alpha"
    finally:
        p.close()
