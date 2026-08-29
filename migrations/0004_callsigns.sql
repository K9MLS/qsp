-- 0004_callsigns
--
-- Names for radio IDs, as the registry reported them.
--
-- See docs/adr/ADR-0030-radio-id-lookup.md. The table exists so that a restart
-- does not re-ask the registry for everything QSP already knew, which is
-- precisely the excessive use their data use policy asks callers to avoid.
--
-- **Absences are rows too.** An ID the registry does not know is recorded with
-- `known = 0`, because otherwise every unregistered radio on the network
-- becomes a request on every transmission it makes. Those rows are re-checked
-- after a while, so somebody who registers today is named tomorrow.
--
-- Nothing here is authoritative and none of it is QSP's. It is a cache of
-- somebody else's data, held only for IDs heard on this instance, and deleting
-- the table costs nothing but a few lookups.

CREATE TABLE callsigns (
    -- The DMR radio ID.
    id         INTEGER PRIMARY KEY,
    -- As registered. Any may be empty: the registry holds partial records.
    callsign   TEXT    NOT NULL DEFAULT '',
    name       TEXT    NOT NULL DEFAULT '',
    country    TEXT    NOT NULL DEFAULT '',
    -- Whether the registry had a record at all. A row with known = 0 is a
    -- remembered absence rather than a failed request; a request that failed is
    -- not recorded, because a registry briefly unreachable has said nothing
    -- about the ID and remembering that would hide a real name.
    known      INTEGER NOT NULL DEFAULT 0,
    -- When this was learned, RFC 3339 in UTC. Text that sorts chronologically,
    -- for the same reason the sessions table uses it.
    fetched_at TEXT    NOT NULL
);

-- Absences are re-checked by age, so the sweep reads this rather than the
-- whole table.
CREATE INDEX idx_callsigns_fetched_at ON callsigns (fetched_at);
