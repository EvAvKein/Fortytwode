-- 0006_account_photos: each account's 42 photo, copied at sync because 42's CDN
-- needs a bearer token to serve it. 'original' is full resolution; 'small' is
-- 42's downsized version, shown in the profile header.
CREATE TABLE IF NOT EXISTS account_photos (
    account_id   BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    variant      TEXT NOT NULL CHECK (variant IN ('original', 'small')),
    content_type TEXT NOT NULL,
    bytes        BYTEA NOT NULL,
    PRIMARY KEY (account_id, variant)
);
