-- 0001_configuration_versions
--
-- Configuration version history.
--
-- The blueprint requires that every configuration save produce an immutable
-- snapshot an operator can inspect, diff and roll back to. This table is that
-- history. Rows are never updated or deleted by the application; a rollback
-- records a new version whose content matches an older one.
--
-- `document` holds the full JSON configuration. `checksum` is the SHA-256 of
-- that document, allowing two versions to be compared without decoding them.

CREATE TABLE configuration_versions (
    number      INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at  TEXT    NOT NULL,
    author      TEXT    NOT NULL,
    summary     TEXT    NOT NULL DEFAULT '',
    checksum    TEXT    NOT NULL,
    document    TEXT    NOT NULL,
    schema_ver  INTEGER NOT NULL
);

CREATE INDEX idx_configuration_versions_created_at
    ON configuration_versions (created_at DESC);
