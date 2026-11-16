-- Discord slash commands over the Interactions Endpoint (request/response).
-- Nothing here is secret: the application public key only verifies Discord's
-- signatures, and link codes are stored as hashes.

-- Server-wide settings, changed by a global administrator in the web console.
CREATE TABLE discord_settings (
  singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  enabled boolean NOT NULL DEFAULT false,
  public_key text NOT NULL DEFAULT '' CHECK (public_key ~ '^([0-9a-f]{64})?$'),
  allowed_guild_ids text[] NOT NULL DEFAULT '{}' CHECK (cardinality(allowed_guild_ids) <= 20),
  updated_by uuid REFERENCES users(id) ON DELETE SET NULL,
  updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- One Discord account per person, one person per Discord account.
CREATE TABLE discord_links (
  discord_user_id text PRIMARY KEY CHECK (discord_user_id ~ '^[0-9]{5,25}$'),
  user_id uuid NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- Single-use codes a signed-in person creates in the web console and enters
-- in Discord with /kb link. Only the SHA-256 hash is stored.
CREATE TABLE discord_link_codes (
  code_hash text PRIMARY KEY,
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX discord_link_codes_user_idx ON discord_link_codes(user_id);

-- Interaction ids already handled, so a replayed request is refused.
CREATE TABLE discord_interactions (
  interaction_id text PRIMARY KEY,
  received_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX discord_interactions_received_idx ON discord_interactions(received_at);
