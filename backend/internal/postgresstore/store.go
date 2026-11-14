// Package postgresstore is the PostgreSQL adapter for the diary and account
// contracts. Business rules remain in rhythm/accounts; SQL models stay here.
package postgresstore

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"rhythm/internal/accounts"
	"rhythm/internal/rhythm"
)

const maxRecordBytes = 4 * 1024 * 1024
const authLock int64 = 72687901
const schemaLock int64 = 72687902

var ErrSchema = errors.New("database schema is incompatible; run rhythm-db migrate")

// Store owns its pool. Close only after all HTTP requests have drained.
type Store struct{ pool *pgxpool.Pool }

func Open(ctx context.Context, databaseURL string) (*Store, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, errors.New("invalid DATABASE_URL") // Parse errors can contain credentials.
	}
	if !secureEndpoint(config.ConnConfig.Host, config.ConnConfig.TLSConfig) {
		return nil, errors.New("remote PostgreSQL requires verified TLS; use sslmode=verify-full")
	}
	for _, fallback := range config.ConnConfig.Fallbacks {
		if !secureEndpoint(fallback.Host, fallback.TLSConfig) {
			return nil, errors.New("remote PostgreSQL fallback requires verified TLS; use sslmode=verify-full")
		}
	}
	config.MaxConns, config.MinConns = 10, 0
	config.MaxConnLifetime = 30 * time.Minute
	config.MaxConnIdleTime = 5 * time.Minute
	config.ConnConfig.ConnectTimeout = 3 * time.Second
	config.ConnConfig.RuntimeParams["application_name"] = "rhythm"
	config.ConnConfig.RuntimeParams["timezone"] = "UTC"
	config.ConnConfig.RuntimeParams["statement_timeout"] = "5000"
	config.ConnConfig.RuntimeParams["lock_timeout"] = "3000"
	config.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = "10000"
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, dbError(err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, dbError(err)
	}
	return &Store{pool: pool}, nil
}

func secureEndpoint(host string, config *tls.Config) bool {
	return localHost(host) || (config != nil && !config.InsecureSkipVerify && config.ServerName != "")
}

func localHost(host string) bool {
	if strings.HasPrefix(host, "/") || host == "localhost" || host == "db" {
		// db is the isolated Docker Compose service; it has no published DB port.
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (s *Store) Close() { s.pool.Close() }

// dbError deliberately exposes only SQLSTATE, never SQL text, DSN or row values.
func dbError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return errors.Join(accounts.ErrUnavailable, err)
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return fmt.Errorf("%w (SQLSTATE %s)", accounts.ErrUnavailable, pg.Code)
	}
	return accounts.ErrUnavailable
}

func (s *Store) transaction(ctx context.Context, options pgx.TxOptions, apply func(pgx.Tx) error) (result error) {
	tx, err := s.pool.BeginTx(ctx, options)
	if err != nil {
		return dbError(err)
	}
	// Also release the connection if a programming bug panics inside a callback;
	// the HTTP panic boundary must not leave a transaction checked out forever.
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		if rollbackErr := tx.Rollback(cleanup); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			result = errors.Join(result, dbError(rollbackErr))
		}
	}()
	if err := apply(tx); err != nil {
		return err
	}
	return dbError(tx.Commit(ctx))
}

func (s *Store) Check(ctx context.Context) error { return s.checkSchema(ctx, s.pool) }

type querier interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func diaryKey(principal string) string {
	if strings.HasPrefix(principal, "u_") {
		return "g_" + principal[2:]
	}
	return principal
}

func identity(ctx context.Context, tx pgx.Tx, principal string) error {
	_, err := tx.Exec(ctx, "INSERT INTO rhythm_identities (id) VALUES ($1) ON CONFLICT DO NOTHING", principal)
	return dbError(err)
}

func (s *Store) Read(ctx context.Context, principal string) (rhythm.Record, error) {
	if !accounts.ValidPrincipal(principal) {
		return rhythm.Record{}, accounts.ErrUnauthorized
	}
	var owner string
	var body []byte
	var revision int64
	err := s.pool.QueryRow(ctx, "SELECT owner, revision, document FROM rhythm_diaries WHERE diary_key=$1", diaryKey(principal)).Scan(&owner, &revision, &body)
	if errors.Is(err, pgx.ErrNoRows) && !strings.HasPrefix(principal, "u_") {
		claimed, err := claimedGuest(ctx, s.pool, principal)
		if err != nil {
			return rhythm.Record{}, err
		}
		if claimed {
			return rhythm.Record{}, accounts.ErrUnauthorized
		}
		return rhythm.Blank(), nil
	}
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && owner != principal) {
		return rhythm.Record{}, accounts.ErrUnauthorized
	}
	if err != nil {
		return rhythm.Record{}, dbError(err)
	}
	return decodeRecord(body, revision)
}

