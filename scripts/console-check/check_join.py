"""The Join page: what a member reads before asking why a text went missing.

Each check names what it protects. To see one fail, undo the thing its
docstring names in console/static/join.html and run this again.
"""
from harness import Page


def check_the_page_says_which_text_format_to_use():
    """On 2026-10-24 a member's BTech, set to "DMR Standard", texted his
    MOTOTRBO 6550 and nothing showed; the network was suspected first. The
    page names the format to choose and what each one reaches.

    Break it: delete the Text messages section from join.html."""
    p = Page("join")
    try:
        assert p.page.is_visible("#texting"), "there is no Text messages section"
        rows = p.page.locator("#texting .fields--formats tbody tr").all_inner_texts()
        assert len(rows) == 3, rows
        assert rows[0].startswith("M-SMS") and "Use this" in rows[0], rows[0]
        assert "Motorola radios show nothing" in rows[1], rows[1]
        notes = p.page.inner_text("#texting .extra__list")
        assert "sender's setting" in notes and "CAI network" in notes and "rewrite rules" in notes, notes
        assert not p.errors, p.errors
    finally:
        p.close()


def check_the_allstar_guide_is_folded_and_filled_in():
    """A node owner needs this server's address and port in MMDVM_Bridge.ini,
    the same values a hotspot uses; most readers of this page bring a
    hotspot, so the section starts closed.

    Break it: in join.js, stop filling allstar-address."""
    join = lambda rq: (200, {"enabled": True, "settings": {"address": "qsp.example.org", "port": 62031}})
    p = Page("join", api={("GET", "/api/join"): join})
    try:
        assert not p.page.evaluate("() => document.getElementById('allstar').open"), "it starts open"
        assert not p.page.is_visible("#allstar-address"), "its values show while it is closed"
        p.page.click("#allstar summary")
        p.page.wait_for_timeout(100)
        assert p.page.inner_text("#allstar-address") == "qsp.example.org", p.page.inner_text("#allstar-address")
        assert p.page.inner_text("#allstar-port") == "62031", p.page.inner_text("#allstar-port")
        assert "has not linked a node this way yet" in p.page.inner_text("#allstar"), \
            "it does not say this is untried"
        assert not p.errors, p.errors
    finally:
        p.close()
