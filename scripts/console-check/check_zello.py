"""The Zello page.

Each check names what it protects. To see one fail, undo the thing its
docstring names in console/static/zello.js and run this again.
"""
from harness import Page, config, differences, fresh, serving

SAVED = (200, {"version": 8, "changes": [{"field": "x"}]})
REFUSED = (400, {"error": "the configuration was refused",
                 "fields": [{"field": "dmr.transcoders[0].alias", "problem": "is too long",
                             "fix": "31 characters at most"}]})


def save(page, answer=SAVED):
    page.api[("POST", "/api/config")] = lambda rq: answer
    page.page.click("#save")
    page.page.wait_for_timeout(300)


def edit(page, selector, value):
    page.page.evaluate(
        "([s, v]) => { const e = document.querySelector(s); if (e.type === 'number') e.type = 'text';"
        " e.value = v; e.dispatchEvent(new Event('input', {bubbles: true})); }", [selector, value])


def check_an_unedited_save_changes_nothing():
    """On a server that has never had Zello, saving the page untouched made
    a vocoder channel and a bridge. On one that has it, the save dropped
    where the connector reports its health.

    Break it: write any of the three parts without asking touched()."""
    for name, document in (("a full configuration", config()), ("a fresh install", fresh())):
        p = Page("zello", api={("GET", "/api/config"): serving(document)})
        try:
            assert p.page.inner_text("#save-status") == "", "%s: called unsaved as it opened" % name
            save(p)
            sent = p.saves()
            assert len(sent) == 1, "%s: Save posted %d configurations: %s" % (
                name, len(sent), p.page.inner_text("#error") if p.page.is_visible("#error") else "")
            diff = differences(document, sent[0]["config"])
            assert not diff, "%s: a save with nothing edited changed %r" % (name, diff)
            assert not p.errors, p.errors
        finally:
            p.close()


def check_what_the_page_does_not_show_survives_an_edit():
    """connector_health and audience are Zello settings with no box here.

    Break it: build next.zello from the four boxes alone."""
    p = Page("zello")
    try:
        edit(p, "#zello-channel", "Club Net")
        save(p)
        diff = differences(config(), p.saves()[0]["config"])
        assert diff == [(".zello.channel", "Club", "Club Net")], diff
        assert p.saves()[0]["config"]["zello"]["connector_health"] == "127.0.0.1:18090"
    finally:
        p.close()


def check_the_talkgroup_changes_the_bridge_and_nothing_else():
    """Break it: write the channel whenever the bridge is written."""
    p = Page("zello")
    try:
        edit(p, "#zello-talkgroup", "91")
        save(p)
        diff = differences(config(), p.saves()[0]["config"])
        assert diff == [(".dmr.bridges[1].endpoints[0].talkgroup", 9, 91),
                        (".dmr.bridges[1].endpoints[1].talkgroup", 9, 91)], diff
    finally:
        p.close()


def check_what_cannot_be_read_is_refused_and_named():
    """A level typed as 3,5 was saved as 3, and a repeater ID with a letter
    in it left the list of repeaters that carry Zello.

    Break it: read with parseInt or parseFloat and a fallback of zero."""
    cases = [
        ("#zello-gain-usrp", "3,5", "level toward Zello"),
        ("#zello-gain-dmr", "loud", "level toward the radios"),
        ("#zello-radio-id", "31oo999", "not a DMR ID"),
        ("#zello-talkgroup", "nine", "not a talkgroup number"),
        ("#zello-permit", "310001, 31ooo2", "31ooo2"),
    ]
    for selector, typed, says in cases:
        p = Page("zello")
        try:
            edit(p, selector, typed)
            save(p)
            assert p.saves() == [], "%s = %r was sent to the server" % (selector, typed)
            said = p.page.inner_text("#error")
            assert says in said and "Nothing was saved" in said, "%s = %r: %r" % (selector, typed, said)
            assert p.page.get_attribute(selector, "aria-invalid") == "true"
            assert p.page.inner_text("#save-status").startswith("Not saved: Step")
            assert p.in_view("#save-status") and p.in_view(selector)
            p.page.click("#revert")
            save(p)
            assert len(p.saves()) == 1 and not differences(config(), p.saves()[0]["config"])
        finally:
            p.close()


