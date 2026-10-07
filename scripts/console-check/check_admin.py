"""The Administration page: what its ten-second refresh may and may not do.

Each check names what it protects. To see one fail, undo the thing its
docstring names in console/static/admin.js and run this again.
"""
from harness import Page


def admin(lookup=True, contact="op@example.org", lifetime=43200):
    return lambda rq: (200, {
        "server": {"network": "Club", "callsign": "W9XYZ", "identifier": "abc", "version": "0.0.0",
                   "started_at": "2026-10-07T08:00:00Z", "uptime_seconds": 7300},
        "agreement": {"agrees": False, "pending": ["dmr.forwarding"], "version": 7, "writable": True},
        "services": {
            "callsigns": {"enabled": lookup, "contact": contact, "usable": lookup, "why": ""},
            "health": {"status": "degraded", "results": [
                {"name": "database", "status": "healthy", "summary": "ok"},
                {"name": "zello-logon", "status": "degraded", "summary": "no credentials"},
                {"name": "dmr", "status": "failing", "summary": "down"},
                {"name": "weather", "status": "healthy", "summary": "ok"}]}},
        "sessions": {"lifetime_seconds": lifetime, "default": True, "active": 2,
                     "expires_in_seconds": 3600, "min_seconds": 300, "max_seconds": 999999},
        "texts": {"available": False},
    })


USERS = lambda rq: (200, {"users": [{"username": "W9AAA", "created": "2026-01-01", "self": True},
                                    {"username": "W9BBB", "created": "2026-01-02"}]})
BOXES = ["callsigns-enabled", "callsigns-contact", "sessions-lifetime"]


def values(p):
    return p.page.evaluate("(ids) => ids.map(i => document.getElementById(i).value)", BOXES)


def open_page(**kw):
    api = {("GET", "/api/admin"): admin(), ("GET", "/api/users"): USERS}
    api.update(kw.pop("api", {}))
    return Page("admin", api=api, clock=True, **kw)


def check_the_refresh_does_not_undo_an_edit():
    """Choose "off", type a contact and a login length, move on, and ten
    seconds later the page had put all three back.

    Break it: in fill(), drop the test against `drawn`."""
    p = open_page()
    try:
        assert values(p) == ["on", "op@example.org", "12h"]
        p.page.select_option("#callsigns-enabled", "off")
        p.page.fill("#callsigns-contact", "new@example.org")
        p.page.fill("#sessions-lifetime", "24h")
        p.page.focus("#user-name")  # the operator has moved on
        before = p.asked("GET", "/api/admin")
        p.pass_time(21)
        assert p.asked("GET", "/api/admin") >= before + 2, "the page did not refresh; nothing was checked"
        assert values(p) == ["off", "new@example.org", "24h"], \
            "two refreshes later the boxes say %r" % values(p)
    finally:
        p.close()


def check_the_refresh_still_brings_news_to_a_box_nobody_touched():
    """Break it: return from fill() whenever the box has been filled once."""
    p = open_page()
    try:
        p.page.fill("#callsigns-contact", "mine@example.org")
        p.page.focus("#user-name")
        p.api[("GET", "/api/admin")] = admin(lookup=False, contact="theirs@example.org", lifetime=86400)
        p.pass_time(11)
        # Somebody else changed all three; the one being edited is kept.
        assert values(p) == ["off", "mine@example.org", "24h"], values(p)
    finally:
        p.close()


def check_a_save_is_sent_as_typed_and_then_the_page_follows_the_server():
    """Break it: leave out saved([...]) after either save."""
    p = open_page()
    try:
        p.page.select_option("#callsigns-enabled", "off")
        p.page.fill("#callsigns-contact", "new@example.org")
        p.page.focus("#user-name")
        p.pass_time(11)
        p.api[("PUT", "/api/admin/callsigns")] = lambda rq: (200, {"callsigns": {"enabled": False}})
        # What the server holds after the save: it tidied the address.
        p.api[("GET", "/api/admin")] = admin(lookup=False, contact="NEW@example.org")
        p.page.click("#callsigns-save")
        p.page.wait_for_timeout(300)
        sent = [b for m, path, b in p.requests if m == "PUT" and path == "/api/admin/callsigns"]
        assert len(sent) == 1 and '"enabled":false' in sent[0] and "new@example.org" in sent[0], sent
        assert values(p)[:2] == ["off", "NEW@example.org"], values(p)

        p.page.fill("#sessions-lifetime", "24h")
        p.page.focus("#user-name")
        p.api[("PUT", "/api/admin/session-lifetime")] = lambda rq: (200, {})
        p.api[("GET", "/api/admin")] = admin(lookup=False, contact="NEW@example.org", lifetime=86400)
        p.page.click("#sessions-save")
        p.page.wait_for_timeout(300)
        sent = [b for m, path, b in p.requests if m == "PUT" and path == "/api/admin/session-lifetime"]
        assert sent == ['{"seconds":86400}'], sent
        p.api[("GET", "/api/admin")] = admin(lookup=False, contact="NEW@example.org", lifetime=3600)
        p.pass_time(11)
        assert values(p)[2] == "1h", "after a save the box no longer follows the server: %r" % values(p)
    finally:
        p.close()


