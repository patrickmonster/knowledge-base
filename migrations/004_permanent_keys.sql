-- NULL means no time-based expiration. Revocation and user permissions still apply.
-- Existing key expiration dates and the 30-day database default remain unchanged.
ALTER TABLE api_keys ALTER COLUMN expires_at DROP NOT NULL;