def check_turning_zello_on_makes_its_channel_and_its_bridge():
    """On a fresh install: the settings, the vocoder channel and the bridge,
    all three, and nothing else.

    Break it: leave `enabled` out of CHANNEL or BRIDGE."""
    p = Page("zello", api={("GET", "/api/config"): serving(fresh())})
    try:
        p.page.locator("#zello-enabled").dispatch_event("click")
        edit(p, "#zello-channel", "Club")
        edit(p, "#zello-issuer", "an-issuer")
        edit(p, "#zello-radio-id", "3100999")
        edit(p, "#zello-talkgroup", "9")
        edit(p, "#zello-permit", "310001")
        save(p)
        sent = p.saves()
        assert len(sent) == 1, p.page.inner_text("#error")
        sent = sent[0]["config"]
        assert sent["zello"]["enabled"] and sent["zello"]["channel"] == "Club"
        assert [t["name"] for t in sent["dmr"]["transcoders"]] == ["zello"]
        assert sent["dmr"]["transcoders"][0]["radio_id"] == 3100999
        assert sent["dmr"]["bridges"] == [{"name": "zello", "enabled": True, "endpoints": [
            {"peer": 310001, "talkgroup": 9, "timeslot": 2},
            {"peer": 0, "transcoder": "zello", "talkgroup": 9, "timeslot": 2}]}], sent["dmr"]["bridges"]
        # Only Zello's own three things are different from the fresh install.
        touched = sorted(set(".".join(d[0].split("[")[0].split(".")[1:3])
                             for d in differences(fresh(), sent)))
        assert all(t in ("zello", "dmr.bridges", "dmr.transcoders") for t in touched), touched
    finally:
        p.close()


def check_a_save_is_answered_where_the_button_is():
    """Break it: remove bar.saved, bar.failed or bar.reveal from save()."""
    for viewport in ((1280, 900), (390, 844)):
        p = Page("zello", viewport=viewport)
        try:
            edit(p, "#zello-alias", "A" * 40)
            p.page.evaluate("window.scrollTo(0, document.body.scrollHeight)")
            save(p, REFUSED)
            assert p.page.inner_text("#save-status").startswith("Not saved")
            assert p.in_view("#save-status") and p.in_view("#error"), \
                "a refusal is off the screen at %r" % (viewport,)
            edit(p, "#zello-alias", "ZELLO GW")
            p.page.evaluate("window.scrollTo(0, document.body.scrollHeight)")
            save(p)
            assert "saved as version 8" in p.page.inner_text("#save-status")
            assert p.in_view("#save-status")
        finally:
            p.close()


def check_the_switch_and_the_list_reach_all_they_govern():
    """The switch turns the settings, the channel and the bridge off
    together; the list of repeaters is the channel's and the bridge's.

    Break it: leave `enabled`, `permitAll` or `permit` out of BRIDGE."""
    p = Page("zello")
    try:
        p.page.locator("#zello-enabled").dispatch_event("click")
        save(p)
        diff = differences(config(), p.saves()[0]["config"])
        assert diff == [(".dmr.bridges[1].enabled", True, False),
                        (".dmr.transcoders[0].enabled", True, False),
                        (".zello.enabled", True, False)], diff
    finally:
        p.close()

    p = Page("zello")
    try:
        edit(p, "#zello-permit", "310001, 310002")
        save(p)
        sent = p.saves()[0]["config"]["dmr"]
        assert sent["transcoders"][0]["permit_peers"] == [310001, 310002]
        assert [e.get("peer") for e in sent["bridges"][1]["endpoints"]] == [310001, 310002, 0]
        assert sent["bridges"][1]["endpoints"][2]["transcoder"] == "zello"
    finally:
        p.close()
