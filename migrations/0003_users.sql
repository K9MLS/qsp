-- 0003_users
--
-- Administrator accounts and their sessions.
--
-- See docs/adr/ADR-0026-authentication.md. Two things about this schema are
-- decisions rather than shape.
--
-- **The only credential stored is a password hash.** There is no reset token,
-- no security question, no recovery code, and no second copy of anything
-- secret. Each of those is another way for an account to be taken and another
-- row an operator must think about when somebody leaves the club. Recovery is
-- `qsp adduser` on the host, which the operator already has access to because
-- they installed QSP there.
--
-- **Sessions are rows, not signed tokens.** A token that carries its own claims
-- cannot be revoked without keeping a list of the revoked ones, which is this
-- table arriving by a worse route. An administrator who is removed, or a
-- session that is suspected, stops working on the next request.

CREATE TABLE users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    -- Callsigns are the natural name here and are case-insensitive in
    -- practice: nobody thinks K9MLS and k9mls are two people. Stored as
    -- entered, compared folded, unique folded.
    username      TEXT    NOT NULL,
    username_fold TEXT    NOT NULL UNIQUE,
    -- Self-describing, per ADR-0006: the algorithm and its parameters travel
    -- with the hash so that raising the cost later does not invalidate
    -- existing accounts.
    password_hash TEXT    NOT NULL,
    created_at    TEXT    NOT NULL,
    -- Zero until the account is first used. An account created and never used
    -- is worth being able to notice.
    last_login_at TEXT    NOT NULL DEFAULT '',
    -- Failed attempts are counted here rather than in memory, because a
    -- process restart would otherwise be a free reset for whoever is guessing.
    failed_count  INTEGER NOT NULL DEFAULT 0,
    -- When the account stops accepting attempts, empty when it is not
    -- throttled.
    locked_until  TEXT    NOT NULL DEFAULT ''
);

CREATE TABLE sessions (
    -- The cookie value: opaque, random, and the primary key so that a
    -- duplicate is impossible rather than merely unlikely.
    token      TEXT    PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TEXT    NOT NULL,
    expires_at TEXT    NOT NULL,
    -- Recorded so an administrator can recognise a session that is not theirs.
    -- Not used for authorisation: an address changes when somebody moves
    -- between wifi and mobile data, and logging them out for it would train
    -- them to expect it.
    source_ip  TEXT    NOT NULL DEFAULT '',
    user_agent TEXT    NOT NULL DEFAULT ''
);

-- Expiry is swept on a schedule and checked on every request; the index serves
-- the sweep.
CREATE INDEX idx_sessions_expires_at ON sessions (expires_at);
CREATE INDEX idx_sessions_user_id ON sessions (user_id);
