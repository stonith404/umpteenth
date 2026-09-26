-- +goose Up
-- Providers keep their model list in sync with its source: the models.dev catalog for Anthropic and OpenAI, the server's model list for other OpenAI-compatible APIs
-- An admin turns models off instead of deleting them, since the next sync would add a deleted model back
-- follow_catalog models take their prices and capabilities from every catalog refresh until an admin edits them
-- unlisted_at marks a synced model its source stopped listing, which is kept so its settings survive the model coming back
ALTER TABLE models ADD COLUMN enabled BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE models ADD COLUMN synced BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE models ADD COLUMN follow_catalog BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE models ADD COLUMN unlisted_at BIGINT;
ALTER TABLE providers ADD COLUMN synced_at BIGINT;
ALTER TABLE providers ADD COLUMN sync_error TEXT;

-- Models of catalog providers were seeded from the bundled catalog by earlier builds, so they follow the catalog from now on
UPDATE models SET follow_catalog = TRUE
WHERE provider_id IN (
  SELECT id FROM providers
  WHERE kind = 'anthropic' OR (kind = 'openai' AND (base_url IS NULL OR base_url = '' OR base_url LIKE '%://api.openai.com%'))
);

-- The latest models.dev catalog, shared by every replica and kept across restarts
CREATE TABLE model_catalog (
  source TEXT PRIMARY KEY,
  data TEXT NOT NULL,
  fetched_at BIGINT NOT NULL
);

-- +goose Down
DROP TABLE model_catalog;
ALTER TABLE providers DROP COLUMN sync_error;
ALTER TABLE providers DROP COLUMN synced_at;
ALTER TABLE models DROP COLUMN unlisted_at;
ALTER TABLE models DROP COLUMN follow_catalog;
ALTER TABLE models DROP COLUMN synced;
ALTER TABLE models DROP COLUMN enabled;
