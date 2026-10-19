-- +goose Up

-- The API an OpenAI-compatible provider calls, responses or chat_completions, and NULL for other kinds
-- OpenAI's own API starts on the Responses API, since its Chat Completions endpoint rejects reasoning effort together with function tools for newer models
ALTER TABLE providers ADD COLUMN api TEXT;
UPDATE providers SET api = CASE
  WHEN COALESCE(base_url, '') = '' OR LOWER(base_url) LIKE 'https://api.openai.com/%' OR LOWER(base_url) = 'https://api.openai.com' THEN 'responses'
  ELSE 'chat_completions'
END
WHERE kind = 'openai';

-- +goose Down

ALTER TABLE providers DROP COLUMN api;
