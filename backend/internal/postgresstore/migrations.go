package postgresstore

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io/fs"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"rhythm/database"
)

const (
	// New migrations must extend migrationNames and migrationVersions, update
	// schemaVersion and add structural fingerprints below, with an integration test
	// from the previous populated version. Unregistered embedded files fail closed.
	initialVersion = 20261018090000
	schemaVersion  = 20261018090100
	legacyVersion  = 1
	legacyChecksum = "bf56b448909735999163e4596e7fb4e1bb082ce48ff2fd237a3c474226256059"
	legacyTableSQL = "CREATE TABLE rhythm_schema_migrations (version bigint PRIMARY KEY, checksum text NOT NULL, applied_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP)"

	// PostgreSQL 16 catalog fingerprints, derived from the immutable SQL in an
	// empty test schema. They exclude row values, owners, schema names and user
	// object OIDs. Strict shape checks prevent adopting an incomplete old schema
	// just because its legacy checksum row happens to be correct.
	schemaV1Fingerprint = "292fe420e356b4cf17953e792b8fbf45a7ecd3888d4161c54bf0eb0bc1ca70ef"
	schemaV2Fingerprint = "31c2e1ae2b32e7598b9e04ae07c890a4d718eca568da84944ea8b34a316f42c6"
	gooseFingerprint    = "0fce8c6ab2b885863fe3d32766a1d721f5fa03ae8be9fec984b7677e2572422b"
)

func migrationNames() []string {
	return []string{"20261018090000_initial.sql", "20261018090100_diaries_updated_at_index.sql"}
}

func migration() ([]byte, string, error) {
	data, err := database.Migrations.ReadFile("migrations/20261018090000_initial.sql")
	if err != nil {
		return nil, "", err
	}
	digest := sha256.Sum256(data)
	actual := hex.EncodeToString(digest[:])
	if actual != legacyChecksum {
		return nil, "", errors.New("immutable initial migration checksum changed")
	}
	return data, actual, nil
}

func migrationFS() (fs.FS, error) {
	files, err := fs.Sub(database.Migrations, "migrations")
	if err != nil {
		return nil, err
	}
	names := migrationNames()
	if len(names) != len(migrationVersions()) {
		return nil, errors.New("migration inventory changed: update schema head and compatibility validation")
	}
	if err := validateMigrationInventory(files, names); err != nil {
		return nil, err
	}
	return files, nil
}

func validateMigrationInventory(files fs.FS, names []string) error {
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		return err
	}
	if len(entries) != len(names) {
		return errors.New("migration inventory changed: update schema head and compatibility validation")
	}
	expected := make(map[string]bool, len(names))
	for _, name := range names {
		expected[name] = true
	}
	// Compare the declared inventory without relying on directory iteration order.
	for _, entry := range entries {
		if entry.IsDir() || !expected[entry.Name()] {
			return errors.New("migration inventory changed: update schema head and compatibility validation")
		}
	}
	return nil
}

type MigrationStatus struct {
	CurrentVersion            int64              `json:"currentVersion"`
	TargetVersion             int64              `json:"targetVersion"`
	Legacy                    bool               `json:"legacy"`
	NeedsVersionNormalization bool               `json:"needsVersionNormalization"`
	Versions                  []MigrationVersion `json:"versions"`
}

type MigrationVersion struct {
	Version   int64      `json:"version"`
	Name      string     `json:"name"`
	State     string     `json:"state"`
	AppliedAt *time.Time `json:"appliedAt,omitempty"`
}

type migrationState struct {
	version int64
	legacy  bool
	numeric bool
	applied map[int64]time.Time
}

func migrationVersions() []int64 { return []int64{initialVersion, schemaVersion} }

