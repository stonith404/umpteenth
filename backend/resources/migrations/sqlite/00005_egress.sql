-- +goose Up
-- An allow-list job reaches only these domains, and a job may opt in to reaching private ranges such as the LAN
ALTER TABLE jobs ADD COLUMN allowed_domains TEXT NOT NULL DEFAULT '[]';
ALTER TABLE jobs ADD COLUMN allow_private_network BOOLEAN NOT NULL DEFAULT FALSE;

-- +goose Down
ALTER TABLE jobs DROP COLUMN allow_private_network;
ALTER TABLE jobs DROP COLUMN allowed_domains;
