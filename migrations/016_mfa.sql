-- Two-step sign-in (TOTP) for web sessions. Kept out of the users table so
-- to_jsonb(u) never carries second-factor material.
CREATE TABLE IF NOT EXISTS user_mfa (
  user_id uuid PRIMARY KEY REFERENCES users(id),
  -- AES-GCM sealed TOTP secret (MFA_ENCRYPTION_KEY); never stored in plain text.
  secret_ciphertext text NOT NULL,
  -- NULL while setup is pending confirmation.
  enabled_at timestamptz,
  -- Last accepted TOTP time step; codes at or before it are replays.
  last_step bigint NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS mfa_recovery_codes (
  user_id uuid NOT NULL REFERENCES users(id),
  code_hash text NOT NULL,
  used_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, code_hash)
);

-- A password-verified sign-in waiting for its second factor.
CREATE TABLE IF NOT EXISTS mfa_challenges (
  token_hash text PRIMARY KEY,
  user_id uuid NOT NULL REFERENCES users(id),
  -- Digest of the password hash at challenge time: a password change invalidates it.
  credential_digest text NOT NULL,
  attempts integer NOT NULL DEFAULT 0,
  expires_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS mfa_challenges_user ON mfa_challenges(user_id);
