-- 0002_audit_events
--
-- Administrative audit trail.
--
-- Clubs run QSP with several administrators. When a routing change causes an
-- argument, the audit trail settles it. Rows are append-only: the application
-- never updates or deletes them.
--
-- `actor` is a username, never a credential or session token. `detail` is a
-- JSON object; the audit recorder is responsible for ensuring no secret is
-- placed in it.

CREATE TABLE audit_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    occurred_at TEXT    NOT NULL,
    actor       TEXT    NOT NULL,
    action      TEXT    NOT NULL,
    subject     TEXT    NOT NULL DEFAULT '',
    outcome     TEXT    NOT NULL,
    source_ip   TEXT    NOT NULL DEFAULT '',
    detail      TEXT    NOT NULL DEFAULT '{}'
);

CREATE INDEX idx_audit_events_occurred_at ON audit_events (occurred_at DESC);
CREATE INDEX idx_audit_events_actor       ON audit_events (actor, occurred_at DESC);
