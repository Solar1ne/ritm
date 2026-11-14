package postgresstore

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

func TestCanonicalMigrationSourcesKeepLegacyBytes(t *testing.T) {
	data, digest, err := migration()
	if err != nil || digest != "bf56b448909735999163e4596e7fb4e1bb082ce48ff2fd237a3c474226256059" || strings.Contains(string(data), "+goose") {
		t.Fatalf("legacy SQL bytes or digest changed: %v", err)
	}
	files, err := migrationFS()
	if err != nil {
		t.Fatal(err)
	}
	second, err := files.Open("20261018090100_diaries_updated_at_index.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationInventoryRejectsUnregisteredTimestamp(t *testing.T) {
	files := fstest.MapFS{
		"20261018090000_initial.sql":                  {Data: []byte("legacy")},
		"20261018090100_diaries_updated_at_index.sql": {Data: []byte("second")},
		"20261018090200_future.sql":                   {Data: []byte("future")},
	}
	if err := validateMigrationInventory(files, []string{"20261018090000_initial.sql", "20261018090100_diaries_updated_at_index.sql", "20261018090200_future.sql"}); err != nil {
		t.Fatalf("declared timestamp inventory rejected: %v", err)
	}
	if err := validateMigrationInventory(files, migrationNames()); err == nil {
		t.Fatal("unregistered future SQL was silently ignored")
	}
}

func migrateLegacyFixture(t *testing.T, s *Store) {
	t.Helper()
	data, digest, err := migration()
	if err != nil {
		t.Fatal(err)
	}
	err = s.transaction(context.Background(), pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(context.Background(), "SELECT pg_advisory_xact_lock($1)", schemaLock); err != nil {
			return err
		}
		if _, err := tx.Exec(context.Background(), legacyTableSQL); err != nil {
			return err
		}
		if _, err := tx.Exec(context.Background(), string(data)); err != nil {
			return err
		}
		_, err := tx.Exec(context.Background(), "INSERT INTO rhythm_schema_migrations(version,checksum) VALUES(1,$1)", digest)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func businessRows(t *testing.T, s *Store) map[string]string {
	t.Helper()
	result := make(map[string]string)
	for _, table := range []string{"rhythm_identities", "rhythm_diaries", "rhythm_accounts", "rhythm_sessions", "rhythm_legacy_revocations", "rhythm_metadata", "rhythm_imports", "rhythm_schema_migrations"} {
		var value string
		query := "SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY t::text), '[]'::jsonb)::text FROM " + pgx.Identifier{table}.Sanitize() + " t"
		if err := s.pool.QueryRow(context.Background(), query).Scan(&value); err != nil {
			t.Fatal(err)
		}
		result[table] = value
	}
	return result
}

func assertLegacyReceipt(t *testing.T, s *Store) {
	t.Helper()
	// This is the pre-Goose binary's readiness contract, independent of Goose.
	var count int
	var version int64
	var checksum string
	if err := s.pool.QueryRow(context.Background(), "SELECT count(*), COALESCE(max(version),0), COALESCE(max(checksum),'') FROM rhythm_schema_migrations").Scan(&count, &version, &checksum); err != nil {
		t.Fatal(err)
	}
	if count != 1 || version != 1 || checksum != legacyChecksum {
		t.Fatal("the pre-Goose binary would reject its migration receipt")
	}
}

func TestGooseFreshStatusPopulatedUpgradeAndRepeat(t *testing.T) {
	s, _ := testDatabase(t, false)
	ctx := context.Background()
	initial, err := s.Status(ctx)
	if err != nil || initial.CurrentVersion != 0 || initial.TargetVersion != schemaVersion || initial.Legacy || len(initial.Versions) != 2 || initial.Versions[0].State != "pending" || initial.Versions[1].State != "pending" {
		t.Fatalf("fresh status: %+v %v", initial, err)
	}
	var absent bool
	if err := s.pool.QueryRow(ctx, "SELECT to_regclass('goose_db_version') IS NULL AND to_regclass('rhythm_schema_migrations') IS NULL").Scan(&absent); err != nil || !absent {
		t.Fatalf("status mutated empty database: %v", err)
	}
	if err := s.MigrateTo(ctx, 1); err != nil {
		t.Fatal(err)
	}
	first, err := s.Status(ctx)
	if err != nil || first.CurrentVersion != initialVersion || first.Versions[0].State != "applied" || first.Versions[1].State != "pending" {
		t.Fatalf("v1 status: %+v %v", first, err)
	}
	if err := s.Check(ctx); !errors.Is(err, ErrSchema) {
		t.Fatalf("runtime accepted pending latest migration: %v", err)
	}
	if err := writeProfile(s, guest("a"), 0); err != nil {
		t.Fatal(err)
	}
	before := businessRows(t, s)
	if err := s.MigrateTo(ctx, 2); err != nil {
		t.Fatal(err)
	}
	after, err := s.Status(ctx)
	if err != nil || after.CurrentVersion != schemaVersion || after.Versions[1].State != "applied" || !reflect.DeepEqual(first.Versions[0], after.Versions[0]) {
		t.Fatalf("v2 status: %+v %v", after, err)
	}
	if !reflect.DeepEqual(before, businessRows(t, s)) {
		t.Fatal("index migration changed populated data or the legacy receipt")
	}
	assertLegacyReceipt(t, s)
	var indexOID uint32
	if err := s.pool.QueryRow(ctx, "SELECT 'rhythm_diaries_updated_at'::regclass::oid").Scan(&indexOID); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repeated, err := s.Status(ctx)
	if err != nil || !reflect.DeepEqual(after, repeated) {
		t.Fatalf("repeat changed migration history: %v", err)
	}
	var repeatedOID uint32
	var versions int
	if err := s.pool.QueryRow(ctx, "SELECT 'rhythm_diaries_updated_at'::regclass::oid, (SELECT count(*) FROM goose_db_version)").Scan(&repeatedOID, &versions); err != nil || repeatedOID != indexOID || versions != 3 {
		t.Fatalf("repeat recreated index or version rows: %v", err)
	}
	if err := s.MigrateTo(ctx, 1); err == nil {
		t.Fatal("downgrade was accepted")
	}
	if !reflect.DeepEqual(before, businessRows(t, s)) {
		t.Fatal("repeat or denied down changed data")
	}
}

func TestGooseAdoptsLegacyWithoutReplayingDDLOrChangingData(t *testing.T) {
	s, _ := testDatabase(t, false)
	migrateLegacyFixture(t, s)
	ctx := context.Background()
	if err := writeProfile(s, guest("a"), 0); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	a := service(t, s)
	grant, err := a.Register(ctx, guest("a"), 1, "legacy-user", "a long synthetic password", now)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, s, "INSERT INTO rhythm_metadata(key,value) VALUES('fixture',decode('abcd','hex'))")
	mustExec(t, s, "INSERT INTO rhythm_imports(fingerprint,diary_count,account_count) VALUES($1,1,1)", strings.Repeat("a", 64))
	before := businessRows(t, s)
	status, err := s.Status(ctx)
	if err != nil || !status.Legacy || status.CurrentVersion != 0 || status.Versions[0].State != "legacy" {
		t.Fatalf("legacy status: %+v %v", status, err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, businessRows(t, s)) {
		t.Fatal("takeover rewrote existing credentials, diary or compatibility metadata")
	}
	if principal, err := a.Resolve(ctx, grant.Token, now); err != nil || principal != grant.Principal {
		t.Fatal("takeover invalidated the existing session")
	}
	assertLegacyReceipt(t, s)
	if err := s.Check(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestGooseRejectsPartialUnknownAndAlteredLegacySchemas(t *testing.T) {
	for _, tc := range []struct{ name, change string }{
		{"checksum", "UPDATE rhythm_schema_migrations SET checksum='wrong'"},
		{"version", "UPDATE rhythm_schema_migrations SET version=99"},
		{"empty_receipt", "DELETE FROM rhythm_schema_migrations"},
		{"missing_table", "DROP TABLE rhythm_metadata"},
		{"column_type", "ALTER TABLE rhythm_metadata ALTER COLUMN value TYPE text"},
		{"missing_check", "ALTER TABLE rhythm_diaries DROP CONSTRAINT rhythm_diaries_revision_check"},
		{"wrong_check", "ALTER TABLE rhythm_diaries DROP CONSTRAINT rhythm_diaries_revision_check; ALTER TABLE rhythm_diaries ADD CONSTRAINT rhythm_diaries_revision_check CHECK(revision>=-1)"},
		{"unknown_table", "CREATE TABLE rhythm_unknown (id integer)"},
		{"unknown_index", "CREATE INDEX rhythm_extra ON rhythm_diaries(updated_at)"},
		{"unprefixed_unique_index", "CREATE UNIQUE INDEX extra_revision_limit ON rhythm_diaries(revision)"},
		{"row_security", "ALTER TABLE rhythm_diaries ENABLE ROW LEVEL SECURITY"},
		{"wrong_goose_table", "CREATE TABLE goose_db_version(version_id bigint)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := testDatabase(t, false)
			migrateLegacyFixture(t, s)
			mustExec(t, s, tc.change)
			before, err := catalogFingerprint(context.Background(), s.pool, "rhythm_")
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Migrate(context.Background()); !errors.Is(err, ErrSchema) {
				t.Fatalf("incompatible schema accepted: %v", err)
			}
			if _, err := s.Status(context.Background()); !errors.Is(err, ErrSchema) {
				t.Fatalf("status accepted incompatible schema: %v", err)
			}
			after, err := catalogFingerprint(context.Background(), s.pool, "rhythm_")
			if err != nil || before != after {
				t.Fatal("rejected migration modified application schema")
			}
		})
	}
}

func TestGooseRejectsCorruptVersionHistory(t *testing.T) {
	for _, tc := range []struct{ name, change string }{
		{"unknown", "INSERT INTO goose_db_version(version_id,is_applied) VALUES(99,true)"},
		{"duplicate", fmt.Sprintf("INSERT INTO goose_db_version(version_id,is_applied) VALUES(%d,true)", initialVersion)},
		{"missing_zero", "DELETE FROM goose_db_version WHERE version_id=0"},
		{"missing_one", fmt.Sprintf("DELETE FROM goose_db_version WHERE version_id=%d", initialVersion)},
		{"unapplied", fmt.Sprintf("UPDATE goose_db_version SET is_applied=false WHERE version_id=%d", schemaVersion)},
		{"empty", "DELETE FROM goose_db_version"},
		{"index_missing", "DROP INDEX rhythm_diaries_updated_at"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := testDatabase(t, true)
			mustExec(t, s, tc.change)
			if err := s.Migrate(context.Background()); !errors.Is(err, ErrSchema) {
				t.Fatalf("bad ledger accepted: %v", err)
			}
			if err := s.Check(context.Background()); !errors.Is(err, ErrSchema) {
				t.Fatalf("bad ledger ready: %v", err)
			}
		})
	}
}

func TestGooseFailureRollsBackInitialAndIncrementalTransactions(t *testing.T) {
	for _, version := range []int64{1, 2} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			s, _ := testDatabase(t, false)
			ctx := context.Background()
			data, digest, err := migration()
			if err != nil {
				t.Fatal(err)
			}
			files, err := migrationFS()
			if err != nil {
				t.Fatal(err)
			}
			var before map[string]string
			if version == 1 {
				data = append(data, []byte("\nSELECT 1/0;")...)
			} else {
				if err := s.MigrateTo(ctx, 1); err != nil {
					t.Fatal(err)
				}
				if err := writeProfile(s, guest("a"), 0); err != nil {
					t.Fatal(err)
				}
				before = businessRows(t, s)
				files = fstest.MapFS{"20261018090100_diaries_updated_at_index.sql": {Data: []byte("-- +goose Up\nCREATE INDEX rhythm_diaries_updated_at ON rhythm_diaries(updated_at);\nSELECT 1/0;\n")}}
			}
			db := stdlib.OpenDBFromPool(s.pool)
			t.Cleanup(func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			})
			provider, err := newMigrationProvider(db, files, data, digest, false)
			if err != nil {
				t.Fatal(err)
			}
			target := migrationVersions()[version-1]
			if _, err := provider.UpTo(ctx, target); err == nil {
				t.Fatal("expected synthetic SQL failure")
			}
			var count int
			if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM goose_db_version WHERE version_id=$1", target).Scan(&count); err != nil || count != 0 {
				t.Fatalf("failed migration was marked applied: %v", err)
			}
			if version == 1 {
				physical, err := catalogFingerprint(ctx, s.pool, "rhythm_")
				if err != nil || physical != "" {
					t.Fatalf("failed initial migration left business DDL: %v", err)
				}
			} else if !reflect.DeepEqual(before, businessRows(t, s)) {
				t.Fatal("failed index migration changed rows")
			}
			if err := s.Migrate(ctx); err != nil {
				t.Fatalf("safe retry after rollback: %v", err)
			}
		})
	}
}

