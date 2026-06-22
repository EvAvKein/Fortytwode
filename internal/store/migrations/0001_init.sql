-- 0001_init: the original schema. Kept with IF NOT EXISTS so this migration is a
-- safe no-op on an already-populated database (an existing deployment simply gets
-- stamped at version 1 the first time the runner sees it).
CREATE TABLE IF NOT EXISTS accounts (
    id            SERIAL PRIMARY KEY,
    email         TEXT   UNIQUE NOT NULL,
    password_hash TEXT   NOT NULL,
    ft_id         BIGINT UNIQUE NOT NULL,     -- 42 user id (immutable binding)
    ft_login      TEXT   UNIQUE NOT NULL,     -- public URL key: /users/<ft_login>
    data          JSONB  NOT NULL,            -- snapshot map: { "me": {...}, ... }
    fetched_at    TIMESTAMPTZ NOT NULL,       -- when the snapshot was last synced
    is_public     BOOLEAN NOT NULL DEFAULT false,  -- viewable without an account
    visibility    JSONB  NOT NULL DEFAULT '{}',    -- per-section overrides {"locations":false,...}
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS sessions (
    id         TEXT PRIMARY KEY,              -- random token (the cookie value)
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL
);

-- Per-42-user sync cooldown: last_sync_at is the start of that user's most recent
-- data fetch. Keyed by ft_id (not account_id) so it also covers anonymous syncs
-- by users who never create an account. Independent of accounts.fetched_at, which
-- tracks snapshot freshness for account holders only.
CREATE TABLE IF NOT EXISTS sync_cooldowns (
    ft_id        BIGINT PRIMARY KEY,          -- 42 user id
    last_sync_at TIMESTAMPTZ NOT NULL
);
