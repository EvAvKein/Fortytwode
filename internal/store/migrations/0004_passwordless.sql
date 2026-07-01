-- 0004_passwordless: drop password auth (login is now an emailed one-time link plus
-- 42 OAuth) and add the columns backing the two email-link flows. Idempotent: the
-- password_hash drop is guarded by IF EXISTS, the new columns by IF NOT EXISTS.
--
-- login_*: token hash + issue time for the magic-link login (mirrors verify_*).
-- pending_email / email_change_*: the requested new address parked with its token
-- hash + issue time for the confirm-first email change. All stay NULL between uses.
ALTER TABLE accounts DROP COLUMN IF EXISTS password_hash;

ALTER TABLE accounts
    ADD COLUMN IF NOT EXISTS login_token_hash        TEXT,
    ADD COLUMN IF NOT EXISTS login_sent_at           TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS pending_email           TEXT,
    ADD COLUMN IF NOT EXISTS email_change_token_hash TEXT,
    ADD COLUMN IF NOT EXISTS email_change_sent_at    TIMESTAMPTZ;

-- Lookups during login/email-change consumption are by token hash; each partial
-- index covers only the rows with a live token (consumed tokens null the column out).
CREATE INDEX IF NOT EXISTS accounts_login_token_hash_idx
    ON accounts (login_token_hash)
    WHERE login_token_hash IS NOT NULL;

CREATE INDEX IF NOT EXISTS accounts_email_change_token_hash_idx
    ON accounts (email_change_token_hash)
    WHERE email_change_token_hash IS NOT NULL;
