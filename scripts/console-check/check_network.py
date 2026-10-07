"""The Network settings page.

Each check names what it protects. To see one fail, undo the thing its
docstring names in console/static/network.js and run this again.
"""
from harness import Page, config, differences, fresh, serving

SAVED = (200, {"version": 8, "changes": [{"field": "x"}]})
RESTART = (200, {"version": 9, "changes": [{"field": "x"}], "needs_restart": ["p25.enabled"]})
REFUSED = (400, {"error": "the configuration was refused",
                 "fields": [{"field": "dmr.identity.callsign", "problem": "is not a callsign",
                             "fix": "use letters and digits"}]})


def save(page, answer=SAVED):
    page.api[("POST", "/api/config")] = lambda rq: answer
    page.page.click("#save")
    page.page.wait_for_timeout(300)


def edit(page, selector, value):
    box = page.page.locator(selector)
    box.fill(value)
    box.dispatch_event("change")


def check_an_unedited_save_changes_nothing():
    """On a server with everything set, and on one just installed, where the
    page used to write a parrot and two durations and ask for a restart.

    Break it: in collect(), write any setting without asking touched()."""
    for name, document in (("a full configuration", config()), ("a fresh install", fresh())):
        p = Page("network", api={("GET", "/api/config"): serving(document)})
        try:
            assert p.page.inner_text("#save-status") == "", "%s: called unsaved as it opened" % name
            save(p)
            sent = p.saves()
            assert len(sent) == 1, "%s: Save posted %d configurations" % (name, len(sent))
            diff = differences(document, sent[0]["config"])
            assert not diff, "%s: a save with nothing edited changed %r" % (name, diff)
            assert not p.errors, p.errors
        finally:
            p.close()


def check_a_value_the_list_does_not_offer_is_shown_and_kept():
    """A timeout of twenty minutes was drawn as fifteen and saved as fifteen.

    Break it: return from setIfOffered when the value is not in the list."""
    document = config()
    document["dmr"]["subscription"]["timeout"] = "20m0s"
    document["dmr"]["calls"] = {"retain": "48h0m0s"}
    p = Page("network", api={("GET", "/api/config"): serving(document)})
    try:
        assert p.page.input_value("#subscription-timeout") == "20m0s"
        assert p.page.input_value("#retain") == "48h0m0s"
        assert "20m0s" in p.page.inner_text("#subscription-timeout option:checked")
        edit(p, "#network-name", "Another Net")
        save(p)
        diff = differences(document, p.saves()[0]["config"])
        assert diff == [(".dmr.join.network_name", "Club Net", "Another Net")], diff
    finally:
        p.close()


def check_one_edit_changes_one_setting():
    """Break it: write next.dmr.parrot.max_duration on every save."""
    for name, document in (("a full configuration", config()), ("a fresh install", fresh())):
        p = Page("network", api={("GET", "/api/config"): serving(document)})
        try:
            edit(p, "#identity-location", "Davenport")
            assert p.page.inner_text("#save-status") == "Changes not saved yet."
            save(p)
            diff = differences(document, p.saves()[0]["config"])
            assert [d[0] for d in diff] == [".dmr.identity.location"], "%s: %r" % (name, diff)
        finally:
            p.close()


def check_what_cannot_be_read_is_refused_and_named():
    """41,88 was saved as latitude 0, and a repeater list with a stray word
    lost its repeaters, each answered "Saved".

    Break it: use parseInt(...) || 0 or Number(...) with a fallback."""
    cases = [
        ("#identity-latitude", "41,88", "Latitude", "comma"),
        ("#identity-latitude", "north", "Latitude", "not a number"),
        ("#identity-longitude", "-190", "Longitude", "not between"),
        ("#ipsc-peers", "3101, 31o2", "Motorola repeaters allowed", "31o2"),
        ("#ipsc-master", "31x", "Motorola master ID", "not a whole number"),
        ("#ipsc-cc", "16", "Colour code", "not between"),
        ("#p25_repeaters-hold", "500", "Hold before transmitting", "not between"),
        ("#p25-allowed", "W9AAA\nW9 BBB", "P25 gateways allowed", "more than one word"),
        ("#p25-max", "lots", "Most gateways at once", "not a whole number"),
        ("#p25-max", "0", "Most gateways at once", "not between"),
    ]
    for selector, typed, label, says in cases:
        p = Page("network")
        try:
            # type="number" boxes refuse letters; set those as a paste would.
            p.page.evaluate("([s, v]) => { const e = document.querySelector(s); e.type = e.type === 'number' ? 'text' : e.type; e.value = v; e.dispatchEvent(new Event('input', {bubbles: true})); }", [selector, typed])
            save(p)
            assert p.saves() == [], "%s = %r was sent to the server" % (selector, typed)
            said = p.page.inner_text("#error")
            assert label in said and says in said, "%s = %r: the page said %r" % (selector, typed, said)
            assert "nothing was saved" in said
            assert p.page.get_attribute(selector, "aria-invalid") == "true"
            assert p.page.inner_text("#save-status").startswith("Not saved: " + label)
            assert p.in_view("#save-status") and p.in_view(selector), \
                "%s: the box to mend is off the screen" % selector
            # Mended, it saves.
            p.page.click("#revert")
            save(p)
            assert len(p.saves()) == 1 and not differences(config(), p.saves()[0]["config"])
        finally:
            p.close()


