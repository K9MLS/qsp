"""The Overview: what it says about stations it is refusing.

Each check names what it protects. To see one fail, undo the thing its
docstring names in console/static/console.js and run this again.
"""
from harness import Page


def peers(refused):
    return lambda rq: (200, {"enabled": True, "forwarding": True, "peers": [], "active": [],
                             "recent": [], "traffic": {}, "refused": refused})


def check_only_a_lockout_still_to_come_is_called_one():
    """Every refused login was shown as "ignored until" a time: the server
    sent year one where there was no lockout, and the page took any value as
    one.

    Break it: test f.locked_until for being there, as the page used to."""
    refused = [
        {"address": "203.0.113.5:62031", "repeater_id": 3132913, "reason": "wrong password",
         "failures": 2, "since": "2026-10-08T11:00:00Z", "locked_until": "0001-01-01T00:00:00Z"},
        {"address": "203.0.113.6:62031", "repeater_id": 3132914, "reason": "wrong password",
         "failures": 9, "since": "2026-10-08T11:00:00Z", "locked_until": "2099-01-01T00:00:00Z"},
        {"address": "203.0.113.7:62031", "repeater_id": 3132915, "reason": "wrong password",
         "failures": 1, "since": "2026-10-08T11:00:00Z"},
    ]
    p = Page("", api={("GET", "/api/peers"): peers(refused)})
    try:
        rows = p.page.locator("#refused-body tbody tr").all_inner_texts()
        assert len(rows) == 3, rows
        assert "retrying" in rows[0] and "ignored" not in rows[0], rows[0]
        assert "ignored until" in rows[1], rows[1]
        assert "retrying" in rows[2], rows[2]
        assert p.page.text_content("#refused-count") == "1 ignored", p.page.text_content("#refused-count")
        assert not p.errors, p.errors
    finally:
        p.close()



def repeater(router, source, ago, **extra):
    r = {"router": router, "up": True, "up_for_seconds": 8040, "site": 1, "type": "Quantar",
         "calls": 3, "frames": 549, "relayed": 549, "sent": 234, "talkgroup": 1,
         "source_id": source, "heard": True, "last_heard_ago_seconds": ago}
    r.update(extra)
    return r


def stations(repeaters, gateways=None, **p25):
    """/api/peers for a server running P25 alone, as the two repeaters of
    2026-10-22 were."""
    body = {"repeaters": repeaters}
    if gateways is None:
        body["gateways_off"] = True
    else:
        body["gateways"] = gateways
    body.update(p25)
    return lambda rq: (200, {"enabled": False, "peers": [], "active": [], "recent": [], "refused": [],
                             "traffic": {"p25": body}})


def ticking(rtrt_disabled=False):
    """The two repeaters, a second older at every answer, so every refresh
    draws different markup and the section is really rebuilt."""
    n = {"asked": 0}

    def answer(rq):
        n["asked"] += 1
        yours = repeater("192.168.1.44", 8080303, 40 + n["asked"], calls=2, frames=234, relayed=234, sent=171)
        theirs = repeater("192.0.2.60", 8080305, 12 + n["asked"], rtrt_disabled=rtrt_disabled)
        return stations([yours, theirs])(rq)
    return answer


def open_overview(answer, **kw):
    return Page("", api={("GET", "/api/peers"): answer}, clock=True, **kw)


def chips(p):
    return p.page.locator("#p25-stations-summary .stations__chip").all_inner_texts()


def is_open(p):
    return p.page.evaluate("() => document.getElementById('p25-stations').open")


def check_the_stations_are_one_line_until_opened():
    """Two repeaters were four lines of sentences on every visit. Folded, the
    line says whether all are linked and who spoke last, and nothing else.

    Break it: draw the details element with `open` whatever stationsOpen is."""
    p = open_overview(ticking())
    try:
        assert not is_open(p), "the stations section was open on a first visit"
        got = chips(p)
        assert got[0] == "2 of 2 repeaters linked", got
        assert any(c.startswith("Last heard 8080305 · 13s ago") for c in got), got
        assert not any("attention" in c for c in got), got
        assert not p.page.is_visible(".stations__table"), "the table shows while folded"
        assert p.page.locator("[data-p25-warning]").count() == 0
        assert not p.errors, p.errors
    finally:
        p.close()


def check_a_repeater_marked_rtrt_disabled_is_named_with_the_fix():
    """On 2026-10-22 one repeater's calls reached the other intact and were
    never transmitted. Folding the section must not fold that away: the chip
    counts it and the sentence saying what to change stays in view.

    Break it: in stationsMarkup, drop the warnings.forEach that writes them."""
    p = open_overview(ticking(rtrt_disabled=True))
    try:
        assert not is_open(p)
        assert "1 needs attention" in chips(p), chips(p)
        warnings = p.page.locator("[data-p25-warning]").all_inner_texts()
        assert len(warnings) == 1, warnings
        assert "192.0.2.60" in warnings[0] and "RT/RT Configuration to ENABLED" in warnings[0], warnings[0]
        assert p.in_view("[data-p25-warning]"), "the warning is not in view while folded"
        assert not p.errors, p.errors
    finally:
        p.close()


