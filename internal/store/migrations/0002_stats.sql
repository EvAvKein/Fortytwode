-- 0002_stats: aggregate counters for the landing page (downloads + profiles)
CREATE TABLE IF NOT EXISTS stats (
    id        INT PRIMARY KEY DEFAULT 1,
    downloads BIGINT NOT NULL DEFAULT 0,
    profiles  BIGINT NOT NULL DEFAULT 0
);

INSERT INTO stats DEFAULT VALUES;
