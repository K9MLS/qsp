"""Last heard: fifty calls in a window ten rows tall.

Each check names what it protects. To see one fail, undo the thing its
docstring names in console/static/console.js or console.css and run this
again.
"""
from harness import Page


def call(n, live=False):
    """Call n: higher n is newer, so a list can grow at the top."""
    c = {"source": 3132000 + n, "target": 9, "group": True, "timeslot": 2,
         "duration": "4s", "frames": 200, "voice": True}
    if not live:
        c["ago"] = "%ds ago" % (1000 - n)
        c["ended_at"] = "2026-10-26T12:%02d:%02dZ" % (n // 60, n % 60)
    return c


class Feed:
    """/api/peers, newest call first, with calls added between refreshes."""

    def __init__(self, count=50, newest=100):
        self.newest = newest
        self.count = count

    def arrive(self, n=1):
        self.newest += n

    def __call__(self, rq):
        recent = [call(self.newest - i) for i in range(self.count)]
        return (200, {"enabled": True, "forwarding": True, "peers": [], "active_calls": [],
                      "recent_calls": recent, "traffic": {}})


def overview(feed, width=1280):
    return Page("", api={("GET", "/api/peers"): feed}, clock=True, viewport=(width, 1200))


def geometry(p):
    return p.page.evaluate("""() => {
      const w = document.querySelector('#calls .calls-window');
      const head = w.querySelector('thead th').getBoundingClientRect();
      const box = w.getBoundingClientRect();
      const rows = [...w.querySelectorAll('tbody tr')].map(r => {
        const b = r.getBoundingClientRect();
        return {key: r.getAttribute('data-call'), top: b.top - head.bottom, bottom: b.bottom - box.bottom};
      });
      return {rows, scrollTop: w.scrollTop, more: w.getAttribute('data-more'),
              headTop: head.top - box.top, height: w.clientHeight};
    }""")


def first_in_view(g):
    return next(r for r in g["rows"] if r["top"] + 45 > 1)


def check_the_window_shows_ten_rows_and_holds_fifty():
    """Fifty rows drawn in full made Last heard three times longer than
    anything else on the Overview. Ten show; all fifty are there.

    Break it: remove max-height from .calls-window."""
    p = overview(Feed())
    try:
        g = geometry(p)
        assert len(g["rows"]) == 50, len(g["rows"])
        visible = [r for r in g["rows"] if r["bottom"] <= 1]
        assert len(visible) == 10, "%d rows fit the window, want 10" % len(visible)
        assert abs(g["rows"][9]["bottom"]) <= 1, "the window does not end on a row: %r" % g["rows"][9]
        assert not p.errors, p.errors
    finally:
        p.close()


def check_eight_rows_on_a_phone():
    """Ten tall rows are most of a phone screen.

    Break it: remove the --calls-rows: 8 media rule."""
    p = overview(Feed(), width=390)
    try:
        visible = [r for r in geometry(p)["rows"] if r["bottom"] <= 1]
        assert len(visible) == 8, "%d rows fit the window on a phone, want 8" % len(visible)
    finally:
        p.close()


def check_the_heading_stays_while_the_rows_scroll():
    """Row thirty-seven still has to say which column is Duration.

    Break it: remove position: sticky from .calls-window thead th."""
    p = overview(Feed())
    try:
        p.page.evaluate("() => { document.querySelector('.calls-window').scrollTop = 900; }")
        p.page.wait_for_timeout(100)
        g = geometry(p)
        assert g["scrollTop"] > 800, g["scrollTop"]
        assert abs(g["headTop"]) <= 1, "the heading scrolled away: %r" % g["headTop"]
    finally:
        p.close()


def check_a_refresh_keeps_the_call_being_read():
    """Put back at the same scroll offset, the list slid one row under the
    reader for every call that arrived, and the line they were reading
    walked away.

    Break it: make restoreCallsPlace return without moving anything."""
    feed = Feed()
    p = overview(feed)
    try:
        p.page.evaluate("() => { document.querySelector('.calls-window').scrollTop = 20 * 45; }")
        p.page.wait_for_timeout(100)
        reading = first_in_view(geometry(p))
        feed.arrive(3)
        before = p.asked("GET", "/api/peers")
        p.pass_time(11)
        assert p.asked("GET", "/api/peers") > before, "the page did not refresh; nothing was checked"
        after = first_in_view(geometry(p))
        assert after["key"] == reading["key"], "reading %s, now %s" % (reading["key"], after["key"])
        assert abs(after["top"] - reading["top"]) <= 1, (reading, after)
    finally:
        p.close()


def check_at_the_top_a_new_call_arrives_in_view():
    """Somebody watching the top is watching for the newest call, so the top
    is kept rather than the call that used to be there.

    Break it: in callsPlace, drop the scrollTop <= 1 test, so the top call
    is held in place and the new ones arrive above the window."""
    feed = Feed()
    p = overview(feed)
    try:
        newest = geometry(p)["rows"][0]["key"]
        feed.arrive(2)
        p.pass_time(11)
        g = geometry(p)
        assert g["scrollTop"] == 0, g["scrollTop"]
        assert g["rows"][0]["key"] != newest and g["rows"][2]["key"] == newest, \
            "the new calls are not at the top in view"
    finally:
        p.close()


def check_the_fade_says_there_is_more_and_goes_at_the_end():
    """A fade that stayed at the end would make the last row look cut off;
    one that never showed would hide that there is more.

    Break it: make markMore return at once."""
    p = overview(Feed())
    try:
        assert geometry(p)["more"] == "true", "no fade with forty rows below"
        assert p.page.is_visible(".calls__more"), "the caption does not say to scroll"
        p.page.evaluate("() => { const w = document.querySelector('.calls-window'); w.scrollTop = w.scrollHeight; }")
        p.page.wait_for_timeout(100)
        assert geometry(p)["more"] == "false", "the fade stayed at the end"
        assert not p.page.is_visible(".calls__more"), "scroll for more, at the end"
    finally:
        p.close()
    p = overview(Feed(count=4))
    try:
        assert geometry(p)["more"] == "false", "a fade with nothing below"
        assert not p.page.is_visible(".calls__more"), "scroll for more, with nothing to scroll"
    finally:
        p.close()


def check_the_caption_counts_the_calls():
    """Nobody should have to scroll to learn how many there are.

    Break it: count recent calls only in the caption."""
    feed = Feed(count=50)
    p = Page("", api={("GET", "/api/peers"): lambda rq: (
        200, {"enabled": True, "forwarding": True, "peers": [], "recent_calls": feed(rq)[1]["recent_calls"],
              "active_calls": [call(500, live=True)], "traffic": {}})})
    try:
        assert p.page.inner_text(".calls__count").startswith("51 calls, newest first"), \
            p.page.inner_text(".calls__count")
        assert not p.errors, p.errors
    finally:
        p.close()
