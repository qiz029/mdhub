package main

// Schema migrations run at process startup: schema.sql is embedded and
// applied against the database on every boot. This works because every
// statement in schema.sql is idempotent (CREATE TABLE IF NOT EXISTS,
// ALTER TABLE ... ADD COLUMN IF NOT EXISTS) — re-running is a no-op once
// the schema is current, so "deploy" never needs a separate manual
// psql step.
//
// Keep schema.sql strictly idempotent: no unguarded ALTER/UPDATE/DROP.
// Future non-idempotent changes (renames, backfills) must carry their own
// guards (e.g. UPDATE ... WHERE, INSERT ... ON CONFLICT DO NOTHING).

import (
	_ "embed"
	"log"
)

//go:embed schema.sql
var schemaSQL string

// migrateSchema applies schema.sql to the connected database.
func migrateSchema() error {
	_, err := db.Exec(schemaSQL)
	return err
}

// mustMigrateSchema applies the schema or exits — continuing to serve with a
// stale schema would surface as confusing query errors far from the cause.
func mustMigrateSchema() {
	if err := migrateSchema(); err != nil {
		log.Fatal("schema migration:", err)
	}
	log.Println("schema migration applied")
}
