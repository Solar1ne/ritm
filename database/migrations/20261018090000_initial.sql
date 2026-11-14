-- Additive initial schema. Forward-only: no destructive down migration.
CREATE TABLE rhythm_identities (
    id text PRIMARY KEY CHECK (id ~ '^[gtu]_[a-f0-9]{64}$'),
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE rhythm_diaries (
    diary_key text PRIMARY KEY REFERENCES rhythm_identities(id),
    owner text NOT NULL UNIQUE REFERENCES rhythm_identities(id),
    revision bigint NOT NULL CHECK (revision >= 0),
    document jsonb NOT NULL CHECK (jsonb_typeof(document) = 'object'),
    updated_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CHECK (diary_key ~ '^[gt]_[a-f0-9]{64}$'),
    CHECK (owner = diary_key OR (diary_key LIKE 'g_%' AND owner = 'u_' || substr(diary_key, 3))),
    CHECK (document ? 'revision' AND jsonb_typeof(document->'revision') = 'number' AND (document->>'revision')::numeric = revision),
    CHECK (octet_length(document::text) <= 4194304)
);

CREATE TABLE rhythm_accounts (
    principal text PRIMARY KEY REFERENCES rhythm_identities(id),
    diary_key text GENERATED ALWAYS AS ('g_' || substr(principal, 3)) STORED REFERENCES rhythm_diaries(diary_key),
    login text NOT NULL UNIQUE,
    password_salt text NOT NULL,
    password_hash text NOT NULL,
    password_iterations integer NOT NULL CHECK (password_iterations = 600000),
    recovery_digest text NOT NULL CHECK (recovery_digest ~ '^[a-f0-9]{64}$'),
    created_at timestamptz NOT NULL,
    CHECK (principal ~ '^u_[a-f0-9]{64}$'),
    CHECK (login ~ '^[a-z0-9._-]{3,32}$')
);

CREATE TABLE rhythm_sessions (
    digest text PRIMARY KEY CHECK (digest ~ '^[a-f0-9]{64}$'),
    principal text NOT NULL REFERENCES rhythm_identities(id),
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL CHECK (expires_at > created_at)
);
CREATE INDEX rhythm_sessions_principal ON rhythm_sessions (principal, created_at);
CREATE INDEX rhythm_sessions_expiry ON rhythm_sessions (expires_at);

CREATE TABLE rhythm_legacy_revocations (
    principal text PRIMARY KEY REFERENCES rhythm_identities(id),
    expires_at timestamptz NOT NULL
);
CREATE INDEX rhythm_revocations_expiry ON rhythm_legacy_revocations (expires_at);

CREATE TABLE rhythm_metadata (
    key text PRIMARY KEY,
    value bytea NOT NULL
);

CREATE TABLE rhythm_imports (
    fingerprint text PRIMARY KEY CHECK (fingerprint ~ '^[a-f0-9]{64}$'),
    diary_count integer NOT NULL CHECK (diary_count >= 0),
    account_count integer NOT NULL CHECK (account_count >= 0),
    imported_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP
);