// Status is read-only, including on an empty database. Goose's Provider.Status
// initializes its version table, so it is deliberately not used for this command.
func (s *Store) Status(ctx context.Context) (MigrationStatus, error) {
	if _, err := migrationFS(); err != nil {
		return MigrationStatus{}, err
	}
	var state migrationState
	err := s.transaction(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		var err error
		state, err = inspectMigrations(ctx, tx)
		return err
	})
	if err != nil {
		return MigrationStatus{}, err
	}
	versions := migrationVersions()
	result := MigrationStatus{CurrentVersion: state.version, TargetVersion: schemaVersion, Legacy: state.legacy, NeedsVersionNormalization: state.numeric, Versions: make([]MigrationVersion, 0, len(versions))}
	for i, name := range migrationNames() {
		version := versions[i]
		item := MigrationVersion{Version: version, Name: name, State: "pending"}
		if applied, ok := state.applied[version]; ok {
			item.State, item.AppliedAt = "applied", &applied
		} else if state.legacy && version == initialVersion {
			item.State = "legacy"
		}
		result.Versions = append(result.Versions, item)
	}
	return result, nil
}

// Migrate is explicit, never triggered by startup or an HTTP request. Goose owns
// each migration's transaction and version record. The old checksum row remains
// unchanged so the pre-Goose binary can still read this additive schema.
func (s *Store) Migrate(ctx context.Context) error { return s.MigrateTo(ctx, schemaVersion) }

// MigrateTo only moves forward. Numeric 1/2 remain aliases for the two known
// timestamp versions, preserving scripts that predate timestamp naming.
func (s *Store) MigrateTo(ctx context.Context, target int64) (result error) {
	switch target {
	case 1:
		target = initialVersion
	case 2:
		target = schemaVersion
	case initialVersion, schemaVersion:
	default:
		return errors.New("unknown migration target; use a registered timestamp or alias 1/2; down migrations are disabled")
	}
	files, err := migrationFS()
	if err != nil {
		return err
	}
	data, digest, err := migration()
	if err != nil {
		return err
	}
	// A dedicated physical connection owns the session lock. It does not consume
	// a pool slot while waiting, so concurrent migrators cannot exhaust the pool
	// needed by Goose. Closing it always releases the lock, including cancellation.
	lockConn, err := pgx.ConnectConfig(ctx, s.pool.Config().ConnConfig.Copy())
	if err != nil {
		return dbError(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		if err := lockConn.Close(cleanup); err != nil {
			result = errors.Join(result, dbError(err))
		}
	}()
	// The same key also serializes with the former transaction-scoped migrator.
	// Lock before Provider.UpTo: Goose's initial HasPending can create its ledger.
	if _, err := lockConn.Exec(ctx, "SELECT pg_advisory_lock($1)", schemaLock); err != nil {
		return dbError(err)
	}
	before, err := inspectMigrations(ctx, lockConn)
	if err != nil {
		return err
	}
	if target < before.version {
		return errors.New("database is newer than the requested target; down migrations are disabled")
	}
	if before.numeric {
		if err := s.normalizeMigrationVersions(ctx); err != nil {
			return err
		}
	}
	db := stdlib.OpenDBFromPool(s.pool)
	defer func() {
		if err := db.Close(); err != nil {
			result = errors.Join(result, dbError(err))
		}
	}()
	provider, err := newMigrationProvider(db, files, data, digest, before.legacy)
	if err != nil {
		return errors.New("cannot initialize migration provider")
	}
	if _, err := provider.UpTo(ctx, target); err != nil {
		if errors.Is(err, ErrSchema) {
			return ErrSchema
		}
		return dbError(err) // Goose errors can include SQL; never expose their text.
	}
	after, err := inspectMigrations(ctx, lockConn)
	if err != nil {
		return err
	}
	if after.version != target || after.legacy || after.numeric {
		return ErrSchema
	}
	return nil
}

// normalizeMigrationVersions runs only while MigrateTo owns schemaLock. A
// ledger lock serializes ledger writes while permitting readers. Application
// relations are locked against concurrent DDL without excluding ordinary writes,
// then checked again. Only exact known histories are rewritten; no DDL is replayed.
func (s *Store) normalizeMigrationVersions(ctx context.Context) error {
	return s.transaction(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "LOCK TABLE goose_db_version IN SHARE ROW EXCLUSIVE MODE"); err != nil {
			return dbError(err)
		}
		if _, err := tx.Exec(ctx, "LOCK TABLE rhythm_schema_migrations,rhythm_identities,rhythm_diaries,rhythm_accounts,rhythm_sessions,rhythm_legacy_revocations,rhythm_metadata,rhythm_imports IN SHARE UPDATE EXCLUSIVE MODE"); err != nil {
			return dbError(err)
		}
		before, err := inspectMigrations(ctx, tx)
		if err != nil {
			return err
		}
		if !before.numeric {
			return nil
		}
		result, err := tx.Exec(ctx, "UPDATE goose_db_version SET version_id=CASE version_id WHEN 1 THEN $1::bigint WHEN 2 THEN $2::bigint END WHERE version_id IN (1,2)", int64(initialVersion), int64(schemaVersion))
		if err != nil {
			return dbError(err)
		}
		if result.RowsAffected() != int64(len(before.applied)-1) {
			return ErrSchema
		}
		after, err := inspectMigrations(ctx, tx)
		if err != nil {
			return err
		}
		if after.numeric || after.version != before.version {
			return ErrSchema
		}
		return nil
	})
}

