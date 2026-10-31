-- Initial relational schema for the URL shortener.
-- gen_random_uuid() is built into PostgreSQL 13+; local development uses PostgreSQL 16.

CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT users_email_not_blank CHECK (length(trim(email)) > 0),
    CONSTRAINT users_password_hash_not_blank CHECK (length(password_hash) > 0)
);

-- Enforce case-insensitive uniqueness even if a caller bypasses app normalization.
CREATE UNIQUE INDEX users_email_lower_unique_idx ON users (lower(email));

CREATE TABLE urls (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    short_code VARCHAR(32) NOT NULL,
    original_url TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    CONSTRAINT urls_short_code_length CHECK (length(short_code) BETWEEN 4 AND 32),
    CONSTRAINT urls_original_url_length CHECK (length(original_url) BETWEEN 1 AND 2048),
    CONSTRAINT urls_expiry_after_creation CHECK (expires_at IS NULL OR expires_at > created_at)
);

CREATE UNIQUE INDEX urls_short_code_unique_idx ON urls (short_code);
CREATE INDEX urls_user_created_id_idx ON urls (user_id, created_at DESC, id DESC);
CREATE INDEX urls_active_expiry_idx ON urls (expires_at)
    WHERE is_active = TRUE AND expires_at IS NOT NULL;
