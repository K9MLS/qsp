"""The Bridges page.

Each check names what it protects. To see one fail, undo the thing its
docstring names in console/static/bridges.js and run this again.
"""
from harness import Page, config, differences

SAVED = (200, {"version": 8, "changes": [{"field": "dmr.bridges"}]})
REFUSED = (400, {"error": "the configuration was refused",
                 "fields": [{"field": "dmr.bridges[0].name", "problem": "is empty",
                             "fix": "give the bridge a name"}]})


def save(page, answer=SAVED):
    page.api[("POST", "/api/config")] = lambda rq: answer
    page.page.click("#save")
    page.page.wait_for_timeout(300)


def check_an_unedited_save_changes_nothing():
    """Break it: build an endpoint from peer, talkgroup and timeslot alone in
    collect(), as the page did until 0.1.329."""
    p = Page("bridges")
    try:
        save(p)
        sent = p.saves()
        assert len(sent) == 1, "pressing Save posted %d configurations" % len(sent)
        diff = differences(config(), sent[0]["config"])
        assert not diff, "a save with nothing edited changed: %r" % diff
        assert not differences(config(), sent[0]["base"]), "the base sent is not what was loaded"
        assert not p.errors, p.errors
    finally:
        p.close()


def check_a_server_with_no_bridges_is_sent_back_none():
    """Break it: assign next.dmr.bridges unconditionally in collect()."""
    def bare(rq):
        c = config()
        c["dmr"]["bridges"] = None
        c["dmr"]["schedule"] = None
        return 200, {"config": c, "writable": True}
    p = Page("bridges", api={("GET", "/api/config"): bare})
    try:
        save(p)
        sent = p.saves()[0]["config"]["dmr"]
        assert sent["bridges"] is None and sent["schedule"] is None, \
            "nothing was edited and the page sent %r and %r" % (sent["bridges"], sent["schedule"])
    finally:
        p.close()


def check_a_link_and_an_audio_channel_survive_an_edit():
    """The fault itself: change one talkgroup, and the bridge to another
    network came back as a bridge to "every peer".

    Break it: drop `over(e.raw, ...)` for a plain object in collect()."""
    p = Page("bridges")
    try:
        # The link's own endpoint, on the first bridge.
        box = p.page.locator('[data-bridge="0"][data-endpoint="1"][data-key="talkgroup"]')
        box.fill("3101")
        box.dispatch_event("change")
        save(p)
        diff = differences(config(), p.saves()[0]["config"])
        assert diff == [(".dmr.bridges[0].endpoints[1].talkgroup", 3100, 3101)], \
            "editing one talkgroup changed: %r" % diff
    finally:
        p.close()


def check_a_link_is_shown_as_what_it_is():
    """A link and an "any peer" endpoint were drawn as the same row. The
    link has no Peer box to turn it into something else, and no Remove.

    Break it: make fixed() return null."""
    p = Page("bridges")
    try:
        shown = p.page.locator(".tg-row__fixed").all_inner_texts()
        assert shown == ["Link to brandmeister", "Audio channel zello"], shown
        for where in ("0:1", "1:1"):
            b, e = where.split(":")
            assert p.page.locator('[data-bridge="%s"][data-endpoint="%s"][data-key="peer"]' % (b, e)).count() == 0, \
                "endpoint %s is a link or a channel and has a Peer box" % where
            assert p.page.locator('[data-remove-endpoint="%s"]' % where).count() == 0, \
                "endpoint %s can be removed here; the page that made it removes it" % where
        # The ordinary ones are as they were.
        assert p.page.locator('[data-key="peer"]').count() == 2
        assert p.page.locator("[data-remove-endpoint]").count() == 2
    finally:
        p.close()


def check_the_schedule_follows_a_renamed_bridge():
    """Break it: delete the windows.forEach in the name handler."""
    p = Page("bridges")
    try:
        name = p.page.locator('[data-bridge="0"][data-key="name"]')
        name.fill("brandmeister-net")
        name.dispatch_event("change")
        save(p)
        diff = differences(config(), p.saves()[0]["config"])
        assert diff == [(".dmr.bridges[0].name", "bm", "brandmeister-net"),
                        (".dmr.schedule[0].bridge", "bm", "brandmeister-net")], diff
    finally:
        p.close()


def check_a_refused_save_is_seen():
    """Save is at the foot of the page and the reason was drawn at the head.

    Break it: remove bar.failed and bar.reveal from save()."""
    for viewport in ((1280, 900), (390, 844)):
        p = Page("bridges", viewport=viewport)
        try:
            p.page.evaluate("window.scrollTo(0, document.body.scrollHeight)")
            save(p, REFUSED)
            status = p.page.inner_text("#save-status")
            assert status.startswith("Not saved"), "the bar says %r" % status
            assert p.in_view("#save-status"), "the bar's answer is off the screen at %r" % (viewport,)
            assert p.in_view("#error"), "the reason is off the screen at %r" % (viewport,)
            assert "give the bridge a name" in p.page.inner_text("#error")
        finally:
            p.close()


def check_a_save_that_worked_says_so_where_the_button_is():
    """Break it: remove bar.saved from save()."""
    for viewport in ((1280, 900), (390, 844)):
        p = Page("bridges", viewport=viewport)
        try:
            p.page.evaluate("window.scrollTo(0, document.body.scrollHeight)")
            box = p.page.locator('[data-bridge="0"][data-endpoint="0"][data-key="talkgroup"]')
            box.fill("3102")
            box.dispatch_event("change")
            assert p.page.inner_text("#save-status") == "Changes not saved yet."
            save(p)
            status = p.page.inner_text("#save-status")
            assert "saved as version 8" in status, "the bar says %r" % status
            assert p.in_view("#save-status") and p.in_view("#save")
            # And a second save starts from what was saved.
            save(p)
            assert not differences(p.saves()[0]["config"], p.saves()[1]["config"])
            assert not differences(p.saves()[0]["config"], p.saves()[1]["base"]), \
                "the second save's base is not what the first one saved"
        finally:
            p.close()


def check_the_bar_knows_when_there_is_nothing_to_save():
    """Break it: have changed() return true."""
    p = Page("bridges")
    try:
        assert p.page.inner_text("#save-status") == ""
        box = p.page.locator('[data-bridge="0"][data-endpoint="0"][data-key="talkgroup"]')
        box.fill("3102")
        box.dispatch_event("change")
        assert p.page.inner_text("#save-status") == "Changes not saved yet."
        box.fill("3100")
        box.dispatch_event("change")
        assert p.page.inner_text("#save-status") == "", "put back as it was, and still called unsaved"
        p.page.click("#add-bridge")
        p.page.wait_for_timeout(50)
        assert p.page.inner_text("#save-status") == "Changes not saved yet."
        p.page.click("#revert")
        assert p.page.inner_text("#save-status") == ""
        assert p.page.locator('[data-key="name"]').count() == 2
    finally:
        p.close()


def check_a_server_error_is_not_an_empty_page():
    """A 500 on load drew an empty Bridges page with a working Save button.

    Break it: go back to `return r.json()` in load()."""
    p = Page("bridges", api={("GET", "/api/config"): lambda rq: (500, {"error": "the database is locked"})})
    try:
        assert p.page.is_visible("#load-error") and "database is locked" in p.page.inner_text("#load-error")
        assert not p.page.is_visible("#error"), "a page that could not load says it did not save"
        assert not p.page.is_visible("#form"), "the form is offered with nothing loaded into it"
    finally:
        p.close()