def check_no_rtrt_warning_when_both_are_enabled():
    """A field the server leaves out is not a warning.

    Break it: push the warning whatever r.rtrt_disabled says."""
    p = open_overview(ticking())
    try:
        assert p.page.locator("[data-p25-warning]").count() == 0
        assert not p.errors, p.errors
    finally:
        p.close()


def check_opened_the_table_has_a_row_for_each_station():
    """Opened, each gateway and repeater is a row, with the figures in
    columns rather than in a sentence.

    Break it: in stationsMarkup, leave the gateways out of rows."""
    gateway = {"callsign": "K9MLS", "address": "192.0.2.9:41000", "talkgroup": 1, "source_id": 8080303,
               "frames": 88, "sent": 412, "last_poll_ago_seconds": 3}
    answer = stations([repeater("192.168.1.44", 8080303, 40),
                       repeater("192.0.2.60", 8080305, 12, rtrt_disabled=True, transmitting=True)],
                      gateways=[gateway])
    p = open_overview(answer)
    try:
        p.page.click("#p25-stations-summary")
        p.page.wait_for_timeout(100)
        assert p.page.is_visible(".stations__table"), "opening did not show the table"
        rows = p.page.locator(".stations__table tbody tr").all_inner_texts()
        assert len(rows) == 3, rows
        assert "192.168.1.44" in rows[0] and "up 2h 14m" in rows[0], rows[0]
        assert "RT/RT off" in rows[1] and "transmitting now" in rows[1], rows[1]
        assert "K9MLS" in rows[2] and "polled 3s ago" in rows[2] and "88 · — · 412" in rows[2], rows[2]
        assert "1 gateway linked" in chips(p), chips(p)
        assert "8080305 transmitting now" in chips(p), chips(p)
        assert not p.errors, p.errors
    finally:
        p.close()


def check_open_survives_the_refresh():
    """The panel is drawn again every ten seconds, and a section that shut
    itself under the reader would be worse than none.

    Break it: in the toggle listener, stop setting stationsOpen."""
    p = open_overview(ticking())
    try:
        p.page.click("#p25-stations-summary")
        before = p.asked("GET", "/api/peers")
        p.pass_time(21)
        assert p.asked("GET", "/api/peers") >= before + 2, "the page did not refresh; nothing was checked"
        assert is_open(p), "a refresh closed the stations section"
        p.page.click("#p25-stations-summary")
        p.pass_time(11)
        assert not is_open(p), "a refresh opened it again after it was closed"
    finally:
        p.close()


def check_the_choice_is_remembered_on_reload():
    """Somebody who keeps it open should not open it on every visit.

    Break it: read the stored choice as anything but "open"."""
    p = open_overview(ticking())
    try:
        p.page.click("#p25-stations-summary")
        p.page.reload()
        p.page.wait_for_timeout(600)
        assert is_open(p), "a reload forgot the section was open"
    finally:
        p.close()


def check_it_works_with_storage_refused():
    """A browser that refuses storage still folds and opens; the choice
    simply lasts as long as the page.

    Break it: remove the try around localStorage.getItem."""
    refuse = """Object.defineProperty(window, 'localStorage',
                  { get() { throw new DOMException('denied', 'SecurityError'); } });"""
    p = open_overview(ticking(), init=refuse)
    try:
        assert p.page.locator("#p25-stations").count() == 1, "nothing was drawn"
        p.page.click("#p25-stations-summary")
        p.pass_time(11)
        assert is_open(p), "it did not stay open without storage"
        assert not p.errors, p.errors
    finally:
        p.close()


def check_keyboard_focus_survives_the_refresh():
    """A keyboard user on the summary was put back at the top of the page by
    every refresh, because the element they were on was replaced.

    Break it: in redraw, drop the refocus by id."""
    p = open_overview(ticking())
    try:
        p.page.focus("#p25-stations-summary")
        p.pass_time(11)
        assert p.page.evaluate("() => document.activeElement && document.activeElement.id") == \
            "p25-stations-summary", "the refresh took the focus away"
        p.page.keyboard.press("Enter")
        p.page.wait_for_timeout(100)
        assert is_open(p), "Enter on the summary did not open it"
    finally:
        p.close()


ZELLO_OVER = 'peer 3121380 keyed up while transcoder "zello" was carrying TG 2'
LINK_OVER = "peer 3132910 keyed up while link BCARA was carrying TG 9"


