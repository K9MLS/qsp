"""The Weather page: finding county and zone codes by name.

NWS retired alerts.weather.gov in December 2025, which is where this page
sent operators to look their codes up. Each check names what it protects;
to see one fail, undo the thing its docstring names in console/static/weather.js.
"""
import json

from harness import Page

WEATHER = lambda rq: (200, {"available": True, "status": {"enabled": False},
                            "default_events": ["Tornado Warning"], "max_characters": 280})

TEXAS = [
    {"code": "TXC085", "name": "Collin", "state": "TX", "kind": "county"},
    {"code": "TXC121", "name": "Denton", "state": "TX", "kind": "county"},
    {"code": "TXZ104", "name": "Collin", "state": "TX", "kind": "forecast"},
    {"code": "TXZ103", "name": "Denton", "state": "TX", "kind": "forecast"},
]


def area(rq):
    body = json.loads(rq.post_data)
    if body.get("state") != "TX":
        return (502, {"error": "weather: the National Weather Service has no county list for " + body["state"]})
    return (200, {"state": "TX", "zones": TEXAS})


def open_page():
    return Page("weather", api={("GET", "/api/weather"): WEATHER, ("POST", "/api/weather/area"): area})


def rows(p):
    return p.page.locator("#weather-zone-list li").all_inner_texts()


def check_a_state_lists_its_codes_by_name_and_add_fills_the_box():
    """Break it: in findCodes, do not draw what came back."""
    p = open_page()
    try:
        p.page.select_option("#weather-state", "TX")
        p.page.click("#weather-find")
        p.page.wait_for_timeout(300)
        got = rows(p)
        assert len(got) == 4, got
        assert "Denton County" in got[1] and "TXC121" in got[1], got
        assert "Denton (forecast zone)" in got[3] and "TXZ103" in got[3], got

        p.page.fill("#weather-zones", "")
        p.page.click("#weather-zone-list li:nth-child(2) button")
        p.page.click("#weather-zone-list li:nth-child(4) button")
        assert p.page.input_value("#weather-zones") == "TXC121, TXZ103", p.page.input_value("#weather-zones")
        assert p.page.inner_text("#weather-zone-list li:nth-child(2) button") == "Added"
        assert p.page.is_disabled("#weather-zone-list li:nth-child(2) button")
        assert not p.errors, p.errors
    finally:
        p.close()


def check_the_list_narrows_as_a_name_is_typed():
    """Break it: ignore the filter in drawFound."""
    p = open_page()
    try:
        p.page.select_option("#weather-state", "TX")
        p.page.click("#weather-find")
        p.page.wait_for_timeout(300)
        p.page.fill("#weather-filter", "collin")
        got = rows(p)
        assert len(got) == 2 and all("Collin" in r for r in got), got
        assert "2 of 4" in p.page.inner_text("#weather-find-note")
    finally:
        p.close()


def check_nws_saying_no_is_said_on_the_page():
    """Break it: show nothing when the answer is not 200."""
    p = open_page()
    try:
        p.page.select_option("#weather-state", "GU")
        p.page.click("#weather-find")
        p.page.wait_for_timeout(300)
        assert "no county list for GU" in p.page.inner_text("#weather-find-note")
        assert rows(p) == []
        assert not p.page.is_disabled("#weather-find")
    finally:
        p.close()


def check_the_retired_site_is_not_linked():
    """The page sent operators to alerts.weather.gov, which NWS retired."""
    p = open_page()
    try:
        links = p.page.eval_on_selector_all("a", "as => as.map(a => a.href)")
        assert not [h for h in links if "alerts.weather.gov" in h], links
    finally:
        p.close()
