-- 000001_init.up.sql — remote node initial schema (plan T09).
-- Version rows are immutable snapshots; soft delete writes a tombstone
-- revision and sets documents.deleted_at. Physical R2 content is never
-- deleted by these tables.

CREATE TABLE auth_allowlist (
    email       TEXT PRIMARY KEY,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id          UUID PRIMARY KEY,
    email       TEXT NOT NULL UNIQUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE otp_challenges (
    id          UUID PRIMARY KEY,
    email       TEXT NOT NULL,
    code_hmac   TEXT NOT NULL,
    attempts    INTEGER NOT NULL DEFAULT 0,
    expires_at  TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
    id          UUID PRIMARY KEY,
    user_id     UUID NOT NULL REFERENCES users(id),
    token_hash  TEXT NOT NULL UNIQUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL,
    revoked_at  TIMESTAMPTZ
);
CREATE INDEX sessions_user ON sessions(user_id);

CREATE TABLE documents (
    id               UUID PRIMARY KEY,
    owner_id         UUID NOT NULL REFERENCES users(id),
    current_revision INTEGER NOT NULL,
    title            TEXT NOT NULL,
    source_url       TEXT NOT NULL DEFAULT '',
    deleted_at       TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX documents_owner_listing ON documents(owner_id, deleted_at, updated_at DESC, id);

CREATE TABLE document_versions (
    document_id UUID    NOT NULL REFERENCES documents(id),
    revision    INTEGER NOT NULL,
    operation   TEXT    NOT NULL CHECK (operation IN ('create','update_meta','update_content','delete')),
    title       TEXT    NOT NULL,
    source_url  TEXT    NOT NULL DEFAULT '',
    sha256      TEXT    NOT NULL DEFAULT '',
    blob_key    TEXT    NOT NULL DEFAULT '',
    size_bytes  BIGINT  NOT NULL DEFAULT 0,
    mime_type   TEXT    NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (document_id, revision)
);