def check_a_blank_box_clears_and_a_zero_is_a_zero():
    """Break it: treat a blank hold as 0, or a blank colour code as 0."""
    p = Page("network")
    try:
        edit(p, "#identity-latitude", "")
        edit(p, "#p25_repeaters-hold", "")
        edit(p, "#ipsc-cc", "0")
        edit(p, "#p25-max", "40")
        save(p)
        diff = differences(config(), p.saves()[0]["config"])
        assert diff == [(".dmr.identity.latitude", 41.88, 0),
                        (".ipsc.colour_code", 1, 0),
                        (".p25.max_gateways", "<absent>", 40),
                        (".p25_repeaters.hold_ms", 150, "<absent>")], diff
        # And blanked again, it leaves the document as the default does.
        edit(p, "#p25-max", "")
        save(p)
        assert "max_gateways" not in p.saves()[1]["config"]["p25"]
    finally:
        p.close()


def check_turning_something_on_supplies_what_it_needs():
    """On a fresh install: a port for P25, and the parrot's two durations.
    Supplied as it is turned on, and at no other time.

    Break it: delete the turnedOn(...) blocks from collect()."""
    p = Page("network", api={("GET", "/api/config"): serving(fresh())})
    try:
        # The switches are drawn over their checkboxes, so a click is sent to
        # the box itself, as a keyboard would.
        p.page.locator("#p25-enabled").dispatch_event("click")
        p.page.locator("#parrot-enabled").dispatch_event("click")
        edit(p, "#parrot-talkgroup", "9990")
        save(p)
        diff = dict((d[0], d[2]) for d in differences(fresh(), p.saves()[0]["config"]))
        assert diff == {".p25.enabled": True, ".p25.listen_address": "0.0.0.0:41000",
                        ".dmr.parrot.enabled": True, ".dmr.parrot.talkgroup": 9990,
                        ".dmr.parrot.max_duration": "30s", ".dmr.parrot.gap": "1s"}, diff
    finally:
        p.close()


def check_a_talkgroup_keeps_what_the_page_does_not_show():
    """Break it: build a talkgroup from name, dialled and timeslot alone."""
    p = Page("network")
    try:
        edit(p, '[data-index="1"][data-key="name"]', "Wide area")
        save(p)
        diff = differences(config(), p.saves()[0]["config"])
        assert diff == [(".dmr.join.talkgroups[1].name", "Wide", "Wide area")], diff
        assert p.saves()[0]["config"]["dmr"]["join"]["talkgroups"][1]["arrives"] == 91
    finally:
        p.close()


def check_a_talkgroup_with_no_number_is_refused():
    """It was dropped from the list without a word.

    Break it: filter out rows with no number, as the page did."""
    p = Page("network")
    try:
        p.page.click("#add-talkgroup")
        edit(p, '[data-index="2"][data-key="name"]', "Statewide")
        save(p)
        assert p.saves() == []
        assert "Statewide" in p.page.inner_text("#error")
        # A row added and left empty is nothing, and is not an error.
        p.page.click("#revert")
        p.page.click("#add-talkgroup")
        save(p)
        assert len(p.saves()) == 1
        assert not differences(config(), p.saves()[0]["config"])
    finally:
        p.close()


def check_a_save_is_answered_where_the_button_is():
    """Break it: remove bar.saved, bar.failed or bar.reveal from save()."""
    for viewport in ((1280, 900), (390, 844)):
        p = Page("network", viewport=viewport)
        try:
            p.page.evaluate("window.scrollTo(0, document.body.scrollHeight)")
            edit(p, "#identity-callsign", "w9xyz/")
            save(p, REFUSED)
            assert p.page.inner_text("#save-status").startswith("Not saved")
            assert p.in_view("#save-status") and p.in_view("#error"), \
                "a refusal is off the screen at %r" % (viewport,)
            assert "use letters and digits" in p.page.inner_text("#error")

            p.page.evaluate("window.scrollTo(0, document.body.scrollHeight)")
            save(p, RESTART)
            status = p.page.inner_text("#save-status")
            assert "saved as version 9" in status and "restart" in status.lower(), status
            assert p.in_view("#save-status")
            assert "p25.enabled" in p.page.inner_text("#restart-note")
            assert p.in_view("#restart-note"), "what needs a restart is off the screen"
            # The page now starts from what was saved.
            save(p)
            assert not differences(p.saves()[-2]["config"], p.saves()[-1]["config"])
            assert not differences(p.saves()[-2]["config"], p.saves()[-1]["base"])
        finally:
            p.close()


def check_a_server_error_is_not_an_empty_page():
    """Break it: go back to `return r.json()` in load()."""
    p = Page("network", api={("GET", "/api/config"): lambda rq: (500, {"error": "the database is locked"})})
    try:
        assert p.page.is_visible("#error") and "database is locked" in p.page.inner_text("#error")
        assert not p.page.is_visible("#form")
    finally:
        p.close()
