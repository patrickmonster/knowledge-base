-- The Discord server allow list was removed (the commands work in every server
-- the bot is in; only people who linked their account can see data). Nothing
-- reads or writes the column any more. Dropping a column also drops the
-- CHECK constraint that was defined on it.
ALTER TABLE discord_settings DROP COLUMN IF EXISTS allowed_guild_ids;