func newMigrationProvider(db *sql.DB, files fs.FS, data []byte, digest string, legacy bool) (*goose.Provider, error) {
	initial := goose.NewGoMigration(initialVersion, &goose.GoFunc{RunTx: func(ctx context.Context, tx *sql.Tx) error {
		if legacy {
			// The structural preflight ran under the same advisory lock. Recheck
			// the compatibility receipt in the transaction; never replay old DDL.
			var count int
			var version int64
			var actual string
			if err := tx.QueryRowContext(ctx, "SELECT count(*), COALESCE(max(version),0), COALESCE(max(checksum),'') FROM rhythm_schema_migrations").Scan(&count, &version, &actual); err != nil {
				return err
			}
			if count != 1 || version != legacyVersion || actual != digest {
				return ErrSchema
			}
			return nil
		}
		if _, err := tx.ExecContext(ctx, legacyTableSQL); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(data)); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO rhythm_schema_migrations (version,checksum) VALUES ($1,$2)", legacyVersion, digest)
		return err
	}}, &goose.GoFunc{RunTx: func(context.Context, *sql.Tx) error {
		return errors.New("destructive down migrations are disabled")
	}})
	return goose.NewProvider(goose.DialectPostgres, db, files,
		goose.WithDisableGlobalRegistry(true),
		goose.WithExcludeVersions([]int64{initialVersion}),
		goose.WithGoMigrations(initial))
}

func (s *Store) checkSchema(ctx context.Context, q querier) error {
	state, err := inspectMigrations(ctx, q)
	if err != nil {
		return err
	}
	if state.version != schemaVersion || state.legacy {
		return ErrSchema
	}
	return nil
}

func inspectMigrations(ctx context.Context, q querier) (migrationState, error) {
	state := migrationState{applied: make(map[int64]time.Time)}
	var namespace *string
	if err := q.QueryRow(ctx, "SELECT current_schema()").Scan(&namespace); err != nil {
		return state, dbError(err)
	}
	if namespace == nil {
		return state, ErrSchema
	}
	_, digest, err := migration()
	if err != nil {
		return state, err
	}
	physical, err := catalogFingerprint(ctx, q, "rhythm_")
	if err != nil {
		return state, err
	}
	physicalVersion := int64(0)
	switch physical {
	case "": // No application objects: only this state may run initial DDL.
	case schemaV1Fingerprint:
		physicalVersion = initialVersion
	case schemaV2Fingerprint:
		physicalVersion = schemaVersion
	default:
		return state, ErrSchema
	}
	if physicalVersion != 0 {
		var count int
		var version int64
		var actual string
		if err := q.QueryRow(ctx, "SELECT count(*), COALESCE(max(version),0), COALESCE(max(checksum),'') FROM rhythm_schema_migrations").Scan(&count, &version, &actual); err != nil {
			return state, errors.Join(ErrSchema, dbError(err))
		}
		if count != 1 || version != legacyVersion || actual != digest {
			return state, ErrSchema
		}
	}
	ledger, err := catalogFingerprint(ctx, q, "goose_db_version")
	if err != nil {
		return state, err
	}
	if ledger != "" {
		if ledger != gooseFingerprint {
			return state, ErrSchema
		}
		rows, err := q.Query(ctx, "SELECT version_id,is_applied,tstamp FROM goose_db_version ORDER BY id")
		if err != nil {
			return state, errors.Join(ErrSchema, dbError(err))
		}
		defer rows.Close()
		// Both histories are accepted for reads, but they may never be mixed.
		// State always uses canonical timestamps; numeric is an explicit marker
		// that the next migration command must normalize the persisted ledger.
		versions := append([]int64{0}, migrationVersions()...)
		position := 0
		for rows.Next() {
			var version int64
			var applied bool
			var at time.Time
			if err := rows.Scan(&version, &applied, &at); err != nil {
				return state, errors.Join(ErrSchema, dbError(err))
			}
			if position >= len(versions) || !applied {
				return state, ErrSchema
			}
			if position == 1 && version == 1 {
				state.numeric = true
			}
			expected := versions[position]
			if state.numeric {
				expected = int64(position)
			}
			if version != expected {
				return state, ErrSchema
			}
			state.version = versions[position]
			state.applied[state.version] = at
			position++
		}
		if err := rows.Err(); err != nil {
			return state, dbError(err)
		}
		if position == 0 {
			return state, ErrSchema
		}
	}
	if state.version > 0 && physicalVersion != state.version {
		return state, ErrSchema
	}
	if state.version == 0 && physicalVersion > 0 {
		if physicalVersion != initialVersion {
			return state, ErrSchema
		}
		state.legacy = true
	}
	return state, nil
}

