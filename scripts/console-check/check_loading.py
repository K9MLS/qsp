"""Pages that load something, and a server that refuses.

A refusal was read as an answer: the record said nobody had transmitted, the
history that nothing had been saved, the links page that there were no
links. Each check names what it protects; to see them fail, have get.js
return the body of an answer that was not ok.
"""
from harness import Page

REFUSAL = lambda rq: (500, {"error": "database is locked"})
PROXY = lambda rq: (502, "<html><body>Bad Gateway</body></html>")

PAGES = [
    ("record", ("GET", "/api/calls")),
    ("history", ("GET", "/api/config/versions")),
    ("links", ("GET", "/api/links")),
    ("zello", ("GET", "/api/config")),
    ("weather", ("GET", "/api/config")),
]


def check_every_page_says_it_could_not_load():
    """Break it: in get.js, drop the r.ok test."""
    for name, call in PAGES:
        for refusal, says in ((REFUSAL, "database is locked"), (PROXY, "502")):
            p = Page(name, api={call: refusal})
            try:
                assert p.page.is_visible("#load-error"), "%s: a refusal was shown as an answer" % name
                said = p.page.inner_text("#load-error")
                assert says in said, "%s: the notice says %r" % (name, said)
                assert not p.page.is_visible("#loading"), "%s: still says it is loading" % name
                assert not p.errors, "%s: %s" % (name, p.errors)
            finally:
                p.close()


def check_a_page_that_loads_says_nothing_of_the_kind():
    """Break it: show the notice whatever happens."""
    answers = {
        ("GET", "/api/calls"): lambda rq: (200, {"calls": [], "since": "2026-10-08T00:00:00Z"}),
        ("GET", "/api/config/versions"): lambda rq: (200, {"versions": []}),
        ("GET", "/api/links"): lambda rq: (200, {"links": []}),
        ("GET", "/api/weather"): lambda rq: (200, {"available": True, "status": {}}),
    }
    for name, _ in PAGES:
        p = Page(name, api=answers)
        try:
            assert not p.page.is_visible("#load-error"), "%s: a page that loaded says it did not" % name
        finally:
            p.close()
