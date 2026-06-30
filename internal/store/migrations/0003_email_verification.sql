-- 0003_email_verification: add email-confirmed actions to accounts, both
-- sign-up email-ownership verification and account-deletion confirmation.
-- Additive and idempotent (IF NOT EXISTS), so it is a safe no-op when re-seen.
--
-- email_verified defaults to false, so every existing account becomes unverified
-- and must verify on next use. verify_token_hash holds the sha256 hex of the
-- active token (never the token itself, so a DB leak can't verify anyone), and
-- verify_sent_at records when the link was issued and backs its 24h TTL. (The
-- resend cooldown is a separate in-memory per-account limiter, not this column.)
--
-- delete_token_hash / delete_requested_at mirror that pair for the deletion-
-- confirmation link: the hash of the active token and when it was issued (24h
-- TTL). They stay NULL until a deletion is requested, and the row is removed
-- outright once the token is confirmed.
ALTER TABLE accounts
    ADD COLUMN IF NOT EXISTS email_verified      BOOLEAN     NOT NULL DEFAULT false,
    ADD COLUMN IF NOT EXISTS verify_token_hash   TEXT,
    ADD COLUMN IF NOT EXISTS verify_sent_at      TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS delete_token_hash   TEXT,
    ADD COLUMN IF NOT EXISTS delete_requested_at TIMESTAMPTZ;

-- Lookups during verification/deletion are by token hash; each partial index
-- covers only the rows with a live token (consumed tokens null the column out).
CREATE INDEX IF NOT EXISTS accounts_verify_token_hash_idx
    ON accounts (verify_token_hash)
    WHERE verify_token_hash IS NOT NULL;

CREATE INDEX IF NOT EXISTS accounts_delete_token_hash_idx
    ON accounts (delete_token_hash)
    WHERE delete_token_hash IS NOT NULL;