def collided(count, notes):
    """/api/peers for a server that has refused `count` frames, explained by
    `notes`, newest first, as the server sends them."""
    traffic = {"datagrams_in": 48211, "frames_accepted": 9120, "collisions": count, "ignored": 0}
    if notes is not None:
        traffic["recent_collisions"] = notes
    return {"enabled": True, "forwarding": True, "active": [], "recent": [], "refused": [],
            "peers": [{"id": 3121380, "callsign": "K9MLS", "protocol": "homebrew"}],
            "traffic": traffic}


def note(summary, at, refusals=50, key_over=True):
    return {"started": at, "at": at, "refusals": refusals, "summary": summary, "key_over": key_over}


THREE_OVERS = [note(ZELLO_OVER, "2026-10-26T21:36:04Z", 120), note(ZELLO_OVER, "2026-10-26T21:31:13Z", 310),
               note(LINK_OVER, "2026-10-26T21:20:05Z", 318)]


def collisions_metric(p):
    return p.page.locator(".metric", has_text="collisions")


def check_collisions_say_who_talked_over_whom():
    """COLLISIONS read 748 in amber on 2026-10-27 with nothing beside it: three
    key-overs the day before. Now the number comes with what it counted, run
    by run, and a peer the console knows is called by its callsign.

    Break it: drop the `showCollisions` note block in renderTraffic, pass
    collisionLines no peer list, or make whenSaid leave out the date."""
    p = open_overview(lambda rq: (200, collided(748, THREE_OVERS)))
    try:
        assert "metric--warn" in collisions_metric(p).get_attribute("class")
        lines = p.page.locator(".inline-note--collisions .inline-note__line").all_inner_texts()
        assert len(lines) == 2, lines
        assert lines[0].startswith("2 key-overs, last at "), lines[0]
        assert "K9MLS (3121380) keyed up while transcoder \"zello\" was carrying TG 2" in lines[0], lines[0]
        assert "Oct 26" in lines[0], "a key-over from another day has no date: " + lines[0]
        assert lines[1].startswith("1 key-over at "), lines[1]
        assert "peer 3132910" in lines[1], "an unknown peer lost its number: " + lines[1]
        assert not p.errors, p.errors
    finally:
        p.close()


def check_collisions_go_quiet_and_keep_their_number():
    """The note and the amber end together, as IGNORED's do: an amber number
    that outlives its reason is what this was built to fix. The count stays.

    Break it: colour the metric on `collisions > 0` again, or in noteFor test
    `count >= state.seen`, and an unchanged number never lets it go."""
    p = open_overview(lambda rq: (200, collided(748, THREE_OVERS)))
    try:
        p.pass_time(12)
        assert "metric--muted" in collisions_metric(p).get_attribute("class")
        assert p.page.locator(".inline-note--collisions").count() == 0
        assert collisions_metric(p).locator(".metric__value").inner_text() == "748"
        assert not p.errors, p.errors
    finally:
        p.close()


def check_a_new_key_over_is_news_again():
    """Once dismissed, the note comes back when the number rises: somebody
    has just talked over somebody, and that is worth saying.

    Break it: in noteFor, arm only the first time (`&& state.seen < 0`)."""
    state = {"count": 748, "notes": THREE_OVERS}
    p = open_overview(lambda rq: (200, collided(state["count"], state["notes"])))
    try:
        p.pass_time(12)
        assert p.page.locator(".inline-note--collisions").count() == 0
        state["count"] = 798
        state["notes"] = [note(ZELLO_OVER, "2026-10-28T12:00:00Z")] + THREE_OVERS
        p.pass_time(11)
        assert "metric--warn" in collisions_metric(p).get_attribute("class")
        lines = p.page.locator(".inline-note--collisions .inline-note__line").all_inner_texts()
        assert lines and lines[0].startswith("3 key-overs"), lines
        assert not p.errors, p.errors
    finally:
        p.close()


def check_a_public_view_shows_a_quiet_number():
    """Signed out, the server withholds the sentences, so the page has a
    number and nothing to say about it: muted, never an unexplained amber.

    Break it: let showCollisions be true without lines."""
    p = open_overview(lambda rq: (200, collided(748, None)))
    try:
        assert "metric--muted" in collisions_metric(p).get_attribute("class")
        assert p.page.locator(".inline-note--collisions").count() == 0
        assert not p.errors, p.errors
    finally:
        p.close()


def check_the_collision_note_is_not_cut_off_on_a_phone():
    """Two lines wrap to five at phone width, and the note's ceiling, which
    the fade starts from, cut the second one in half.

    Break it: remove the 767px rule for .inline-note--collisions."""
    p = open_overview(lambda rq: (200, collided(748, THREE_OVERS)), viewport=(390, 844))
    try:
        clipped = p.page.evaluate("""() => { const n = document.querySelector('.inline-note--collisions');
                                     return n.scrollHeight - n.clientHeight; }""")
        assert clipped <= 1, "%dpx of the note is cut off" % clipped
        assert not p.errors, p.errors
    finally:
        p.close()
