-- GitHub webhook secrets are stored as issued instead of sealed with a server
-- key (WEBHOOK_ENCRYPTION_KEY is gone). Webhooks created before this cannot be
-- unsealed any more, so their secret is empty until the owner rotates it.
ALTER TABLE github_webhooks ADD COLUMN secret text CHECK (secret IS NULL OR length(secret) BETWEEN 32 AND 200);
ALTER TABLE github_webhooks DROP COLUMN secret_sealed;
