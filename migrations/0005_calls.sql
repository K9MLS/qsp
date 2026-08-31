-- 0005_calls
--
-- Completed transmissions, as a record rather than a display.
--
-- The last-heard list held fifty calls in memory and lost them on restart. That
-- is a display, and it works as one — until a net control station uses it to
-- recover a check-in they missed, which is the use it was actually being put to.
-- Then it is the only record of who was on the net, and it has to hold a whole
-- one and survive the deploy that follows. See docs/adr/ADR-0033.
--
-- One row per completed call, never per frame: a busy club evening is a few
-- hundred rows. Rows are append-only apart from retention pruning, which
-- deletes by age.
--
-- `source` is the radio ID that keyed up rather than the peer ID, because that
-- is the identity an operator recognises and it survives relaying. No audio is
-- stored: QSP carries bursts it never decodes, and a record of who spoke is not
-- a recording of what they said.

CREATE TABLE calls (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    started_at  TEXT    NOT NULL,
    ended_at    TEXT    NOT NULL,
    peer_id     INTEGER NOT NULL,
    stream_id   INTEGER NOT NULL,
    timeslot    INTEGER NOT NULL,
    source      INTEGER NOT NULL,
    target      INTEGER NOT NULL,
    is_group    INTEGER NOT NULL DEFAULT 1,
    voice       INTEGER NOT NULL DEFAULT 0,
    frames      INTEGER NOT NULL DEFAULT 0,
    end_reason  TEXT    NOT NULL DEFAULT ''
);

-- Reading a net back means "everything after this time, newest first", which is
-- the only query this table is expected to answer often.
CREATE INDEX idx_calls_started_at ON calls (started_at DESC);

-- And "when was this station last heard", for a member asking about themselves.
CREATE INDEX idx_calls_source ON calls (source, started_at DESC);
