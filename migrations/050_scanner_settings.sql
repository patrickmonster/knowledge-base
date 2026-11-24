-- Malware scanner address set by a global administrator in the web console, so
-- CLAMAV_ADDRESS no longer has to be a server environment variable. One row for
-- the whole server. No row means "use the environment value"; a row with an
-- empty address turns scanning off even when the environment sets one.
CREATE TABLE scanner_settings (
  id boolean PRIMARY KEY DEFAULT true CHECK (id),
  clamav_address text NOT NULL CHECK (char_length(clamav_address) <= 255),
  updated_by uuid NOT NULL REFERENCES users(id),
  updated_at timestamptz NOT NULL DEFAULT now()
);
