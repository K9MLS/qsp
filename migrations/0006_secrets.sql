-- 0006_secrets
--
-- Credentials the console accepts and the configuration refers to.
--
-- ADR-0012 keeps secrets out of the configuration document, and ADR-0065 keeps
-- that rule while moving where an operator types them: the console, not a text
-- editor. This table is where a typed credential lands.
--
-- **It is deliberately not in configuration_versions.** That table stores the
-- full JSON document for every save so an operator can inspect, diff and roll
-- back, so a secret written into configuration would appear in every snapshot,
-- every diff and every version shown in the console, in plain text, with an
-- author's name attached. Configuration continues to refer to secrets by name;
-- this table holds them.
--
-- `name` is what a configuration field names, such as `dmr.password` or
-- `transcoder.dvstick.zello`. It is the primary key: one value per name, and a
-- second write replaces the first rather than accumulating history. **History
-- is deliberately absent** — a table of every password a server has ever held
-- is a liability, and rotation means the old value should stop existing.
--
-- `ciphertext` is AES-256-GCM. The nonce is stored with it because GCM needs a
-- distinct one per encryption under the same key and there is nothing to gain
-- from deriving it. The secret's name is the additional authenticated data, so
-- a ciphertext cannot be moved from one name to another: copying the Zello
-- credential into the peer-password row fails to decrypt rather than silently
-- offering the wrong secret to a link.
--
-- `updated_at` and `updated_by` are for the console to show, and because a
-- credential nobody can date is a credential nobody trusts.

CREATE TABLE secrets (
    name        TEXT    PRIMARY KEY,
    nonce       BLOB    NOT NULL,
    ciphertext  BLOB    NOT NULL,
    updated_at  TEXT    NOT NULL,
    updated_by  TEXT    NOT NULL DEFAULT ''
);
