-- REFERENCE ONLY — this file is NOT executed. The source of truth is the ordered
-- files in ./migrations, applied by store.Migrate. This is a hand-readable snapshot
-- of the current full schema; regenerate it when it drifts with `make schema`.
CREATE TABLE accounts (
    id            SERIAL PRIMARY KEY,
    email         TEXT   UNIQUE NOT NULL,
    password_hash TEXT   NOT NULL,
    ft_id         BIGINT UNIQUE NOT NULL,     -- 42 user id (immutable binding)
    ft_login      TEXT   UNIQUE NOT NULL,     -- public URL key: /u/<ft_login>
    data          JSONB  NOT NULL,            -- snapshot map: { "me": {...}, ... }
    fetched_at    TIMESTAMPTZ NOT NULL,       -- when the snapshot was last synced
    is_public     BOOLEAN NOT NULL DEFAULT false,  -- viewable without an account
    visibility    JSONB  NOT NULL DEFAULT '{}',    -- per-section overrides {"locations":false,...}
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
    id         TEXT PRIMARY KEY,              -- random token (the cookie value)
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL
);

-- Per-42-user sync cooldown: last_sync_at is the start of that user's most recent
-- data fetch. Keyed by ft_id (not account_id) so it also covers anonymous syncs
-- by users who never create an account. Independent of accounts.fetched_at, which
-- tracks snapshot freshness for account holders only.
CREATE TABLE sync_cooldowns (
    ft_id        BIGINT PRIMARY KEY,          -- 42 user id
    last_sync_at TIMESTAMPTZ NOT NULL
);

-- Maintained by the migration runner (store.Migrate), not by a migration file.
-- body stores the exact SQL that ran; the runner compares it on boot to detect a
-- migration edited after it was applied.
CREATE TABLE schema_migrations (
    version    INT PRIMARY KEY,
    name       TEXT NOT NULL,            -- migration filename, e.g. '0001_init.sql'
    body       TEXT NOT NULL,            -- the SQL that ran
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
