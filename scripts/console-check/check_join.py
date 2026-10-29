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


def check_the_page_says_what_dvswitch_is_for_and_where_the_guide_is():
    """An AllStar owner reading the Join page needs to learn in a paragraph
    what DVSwitch gets them, and find the rest; the settings live on the
    guide, not here.

    Break it: delete the DVSwitch section from join.html, or point its link
    anywhere but /dvswitch."""
    p = Page("join")
    try:
        body = p.page.inner_text("#dvswitch")
        assert "AllStar" in body and "FM radios" in body, body
        assert p.page.get_attribute("#dvswitch a.extra__go", "href") == "/dvswitch"
        assert not p.errors, p.errors
    finally:
        p.close()
