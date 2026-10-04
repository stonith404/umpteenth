-- name: GetProvider :one
SELECT * FROM providers WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: CreateProvider :exec
INSERT INTO providers (id, workspace_id, name, kind, base_url, api_key_enc, key_id, created_at)
VALUES (sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(name), sqlc.arg(kind), sqlc.narg(base_url), sqlc.narg(api_key_enc), sqlc.narg(key_id), sqlc.arg(created_at));

-- name: UpdateProvider :execrows
-- Credentials are replaced or cleared atomically with the destination, comparing against the row actually being updated
UPDATE providers SET name = sqlc.arg(name), base_url = sqlc.narg(base_url),
 api_key_enc = CASE WHEN CAST(sqlc.arg(replace_key) AS BOOLEAN) THEN sqlc.narg(api_key_enc) WHEN COALESCE(base_url, '') = COALESCE(sqlc.narg(base_url), '') THEN api_key_enc ELSE NULL END,
 key_id = CASE WHEN CAST(sqlc.arg(replace_key) AS BOOLEAN) THEN sqlc.narg(key_id) WHEN COALESCE(base_url, '') = COALESCE(sqlc.narg(base_url), '') THEN key_id ELSE NULL END
WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: UpdateProviderKey :execrows
UPDATE providers SET api_key_enc = sqlc.narg(api_key_enc), key_id = sqlc.narg(key_id)
WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: DeleteProvider :execrows
DELETE FROM providers WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: GetModel :one
SELECT m.id, m.provider_id, m.model, m.label, m.price_in, m.price_out, m.price_cache_read, m.price_cache_write, m.caps,
       m.enabled, m.synced, m.unlisted_at,
       p.name AS provider_name, p.kind AS provider_kind, p.base_url AS provider_base_url, p.api_key_enc AS provider_api_key_enc
FROM models m JOIN providers p ON p.id = m.provider_id
WHERE m.workspace_id = sqlc.arg(workspace_id) AND m.id = sqlc.arg(id);

-- name: FindModel :one
SELECT m.id FROM models m JOIN providers p ON p.id = m.provider_id
WHERE m.workspace_id = sqlc.arg(workspace_id) AND p.kind = sqlc.arg(kind) AND m.model = sqlc.arg(model)
ORDER BY m.created_at LIMIT 1;

-- name: CreateModel :exec
INSERT INTO models (id, workspace_id, provider_id, model, label, price_in, price_out, price_cache_read, price_cache_write, context_window, caps, created_at)
VALUES (sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(provider_id), sqlc.arg(model), sqlc.narg(label), sqlc.arg(price_in), sqlc.arg(price_out), sqlc.arg(price_cache_read), sqlc.arg(price_cache_write), sqlc.arg(context_window), sqlc.arg(caps), sqlc.arg(created_at));

-- name: CreateSyncedModel :execrows
-- A sync of the same provider running at the same time may have added the model already, and its row is kept as that sync wrote it
INSERT INTO models (id, workspace_id, provider_id, model, label, price_in, price_out, price_cache_read, price_cache_write, context_window, caps, synced, follow_catalog, created_at)
VALUES (sqlc.arg(id), sqlc.arg(workspace_id), sqlc.arg(provider_id), sqlc.arg(model), sqlc.narg(label), sqlc.arg(price_in), sqlc.arg(price_out), sqlc.arg(price_cache_read), sqlc.arg(price_cache_write), sqlc.arg(context_window), sqlc.arg(caps), TRUE, sqlc.arg(follow_catalog), sqlc.arg(created_at))
ON CONFLICT (provider_id, model) DO NOTHING;

-- name: UpdateModel :execrows
UPDATE models SET label = sqlc.narg(label), price_in = sqlc.arg(price_in), price_out = sqlc.arg(price_out),
  price_cache_read = sqlc.arg(price_cache_read), price_cache_write = sqlc.arg(price_cache_write),
  context_window = sqlc.arg(context_window), caps = sqlc.arg(caps), follow_catalog = sqlc.arg(follow_catalog)
WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: DeleteModel :execrows
DELETE FROM models WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: ClearMissingModelSettings :exec
-- Default models are stored as JSON strings in settings, which no foreign key covers, so a deleted model's ID is removed from them here
DELETE FROM settings
WHERE settings.workspace_id = sqlc.arg(workspace_id)
  AND settings.key IN ('agentModelId', 'utilityModelId', 'reflectionModelId')
  AND settings.value NOT IN (SELECT '"' || m.id || '"' FROM models m WHERE m.workspace_id = sqlc.arg(workspace_id));

-- name: ListProviderModels :many
SELECT id, model, label, price_in, price_out, price_cache_read, price_cache_write, caps, synced, follow_catalog, unlisted_at FROM models
WHERE workspace_id = sqlc.arg(workspace_id) AND provider_id = sqlc.arg(provider_id);

-- name: UpdateModelMetadata :exec
-- A sync refreshes the metadata of models that follow the catalog, leaving the label, prices and capabilities an admin set alone
UPDATE models SET label = sqlc.narg(label), price_in = sqlc.arg(price_in), price_out = sqlc.arg(price_out),
  price_cache_read = sqlc.arg(price_cache_read), price_cache_write = sqlc.arg(price_cache_write),
  context_window = sqlc.arg(context_window), caps = sqlc.arg(caps)
WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id) AND follow_catalog;

-- name: MarkModelListed :exec
-- A model the source lists is synced from now on, even when an admin added it before the source listed it
UPDATE models SET synced = TRUE, unlisted_at = NULL
WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: MarkModelUnlisted :exec
UPDATE models SET unlisted_at = sqlc.arg(unlisted_at)
WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id) AND unlisted_at IS NULL;

-- name: SetModelEnabled :execrows
UPDATE models SET enabled = sqlc.arg(enabled) WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: SetProviderModelsEnabled :execrows
UPDATE models SET enabled = sqlc.arg(enabled) WHERE workspace_id = sqlc.arg(workspace_id) AND provider_id = sqlc.arg(provider_id);

-- name: ListDefaultModelSettings :many
SELECT key, value FROM settings
WHERE workspace_id = sqlc.arg(workspace_id) AND key IN ('agentModelId', 'utilityModelId', 'reflectionModelId');

-- name: SetProviderSync :exec
UPDATE providers SET synced_at = sqlc.arg(synced_at), sync_error = sqlc.narg(sync_error)
WHERE workspace_id = sqlc.arg(workspace_id) AND id = sqlc.arg(id);

-- name: ListAllProviders :many
-- unscoped: the catalog refresh syncs the providers of every workspace
SELECT id, workspace_id FROM providers ORDER BY created_at;

-- name: GetModelCatalog :one
SELECT data, fetched_at FROM model_catalog WHERE source = sqlc.arg(source);

-- name: GetModelCatalogFetchedAt :one
SELECT fetched_at FROM model_catalog WHERE source = sqlc.arg(source);

-- name: SaveModelCatalog :exec
-- A replica that fetched an older document never replaces a newer one
INSERT INTO model_catalog (source, data, fetched_at) VALUES (sqlc.arg(source), sqlc.arg(data), sqlc.arg(fetched_at))
ON CONFLICT (source) DO UPDATE SET data = excluded.data, fetched_at = excluded.fetched_at
WHERE excluded.fetched_at > model_catalog.fetched_at;
