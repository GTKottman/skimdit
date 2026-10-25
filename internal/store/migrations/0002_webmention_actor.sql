-- Fediverse responses remember which actor sent them, so only that actor
-- can change or delete them.
ALTER TABLE webmentions ADD COLUMN actor TEXT NOT NULL DEFAULT '';