func TestGooseConcurrentMigratorsDoNotExhaustApplicationPool(t *testing.T) {
	s, _ := testDatabase(t, false)
	var workers sync.WaitGroup
	errs := make(chan error, 12)
	for range 12 {
		workers.Go(func() { errs <- s.Migrate(context.Background()) })
	}
	workers.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := s.pool.QueryRow(context.Background(), "SELECT count(*) FROM goose_db_version").Scan(&count); err != nil || count != 3 {
		t.Fatalf("concurrent migration history=%d err=%v", count, err)
	}
}

func TestGooseSharesLegacyLockAndCancellationReleasesResources(t *testing.T) {
	s, _ := testDatabase(t, false)
	ctx := context.Background()
	legacyTx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := legacyTx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			t.Error(err)
		}
	})
	if _, err := legacyTx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", schemaLock); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	err = s.Migrate(canceled)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("new migrator bypassed legacy lock: %v", err)
	}
	if err := legacyTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	followup, stop := context.WithTimeout(ctx, 2*time.Second)
	defer stop()
	if err := s.Migrate(followup); err != nil {
		t.Fatalf("canceled waiter leaked resources: %v", err)
	}
}

func TestGooseRejectsMissingTargetSchema(t *testing.T) {
	s, raw := testDatabase(t, false)
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	query := u.Query()
	query.Set("search_path", "missing_goose_target_schema")
	u.RawQuery = query.Encode()
	missing, err := Open(context.Background(), u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer missing.Close()
	if _, err := missing.Status(context.Background()); !errors.Is(err, ErrSchema) {
		t.Fatalf("missing schema reported as ready/pending: %v", err)
	}
	if err := missing.Migrate(context.Background()); !errors.Is(err, ErrSchema) {
		t.Fatalf("missing target schema accepted: %v", err)
	}
	status, err := s.Status(context.Background())
	if err != nil || status.CurrentVersion != 0 {
		t.Fatalf("invalid target affected another schema: %v", err)
	}
}
