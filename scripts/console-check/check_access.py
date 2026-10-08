"""The Access control page.

Each check names what it protects. To see one fail, undo the thing its
docstring names in console/static/access.js and run this again.
"""
from harness import Page, config, differences, fresh, serving

SAVED = (200, {"version": 8, "changes": [{"field": "x"}]})
REFUSED = (400, {"error": "the configuration was refused",
                 "fields": [{"field": "dmr.access.registration.ids[1]",
                             "problem": "\"31oo\" is not an ID", "fix": "use digits"}]})


def save(page, answer=SAVED):
    page.api[("POST", "/api/config")] = lambda rq: answer
    page.page.click("#save")
    page.page.wait_for_timeout(300)


def edit(page, selector, value):
    box = page.page.locator(selector)
    box.fill(value)
    box.dispatch_event("input")


def check_an_unedited_save_changes_nothing():
    """On a fresh install the page used to write four empty access lists
    into a configuration that had no access block at all.

    Break it: in collect(), write a list without comparing it with shown."""
    for name, document in (("a full configuration", config()), ("a fresh install", fresh())):
        p = Page("access", api={("GET", "/api/config"): serving(document)})
        try:
            assert p.page.inner_text("#save-status") == "", "%s: called unsaved as it opened" % name
            save(p)
            sent = p.saves()
            assert len(sent) == 1
            diff = differences(document, sent[0]["config"])
            assert not diff, "%s: a save with nothing edited changed %r" % (name, diff)
            assert not p.errors, p.errors
        finally:
            p.close()


def check_one_list_edited_is_one_list_changed():
    """Break it: write all four lists whenever one is edited."""
    for name, document in (("a full configuration", config()), ("a fresh install", fresh())):
        p = Page("access", api={("GET", "/api/config"): serving(document)})
        try:
            edit(p, "#acl-subscribers-ids", "3101234\n3105555")
            assert p.page.inner_text("#save-status") == "Changes not saved yet."
            save(p)
            sent = p.saves()[0]["config"]
            assert sent["dmr"]["access"]["subscribers"] == {"mode": "deny", "ids": ["3101234", "3105555"]}
            touched = sorted(set(d[0].split(".ids")[0] for d in differences(document, sent)))
            assert all(t.startswith(".dmr.access") and "subscribers" in t or t == ".dmr.access"
                       for t in touched), "%s: %r" % (name, touched)
            if name == "a full configuration":
                assert touched == [".dmr.access.subscribers"], touched
        finally:
            p.close()


def check_a_mode_is_a_change_too():
    """Break it: leave the radio buttons out of listNow()."""
    p = Page("access")
    try:
        p.page.locator('input[name="acl-ts1-mode"][value="permit"]').check()
        assert p.page.inner_text("#save-status") == "Changes not saved yet."
        save(p)
        diff = differences(config(), p.saves()[0]["config"])
        assert diff == [(".dmr.access.talkgroups.timeslot_1.mode", "deny", "permit")], diff
    finally:
        p.close()


def check_a_repeater_line_that_cannot_be_read_is_refused():
    """A callsign typed before its ID, or an ID with a letter in it, was
    skipped, and that repeater left the allow list with the answer "saved".

    Break it: `return` from a bad line in parseIPSC without recording it."""
    for typed, says in (("3101 W9AAA\nW9BBB 3102", "line 2"), ("3101\n31o2 W9BBB", "31o2"),
                        ("0 W9AAA", "line 1")):
        p = Page("access")
        try:
            edit(p, "#acl-ipsc-ids", typed)
            save(p)
            assert p.saves() == [], "%r was sent to the server" % typed
            said = p.page.inner_text("#error")
            assert says in said and "no repeater has been dropped" in said, said
            assert p.page.get_attribute("#acl-ipsc-ids", "aria-invalid") == "true"
            assert p.page.inner_text("#save-status").startswith("Not saved: Allowed repeaters")
            assert p.in_view("#save-status") and p.in_view("#acl-ipsc-ids")
        finally:
            p.close()


def check_repeaters_and_their_names_are_saved_together():
    """Break it: write allowed_peers and forget peer_names."""
    p = Page("access")
    try:
        edit(p, "#acl-ipsc-ids", "3101 W9AAA\n3103 W9CCC\n3104")
        save(p)
        ipsc = p.saves()[0]["config"]["ipsc"]
        assert ipsc["allowed_peers"] == [3101, 3103, 3104], ipsc["allowed_peers"]
        assert ipsc["peer_names"] == {"3101": "W9AAA", "3103": "W9CCC"}, ipsc["peer_names"]
    finally:
        p.close()


def check_a_save_is_answered_where_the_button_is():
    """Break it: remove bar.saved, bar.failed or bar.reveal from save()."""
    for viewport in ((1280, 900), (390, 844)):
        p = Page("access", viewport=viewport)
        try:
            edit(p, "#acl-registration-ids", "310001\n31oo")
            p.page.evaluate("window.scrollTo(0, document.body.scrollHeight)")
            save(p, REFUSED)
            assert p.page.inner_text("#save-status").startswith("Not saved")
            assert p.in_view("#save-status") and p.in_view("#error"), \
                "a refusal is off the screen at %r" % (viewport,)
            # What was typed is still there to mend.
            assert p.page.input_value("#acl-registration-ids") == "310001\n31oo"

            edit(p, "#acl-registration-ids", "310001\n3100-3199\n3200")
            p.page.evaluate("window.scrollTo(0, document.body.scrollHeight)")
            save(p)
            assert "saved as version 8" in p.page.inner_text("#save-status")
            assert p.in_view("#save-status")
            save(p)
            assert not differences(p.saves()[-2]["config"], p.saves()[-1]["config"])
            assert not differences(p.saves()[-2]["config"], p.saves()[-1]["base"])
        finally:
            p.close()


def check_a_server_error_is_not_four_empty_lists():
    """Break it: go back to `return r.json()` in load()."""
    p = Page("access", api={("GET", "/api/config"): lambda rq: (500, {"error": "the database is locked"})})
    try:
        assert p.page.is_visible("#load-error") and "database is locked" in p.page.inner_text("#load-error")
        assert not p.page.is_visible("#error"), "a page that could not load says it did not save"
        assert not p.page.is_visible("#form")
    finally:
        p.close()
