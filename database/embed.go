// Package database supplies the schema files used by the PostgreSQL migrator.
package database

import "embed"

// Migrations contains the immutable initial schema and incremental Goose migrations.
//
//go:embed migrations/*.sql
var Migrations embed.FS
