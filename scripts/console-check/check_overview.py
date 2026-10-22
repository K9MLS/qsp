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


def repeaters_answer(rtrt_disabled):
    """The two repeaters of 2026-10-22, as /api/peers carries them."""
    yours = {"router": "192.168.1.44", "up": True, "site": 1, "type": "Quantar", "calls": 2,
             "frames": 234, "relayed": 234, "sent": 171, "talkgroup": 1, "source_id": 8080303,
             "heard": True, "last_heard_ago_seconds": 40}
    theirs = {"router": "192.0.2.60", "up": True, "site": 1, "type": "Quantar", "calls": 3,
              "frames": 549, "relayed": 549, "sent": 234, "talkgroup": 1, "source_id": 8080305,
              "heard": True, "last_heard_ago_seconds": 12, "rtrt_disabled": rtrt_disabled}
    return lambda rq: (200, {"enabled": False, "peers": [], "active": [], "recent": [], "refused": [],
                             "traffic": {"p25": {"gateways_off": True, "repeaters": [yours, theirs]}}})


def check_a_repeater_marked_rtrt_disabled_is_named_with_the_fix():
    """On 2026-10-22 one repeater's calls reached the other intact and were
    never transmitted. Its calls were marked RT/RT disabled, which took a
    packet capture to see. The page says so, in amber, naming the repeater.

    Break it: in p25Markup, drop the p25Warnings.forEach that writes them."""
    p = Page("", api={("GET", "/api/peers"): repeaters_answer(True)})
    try:
        warnings = p.page.locator("[data-p25-warning]").all_inner_texts()
        assert len(warnings) == 1, warnings
        assert "192.0.2.60" in warnings[0] and "RT/RT Configuration to ENABLED" in warnings[0], warnings[0]
        assert "192.168.1.44" not in warnings[0], warnings[0]
        assert not p.errors, p.errors
    finally:
        p.close()


def check_no_rtrt_warning_when_both_are_enabled():
    """A field the server leaves out is not a warning.

    Break it: push the warning whatever r.rtrt_disabled says."""
    p = Page("", api={("GET", "/api/peers"): repeaters_answer(False)})
    try:
        assert p.page.locator("[data-p25-warning]").count() == 0
        assert "234 sent to it" in p.page.inner_text("body"), "the relayed count is not shown"
        assert not p.errors, p.errors
    finally:
        p.close()
