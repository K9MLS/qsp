-- 0007_p25_calls
--
-- Completed P25 transmissions, as a record: who keyed up, on which talkgroup,
-- and where the call came into QSP. See docs/adr/ADR-0059.
--
-- **A table of its own, not rows in `calls`.** That table is keyed on a
-- repeater ID, a stream ID and a timeslot, all NOT NULL, and a P25 call has
-- none of the three: a gateway announces a callsign and no ID, the protocol
-- carries no stream identifier, and P25 has no timeslots. Storing one there
-- would mean inventing three values, and an invented value is one that can
-- collide with a real one.
--
-- So this migration only adds. Nothing that exists is altered, and a database
-- holding a DMR record keeps every row of it.
--
-- One row per completed call, never per frame. `via_kind` is 'repeater' for a
-- Motorola repeater linked over V.24 and 'gateway' for a hotspot or gateway on
-- the P25 network port; `via_name` is what QSP called it at the time, such as
-- "Quantar, site 1" or a gateway's callsign. `carried` is 0 for a call that
-- was heard and not relayed because another station was talking. No audio is
-- stored: QSP carries voice frames it never decodes.

CREATE TABLE p25_calls (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    started_at  TEXT    NOT NULL,
    ended_at    TEXT    NOT NULL,
    source      INTEGER NOT NULL DEFAULT 0,
    talkgroup   INTEGER NOT NULL DEFAULT 0,
    frames      INTEGER NOT NULL DEFAULT 0,
    via_kind    TEXT    NOT NULL DEFAULT '',
    via_name    TEXT    NOT NULL DEFAULT '',
    carried     INTEGER NOT NULL DEFAULT 1,
    end_reason  TEXT    NOT NULL DEFAULT ''
);

-- "Everything after this time, newest first", the query Last heard and the
-- record page both ask.
CREATE INDEX idx_p25_calls_started_at ON p25_calls (started_at DESC);

-- "When was this radio last heard."
CREATE INDEX idx_p25_calls_source ON p25_calls (source, started_at DESC);
