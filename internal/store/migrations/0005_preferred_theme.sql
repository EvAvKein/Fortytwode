-- 0005_preferred_theme: per-account theme override for the site's light/dark themes.
-- NULL = no override (follow the OS via the CSS prefers-color-scheme media query),
-- otherwise an explicit 'light' | 'dark'.
ALTER TABLE accounts
    ADD COLUMN IF NOT EXISTS preferred_theme TEXT
        CHECK (preferred_theme IN ('light', 'dark'));
