package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMigrationArgumentsFailBeforeOpeningDatabase(t *testing.T) {
	original := os.Args
	t.Cleanup(func() { os.Args = original })
	t.Setenv("DATABASE_URL", "")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, "usage"},
		{[]string{"migrate", "extra"}, "no extra arguments"},
		{[]string{"status", "extra"}, "no extra arguments"},
		{[]string{"migrate-to"}, "usage"},
		{[]string{"migrate-to", "0"}, "positive integer"},
		{[]string{"migrate-to", "no"}, "positive integer"},
		{[]string{"migrate-to", "-1"}, "positive integer"},
		{[]string{"down"}, "unknown command"},
	} {
		t.Run(strings.Join(tc.args, "_"), func(t *testing.T) {
			os.Args = append([]string{"rhythm-db"}, tc.args...)
			if err := run(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("args=%v err=%v", tc.args, err)
			}
		})
	}
}

func TestCreateMigrationIsOfflineUTCTimestampAndForwardOnly(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"20261018090000_initial.sql", "20261018090100_diaries_updated_at_index.sql"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("-- existing immutable SQL\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	original := os.Args
	t.Cleanup(func() { os.Args = original })
	// Even an unusable configured URL must not be parsed or opened for authoring.
	t.Setenv("DATABASE_URL", "this-is-not-a-database-url")
	os.Args = []string{"rhythm-db", "create", "-dir", dir, "diary_index"}
	before := time.Now().UTC().Truncate(time.Second)
	if err := run(); err != nil {
		t.Fatal(err)
	}
	after := time.Now().UTC().Truncate(time.Second)
	matches, err := filepath.Glob(filepath.Join(dir, "*_diary_index.sql"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("timestamp file not created: %v", err)
	}
	created := matches[0]
	prefix := strings.TrimSuffix(filepath.Base(created), "_diary_index.sql")
	stamp, err := time.Parse("20060102150405", prefix)
	if err != nil || len(prefix) != 14 || stamp.Before(before) || stamp.After(after) {
		t.Fatalf("version is not a current UTC timestamp: %q %v", prefix, err)
	}
	data, err := os.ReadFile(created)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "-- +goose Up") || !strings.Contains(string(data), "Replace SELECT 1") || strings.Contains(string(data), "-- +goose Down") {
		t.Fatalf("unexpected forward-only scaffold: %s", data)
	}
	for _, name := range []string{"20261018090000_initial.sql", "20261018090100_diaries_updated_at_index.sql"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(data) != "-- existing immutable SQL\n" {
			t.Fatalf("create changed an applied migration: %v", err)
		}
	}
}

func TestCreateMigrationRejectsUnsafeNamesWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"../outside", "Upper", "a-b", "two names", "1numeric", "", strings.Repeat("a", 65)} {
		if err := createMigration([]string{"-dir", dir, name}); err == nil {
			t.Fatalf("unsafe name %q accepted", name)
		}
	}
	if err := createMigration([]string{"-dir", dir}); err == nil {
		t.Fatal("missing name accepted")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("invalid create wrote files: %v", err)
	}
}
