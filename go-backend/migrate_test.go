package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestMigrateSchemaExecutesEmbeddedSQL(t *testing.T) {
	mock := withMockDatabase(t)
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS documents").
		WillReturnResult(sqlmock.NewResult(0, 0))

	if err := migrateSchema(); err != nil {
		t.Fatalf("migrateSchema: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestMigrateSchemaPropagatesErrors(t *testing.T) {
	mock := withMockDatabase(t)
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS documents").
		WillReturnError(errors.New("permission denied"))

	if err := migrateSchema(); err == nil {
		t.Fatal("expected migration error to propagate")
	}
}

// The startup-migration model depends on schema.sql staying idempotent.
// Guard the two constructs that make it so; a non-idempotent statement
// belongs behind its own guard, not in a plain DDL line.
func TestEmbeddedSchemaStaysIdempotent(t *testing.T) {
	if !strings.Contains(schemaSQL, "CREATE TABLE IF NOT EXISTS documents") {
		t.Error("schema.sql lost its idempotent CREATE TABLE guard")
	}
	if !strings.Contains(schemaSQL, "ADD COLUMN IF NOT EXISTS") {
		t.Error("schema.sql lost its idempotent ADD COLUMN guard")
	}
	for _, stmt := range strings.Split(schemaSQL, ";") {
		upper := strings.ToUpper(stmt)
		if strings.Contains(upper, "DROP TABLE") || strings.Contains(upper, "DROP COLUMN") {
			t.Errorf("schema.sql must never drop: %q", strings.TrimSpace(stmt))
		}
	}
}