// lockedRecord creates only an unclaimed guest/Telegram diary. A missing user
// diary is an error, never an opportunity to bypass account ownership.
func lockedRecord(ctx context.Context, tx pgx.Tx, principal string) (rhythm.Record, error) {
	if !accounts.ValidPrincipal(principal) {
		return rhythm.Record{}, accounts.ErrUnauthorized
	}
	key := diaryKey(principal)
	if !strings.HasPrefix(principal, "u_") {
		claimed, err := claimedGuest(ctx, tx, principal)
		if err != nil {
			return rhythm.Record{}, err
		}
		if claimed {
			return rhythm.Record{}, accounts.ErrUnauthorized
		}
		if err := identity(ctx, tx, principal); err != nil {
			return rhythm.Record{}, err
		}
		body, err := json.Marshal(rhythm.Blank())
		if err != nil {
			return rhythm.Record{}, err
		}
		if _, err := tx.Exec(ctx, "INSERT INTO rhythm_diaries (diary_key,owner,revision,document) VALUES ($1,$1,0,$2) ON CONFLICT (diary_key) DO NOTHING", key, body); err != nil {
			return rhythm.Record{}, dbError(err)
		}
	}
	var owner string
	var revision int64
	var body []byte
	err := tx.QueryRow(ctx, "SELECT owner,revision,document FROM rhythm_diaries WHERE diary_key=$1 FOR UPDATE", key).Scan(&owner, &revision, &body)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && owner != principal) {
		return rhythm.Record{}, accounts.ErrUnauthorized
	}
	if err != nil {
		return rhythm.Record{}, dbError(err)
	}
	// A guest can pass the first check, pause, and resume its INSERT after
	// registration followed by deletion. Repeat under the diary row lock so
	// such an insertion is rolled back instead of resurrecting the old diary.
	claimed, err := claimedGuest(ctx, tx, principal)
	if err != nil {
		return rhythm.Record{}, err
	}
	if claimed {
		return rhythm.Record{}, accounts.ErrUnauthorized
	}
	return decodeRecord(body, revision)
}

func decodeRecord(body []byte, revision int64) (rhythm.Record, error) {
	if len(body) > maxRecordBytes || revision < 0 {
		return rhythm.Record{}, accounts.ErrUnavailable
	}
	var record rhythm.Record
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil || record.Revision != uint64(revision) {
		return rhythm.Record{}, accounts.ErrUnavailable
	}
	if len(record.Entries) > rhythm.MaxEntries || len(record.Receipts) > rhythm.MaxEntries || len(record.Weeks) > rhythm.MaxWeeks || len(record.Workouts) > rhythm.MaxWorkouts || len(record.HRVEntries) > rhythm.MaxWellnessEntries || len(record.HRVReceipts) > rhythm.MaxWellnessEntries || len(record.BodyEntries) > rhythm.MaxWellnessEntries || len(record.BodyReceipts) > rhythm.MaxWellnessEntries || len(record.MealSelections) > rhythm.MaxMealSelections {
		return rhythm.Record{}, accounts.ErrUnavailable
	}
	if record.Profile != nil {
		if err := rhythm.ValidateProfile(*record.Profile); err != nil {
			return rhythm.Record{}, accounts.ErrUnavailable
		}
	}
	if record.Entries == nil {
		record.Entries = []rhythm.Entry{}
	}
	return record, nil
}

func (s *Store) Update(ctx context.Context, principal string, apply func(rhythm.Record) (rhythm.Record, error)) (rhythm.Record, error) {
	var result rhythm.Record
	err := s.transaction(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		current, err := lockedRecord(ctx, tx, principal)
		if err != nil {
			return err
		}
		next, err := apply(current)
		if err != nil {
			return err
		}
		if next.Revision == current.Revision {
			result = rhythm.Clone(current)
			return nil
		}
		if next.Revision > math.MaxInt64 {
			return rhythm.ErrCapacity
		}
		body, err := json.Marshal(next)
		if err != nil {
			return fmt.Errorf("encode diary: %w", err)
		}
		if len(body) > maxRecordBytes {
			return rhythm.ErrCapacity
		}
		// JSONB's canonical representation may be slightly larger than compact JSON.
		var size int
		if err := tx.QueryRow(ctx, "SELECT octet_length($1::jsonb::text)", body).Scan(&size); err != nil {
			return dbError(err)
		}
		if size > maxRecordBytes {
			return rhythm.ErrCapacity
		}
		if _, err := tx.Exec(ctx, "UPDATE rhythm_diaries SET revision=$2, document=$3, updated_at=CURRENT_TIMESTAMP WHERE diary_key=$1", diaryKey(principal), int64(next.Revision), body); err != nil {
			return dbError(err)
		}
		result = rhythm.Clone(next)
		return nil
	})
	if err != nil {
		return rhythm.Record{}, err
	}
	return result, nil
}

func (s *Store) SessionSecret(ctx context.Context, configured string) ([]byte, error) {
	if configured != "" && len(configured) < 32 {
		return nil, errors.New("SESSION_SECRET must contain at least 32 bytes")
	}
	var key []byte
	err := s.transaction(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", authLock); err != nil {
			return dbError(err)
		}
		err := tx.QueryRow(ctx, "SELECT value FROM rhythm_metadata WHERE key='session-secret'").Scan(&key)
		if err == nil {
			if len(key) < 32 {
				return accounts.ErrUnavailable
			}
			if configured != "" && !bytes.Equal(key, []byte(configured)) {
				return errors.New("SESSION_SECRET differs from the database identity key; explicit key migration is required")
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return dbError(err)
		}
		var nonempty bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM rhythm_identities) OR EXISTS (SELECT 1 FROM rhythm_imports)").Scan(&nonempty); err != nil {
			return dbError(err)
		}
		if nonempty {
			return errors.New("nonempty database is missing its identity key; restore the original key")
		}
		key = []byte(configured)
		if configured == "" {
			key = make([]byte, 32)
			if _, err := rand.Read(key); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, "INSERT INTO rhythm_metadata (key,value) VALUES ('session-secret',$1)", key)
		return dbError(err)
	})
	return key, err
}
