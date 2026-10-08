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
