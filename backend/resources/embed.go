package resources

import "embed"

// Migrations holds the goose migrations for both database engines, under migrations/sqlite and migrations/postgres
//
//go:embed migrations
var Migrations embed.FS
