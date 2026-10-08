"""The Overview's refresh: what it may not take away from the reader.

The Overview asks again every ten seconds. Each check names what it
protects; to see one fail, undo the thing its docstring names.
"""
from harness import Page


def peer(n, lat, lon):
    return {"id": 3132900 + n, "callsign": "W9T%02d" % n, "status": "connected", "protocol": "homebrew",
            "connected_for": "1h", "idle_for": "2s", "latitude": lat, "longitude": lon,
            "location": "Town %d" % n, "address": "203.0.113.%d:62031" % n}


def answer(lat_shift=0.0, count=12):
    peers = [peer(n, 41.5 + n * 0.05 + lat_shift, -87.5 - n * 0.05) for n in range(count)]
    return lambda rq: (200, {"enabled": True, "forwarding": True, "peers": peers, "active": [],
                             "recent": [], "traffic": {}, "map": {"max_zoom": 18}})


def overview(api=None, viewport=(1280, 900)):
    calls = {("GET", "/api/peers"): answer()}
    calls.update(api or {})
    return Page("", api=calls, clock=True, viewport=viewport)


def view(p):
    return p.page.get_attribute(".map-frame", "data-view")


def check_a_zoomed_map_stays_zoomed():
    """An operator who zoomed in on one town was put back to the whole
    network at the next refresh, before they could read it.

    Break it: in map.js show(), set fitted = false unconditionally."""
    p = overview()
    try:
        fitted = view(p)
        assert fitted, "the map was not drawn"
        p.page.click(".map__zoom[data-zoom='1']")
        p.page.click(".map__zoom[data-zoom='1']")
        zoomed = view(p)
        assert zoomed != fitted, "zooming did nothing, so nothing was checked"
        p.pass_time(31)
        assert p.asked("GET", "/api/peers") >= 3, "the page did not refresh"
        assert view(p) == zoomed, "after three refreshes the view is %s, not %s" % (view(p), zoomed)
    finally:
        p.close()


def check_an_untouched_map_follows_the_stations():
    """The other half: a view nobody moved is fitted again when a station
    moves, so a map left open shows where everybody is.

    Break it: in map.js show(), never set fitted = false."""
    p = overview()
    try:
        before = view(p)
        p.api[("GET", "/api/peers")] = answer(lat_shift=5.0)
        p.pass_time(11)
        assert view(p) != before, "the stations moved and the map did not follow"
    finally:
        p.close()


def check_the_peers_table_keeps_its_place():
    """Every refresh rebuilt every table: one scrolled sideways on a phone went
    back to its first column, and one tabbed into lost its focus.

    Break it: in console.js redraw(), set innerHTML without putting the
    scroll and focus back."""
    p = overview(viewport=(420, 900))
    try:
        table = "#peers-body .table-scroll"
        p.page.focus(table)
        p.page.eval_on_selector(table, "e => { e.scrollLeft = 200; }")
        assert p.page.eval_on_selector(table, "e => e.scrollLeft") > 0, \
            "the table does not scroll at this width, so nothing was checked"
        # A station's idle time changes, so the table is really redrawn.
        p.api[("GET", "/api/peers")] = answer(count=13)
        p.pass_time(11)
        assert p.page.locator("#peers-body tbody tr").count() == 13, "the table was not redrawn"
        assert p.page.eval_on_selector(table, "e => e.scrollLeft") > 0, "the table went back to its first column"
        assert p.page.evaluate("document.activeElement === document.querySelector('%s')" % table), \
            "the table lost its focus"
    finally:
        p.close()