def check_only_what_is_wrong_is_listed_as_not_passing():
    """Every subsystem was listed, on a server with nothing wrong.

    Break it: compare with "passing", which the server never says."""
    p = open_page()
    try:
        facts = p.page.inner_text("#services-facts")
        assert "zello-logon, dmr" in facts, facts
        assert "database" not in facts and "weather" not in facts, facts
    finally:
        p.close()


def check_the_restart_button_does_not_forget_it_was_pressed():
    """Five seconds after the second click the label went back to "Restart
    QSP", on a button that was disabled and restarting it.

    Break it: remove `if (button.disabled) { return; }` from its timer."""
    p = open_page()
    try:
        p.api[("POST", "/api/restart")] = lambda rq: (200, {"note": "QSP is restarting."})
        p.page.click("[data-restart]")
        assert "Everything drops" in p.page.inner_text("[data-restart]")
        p.page.click("[data-restart]")
        p.page.wait_for_timeout(200)
        p.pass_time(6)
        assert p.page.inner_text("[data-restart]") == "Restarting", p.page.inner_text("[data-restart]")
        assert p.page.is_disabled("[data-restart]")
    finally:
        p.close()


def choose_a_backup(p, passphrase="correct horse"):
    p.page.set_input_files("#full-restore-file",
                           files=[{"name": "club.qspfull", "mimeType": "application/octet-stream",
                                   "buffer": b"QSPFULL\x00not a real one"}])
    p.page.fill("#full-restore-passphrase", passphrase)
    p.page.click("#full-restore")
    p.page.wait_for_timeout(300)


def check_a_restore_says_which_password_files_it_will_not_write():
    """The server refuses a password file a backup places outside its own
    directory, and says so before the restore is confirmed.

    Break it: drop refused_password_files from the list in fullRestore()."""
    p = open_page()
    try:
        p.api[("POST", "/api/admin/full-restore")] = lambda rq: (428, {
            "summary": "This backup holds 3 password files; 1 will not be written.",
            "password_files": ["/var/lib/qsp/peers.pw"],
            "refused_password_files": ["/etc/cron.d/qsp"]})
        choose_a_backup(p)
        assert p.in_view("#full-restore-confirm"), "the confirmation did not appear"
        contents = p.page.inner_text("#full-restore-contents")
        assert "will not be written" in contents and "/etc/cron.d/qsp" in contents, contents
    finally:
        p.close()


def check_a_restore_that_failed_keeps_nothing():
    """An answer that was not JSON was shown as the browser's complaint about
    parsing it, and the passphrase stayed in the page.

    Break it: remove the .catch() after r.json() in fullRestore(), or the
    line that empties full-restore-passphrase."""
    p = open_page()
    try:
        p.api[("POST", "/api/admin/full-restore")] = lambda rq: (502, "<html>Bad Gateway</html>")
        choose_a_backup(p)
        said = p.page.inner_text("#full-restore-error")
        assert "502" in said and "JSON" not in said and "token" not in said, said
        assert p.page.input_value("#full-restore-passphrase") == "", "the passphrase is still in the page"
        assert not p.page.is_disabled("#full-restore"), "the button was left disabled"
        assert not p.errors, p.errors
    finally:
        p.close()


def check_a_restore_is_sent_once_however_often_it_is_pressed():
    """Both buttons stayed live while the request was out, and a second press
    sent a second restore.

    Break it: remove the line that disables the buttons in fullRestore()."""
    p = open_page()
    try:
        confirmed = []

        def answer(rq):
            body = rq.post_data_json
            if not body.get("confirm"):
                return (428, {"summary": "This backup holds everything."})
            confirmed.append(1)
            import time
            time.sleep(0.6)
            return (200, {"note": "Restored."})
        p.api[("POST", "/api/admin/full-restore")] = answer
        choose_a_backup(p)
        p.page.evaluate("""() => { const b = document.getElementById('full-restore-confirmed');
                                   b.click(); b.click(); b.click(); }""")
        p.page.wait_for_timeout(2500)
        assert len(confirmed) == 1, "the restore was sent %d times" % len(confirmed)
    finally:
        p.close()