func catalogFingerprint(ctx context.Context, q querier, prefix string) (string, error) {
	var signature string
	if err := q.QueryRow(ctx, schemaSignatureSQL, prefix).Scan(&signature); err != nil {
		return "", dbError(err)
	}
	if signature == "[]" {
		return "", nil
	}
	digest := sha256.Sum256([]byte(signature))
	return hex.EncodeToString(digest[:]), nil
}

// Only application-owned names are inspected. An unrelated application's tables
// may coexist in the database, but unexpected rhythm_/Goose objects are rejected.
const schemaSignatureSQL = `
SELECT COALESCE(jsonb_agg(row_data ORDER BY name COLLATE "C"), '[]'::jsonb)::text
FROM (
SELECT c.relname AS name, jsonb_build_object(
  'name', c.relname, 'kind', c.relkind, 'persistence', c.relpersistence,
  'rls', c.relrowsecurity, 'forceRLS', c.relforcerowsecurity,
  'inherited', EXISTS (SELECT 1 FROM pg_inherits i WHERE i.inhrelid=c.oid OR i.inhparent=c.oid),
  'columns', (SELECT COALESCE(jsonb_agg(jsonb_build_array(a.attnum,a.attname,format_type(a.atttypid,a.atttypmod),a.attnotnull,a.attidentity,a.attgenerated,COALESCE(pg_get_expr(d.adbin,d.adrelid,false),'')) ORDER BY a.attnum),'[]'::jsonb) FROM pg_attribute a LEFT JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum WHERE a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped),
  'constraints', (SELECT COALESCE(jsonb_agg(jsonb_build_array(k.conname,k.contype,k.convalidated,k.condeferrable,k.condeferred,pg_get_constraintdef(k.oid,false)) ORDER BY k.conname COLLATE "C"),'[]'::jsonb) FROM pg_constraint k WHERE k.conrelid=c.oid),
  'index', (SELECT jsonb_build_array(am.amname,i.indisunique,i.indisprimary,i.indisvalid,i.indisready,i.indnkeyatts,i.indnatts,i.indkey::text,i.indclass::text,i.indcollation::text,i.indoption::text,COALESCE(pg_get_expr(i.indexprs,i.indrelid,false),''),COALESCE(pg_get_expr(i.indpred,i.indrelid,false),'')) FROM pg_index i JOIN pg_am am ON am.oid=c.relam WHERE i.indexrelid=c.oid),
  'triggers', (SELECT COALESCE(jsonb_agg(t.tgname ORDER BY t.tgname COLLATE "C"),'[]'::jsonb) FROM pg_trigger t WHERE t.tgrelid=c.oid AND NOT t.tgisinternal)
) AS row_data
FROM pg_class c
WHERE c.relnamespace=current_schema()::regnamespace AND (
  starts_with(c.relname,$1) OR c.oid IN (
    SELECT i.indexrelid FROM pg_index i JOIN pg_class owner ON owner.oid=i.indrelid
    WHERE owner.relnamespace=c.relnamespace AND starts_with(owner.relname,$1)
  )
)
) AS objects`
