"""The DVSwitch guide: what it gets a node owner, and this network's settings.

Each check names what it protects. To see one fail, undo the thing its
docstring names in console/static/dvswitch.js or dvswitch.html and run this
again.
"""
from harness import Page


def joined(**settings):
    base = {"network_name": "QSP Server Prod", "address": "qsp.example.org", "port": 62031,
            "talkgroups": [{"name": "Local", "dialled": 9, "timeslot": 2},
                           {"name": "Wide", "dialled": 7000002, "arrives": 2, "timeslot": 2}]}
    base.update(settings)
    return {("GET", "/api/join"): lambda rq: (200, {"enabled": True, "settings": base})}


def check_this_networks_settings_are_filled_in():
    """A node logs in where a hotspot does, so the guide's values are the Join
    page's, and the talkgroup a node sets is the one that arrives here: a
    node has no hotspot rewriting its number.

    Break it: in dvswitch.js, use tg.dialled alone, or stop filling
    dmr-address or p25-hosts."""
    p = Page("dvswitch", api=joined(p25_port=41000))
    try:
        assert p.page.inner_text("#dmr-address") == "qsp.example.org"
        assert p.page.inner_text("#dmr-port") == "62031"
        rows = p.page.locator("#talkgroup-rows tr").all_inner_texts()
        assert [r.split("\t")[0] for r in rows] == ["9", "2"], rows
        assert p.page.inner_text("#p25-hosts") == "number  qsp.example.org  41000", p.page.inner_text("#p25-hosts")
        assert p.page.is_visible("#p25-on") and not p.page.is_visible("#p25-off")
        assert "QSP Server Prod" in p.page.inner_text(".join__lede")
        assert not p.errors, p.errors
    finally:
        p.close()


def check_no_p25_steps_when_this_network_takes_no_gateways():
    """With the P25 listener off there is no port to give, and steps ending
    at a port nothing answers on cannot work. The route stays described, and
    plainly not on offer.

    Break it: in renderP25, leave p25-on shown whatever the port."""
    p = Page("dvswitch", api=joined())
    try:
        assert not p.page.is_visible("#p25-on"), "P25 steps shown with no P25 listener"
        assert p.page.is_visible("#p25-off")
        assert "route--off" in p.page.get_attribute("#route-p25", "class")
        assert not p.errors, p.errors
    finally:
        p.close()


def check_a_server_that_cannot_be_asked_is_not_called_off():
    """When /api/join fails, nothing is known about P25, which is not the
    same as P25 being off. The steps stay, and the address says who to ask.

    Break it: in the fetch's catch, call render({}, true)."""
    p = Page("dvswitch", api={("GET", "/api/join"): lambda rq: (500, "{}")})
    try:
        assert p.page.is_visible("#p25-on") and not p.page.is_visible("#p25-off")
        assert "Ask whoever runs the network" in p.page.inner_text("#dmr-address")
        assert not p.errors, p.errors
    finally:
        p.close()


def check_nothing_claims_to_be_proven_before_it_is():
    """Nobody has linked DVSwitch to QSP yet. Green is for what has been seen
    working, so the only green mark on the page is the legend explaining it.

    Break it: mark a route mark--proven in dvswitch.html."""
    p = Page("dvswitch", api=joined(p25_port=41000))
    try:
        assert p.page.locator(".route .mark--proven").count() == 0
        assert p.page.locator(".mark--proven").count() == 1
        assert "Nobody has linked DVSwitch to this network yet" in p.page.inner_text(".join__foot")
        assert not p.errors, p.errors
    finally:
        p.close()


def check_a_phone_does_not_scroll_sideways():
    """The settings tables and the hosts line are the widest things on the
    page; at phone width they wrap rather than push the page wider.

    Break it: give .config a min-width of 40rem."""
    p = Page("dvswitch", api=joined(p25_port=41000), viewport=(390, 844))
    try:
        wider = p.page.evaluate("() => document.documentElement.scrollWidth - window.innerWidth")
        assert wider <= 0, "%dpx wider than the phone" % wider
        assert not p.errors, p.errors
    finally:
        p.close()
