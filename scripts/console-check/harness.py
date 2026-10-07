"""A console page in a real browser, with a stand-in for the server.

Nothing else in the gate chain runs the console's JavaScript: the Go tests
read it as text. The faults that reached operators in October 2026 (a page
that rewrote settings it did not show, a refused save that looked like a
saved one) were all in what the script does, and were found in an hour by
doing this.

Every request the page makes is answered here, from the files in
console/static and from the dictionary a check passes in, so no QSP server
runs and nothing leaves the machine.
"""
import copy
import json
import mimetypes
import os
from urllib.parse import urlparse

from playwright.sync_api import sync_playwright

HERE = os.path.dirname(os.path.abspath(__file__))
STATIC = os.path.normpath(os.path.join(HERE, "..", "..", "console", "static"))
CONFIG = json.load(open(os.path.join(HERE, "config.json")))
FRESH = json.load(open(os.path.join(HERE, "fresh.json")))


def config():
    """A configuration with something in every part a page edits."""
    return copy.deepcopy(CONFIG)


def fresh():
    """The configuration of a server just installed: `qsp -print-config`."""
    return copy.deepcopy(FRESH)


def serving(document):
    """An answer to GET /api/config that serves a copy of document."""
    return lambda rq: (200, {"config": copy.deepcopy(document), "writable": True})


class Page:
    """One console page, open, with every request it made on record."""

    def __init__(self, name, api=None, viewport=(1280, 900)):
        self.requests = []
        self.errors = []
        self.api = {
            ("GET", "/api/config"): lambda rq: (200, {"config": config(), "writable": True}),
            ("GET", "/api/session"): lambda rq: (200, {"authenticated": True, "username": "W9AAA",
                                                        "version": "0.0.0"}),
        }
        self.api.update(api or {})
        self._pw = sync_playwright().start()
        self._browser = self._pw.chromium.launch()
        self.page = self._browser.new_page(viewport={"width": viewport[0], "height": viewport[1]})
        self.page.on("pageerror", lambda e: self.errors.append(str(e)))
        self.page.on("dialog", lambda d: d.accept())
        self.page.route("**/*", self._answer)
        self.page.goto("http://qsp.test/" + name)
        self.page.wait_for_timeout(600)

    def _answer(self, route, request):
        url = urlparse(request.url)
        if url.hostname != "qsp.test":
            return route.fulfill(status=404, body="")
        if url.path.startswith("/api/") or url.path == "/healthz":
            self.requests.append((request.method, url.path, request.post_data))
            if url.path == "/api/events":
                return route.fulfill(status=200, content_type="text/event-stream", body=": hi\n\n")
            answer = self.api.get((request.method, url.path))
            if answer is None:
                return route.fulfill(status=200, content_type="application/json", body="{}")
            status, body = answer(request)
            return route.fulfill(status=status, content_type="application/json",
                                 body=body if isinstance(body, str) else json.dumps(body))
        name = url.path.lstrip("/") or "index"
        path = os.path.join(STATIC, name if "." in name else name + ".html")
        if not os.path.isfile(path):
            return route.fulfill(status=404, body="")
        return route.fulfill(status=200, body=open(path, "rb").read(),
                             content_type=mimetypes.guess_type(path)[0] or "text/plain")

    def saves(self):
        """Every configuration the page posted, oldest first."""
        return [json.loads(body) for method, path, body in self.requests
                if method == "POST" and path == "/api/config" and body]

    def in_view(self, selector):
        """Whether an element is drawn inside the window, where it can be read."""
        return self.page.evaluate(
            """(s) => { const e = document.querySelector(s);
                 if (!e || e.hidden || !e.getClientRects().length) return false;
                 const r = e.getBoundingClientRect();
                 return r.bottom > 0 && r.top < window.innerHeight && r.height > 0; }""", selector)

    def close(self):
        self._browser.close()
        self._pw.stop()


def differences(a, b, path=""):
    """Where two documents differ, as (path, in a, in b)."""
    if isinstance(a, dict) and isinstance(b, dict):
        out = []
        for key in sorted(set(a) | set(b)):
            if key not in a:
                out.append((path + "." + key, "<absent>", b[key]))
            elif key not in b:
                out.append((path + "." + key, a[key], "<absent>"))
            else:
                out += differences(a[key], b[key], path + "." + key)
        return out
    if isinstance(a, list) and isinstance(b, list) and len(a) == len(b):
        out = []
        for i, (x, y) in enumerate(zip(a, b)):
            out += differences(x, y, "%s[%d]" % (path, i))
        return out
    return [] if a == b else [(path, a, b)]
