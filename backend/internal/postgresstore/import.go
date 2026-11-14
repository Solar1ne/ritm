package postgresstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"rhythm/internal/accounts"
	"rhythm/internal/rhythm"
)

// ImportData is an offline adapter input, intentionally separate from HTTP backup.
type ImportData struct {
	Records     map[string]rhythm.Record
	Accounts    accounts.Ledger
	Secret      []byte
	Fingerprint string
}

type ImportResult struct {
	Diaries         int  `json:"diaries"`
	Accounts        int  `json:"accounts"`
	AlreadyImported bool `json:"alreadyImported"`
}

// Preflight is advisory and read-only. Import repeats its checks under locks.
func (s *Store) PreflightImport(ctx context.Context, data ImportData, now time.Time) error {
	if err := validateImport(data, now); err != nil {
		return err
	}
	if err := s.Check(ctx); err != nil {
		return err
	}
	var allowed bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM rhythm_imports WHERE fingerprint=$1) OR
	(NOT EXISTS(SELECT 1 FROM rhythm_identities) AND NOT EXISTS(SELECT 1 FROM rhythm_metadata) AND NOT EXISTS(SELECT 1 FROM rhythm_imports))`, data.Fingerprint).Scan(&allowed); err != nil {
		return dbError(err)
	}
	if !allowed {
		return errors.New("target database is not empty; import refused")
	}
	// Check PostgreSQL's actual representation before the source is fenced.
	// Compact JSON can fit the file limit while canonical JSONB exceeds it.
	for _, record := range data.Records {
		body, err := json.Marshal(record)
		if err != nil {
			return err
		}
		var size int
		if err := s.pool.QueryRow(ctx, "SELECT octet_length($1::jsonb::text)", body).Scan(&size); err != nil {
			return dbError(err)
		}
		if size > maxRecordBytes {
			return rhythm.ErrCapacity
		}
	}
	return nil
}

func validateImport(data ImportData, now time.Time) error {
	if len(data.Secret) < 32 || !accounts.ValidPrincipal("g_"+data.Fingerprint) || len(data.Records) > 10000 {
		return errors.New("invalid import envelope")
	}
	if err := data.Accounts.Validate(); err != nil {
		return err
	}
	for principal, record := range data.Records {
		if !accounts.ValidPrincipal(principal) || record.Revision > math.MaxInt64 {
			return errors.New("invalid imported identity or revision")
		}
		_, claimed := data.Accounts.Accounts["u_"+principal[2:]]
		if (strings.HasPrefix(principal, "u_") && !claimed) || (strings.HasPrefix(principal, "g_") && claimed) {
			return errors.New("imported diary ownership conflicts with accounts")
		}
		checked, err := rhythm.EnsureWeeks(record, now)
		if err != nil {
			return errors.New("imported diary graph is invalid")
		}
		if err := rhythm.ValidateExtras(checked, now); err != nil {
			return errors.New("imported diary extras are invalid")
		}
		if checked.Profile != nil {
			if err := rhythm.ValidateArchive(checked, now); err != nil {
				return errors.New("imported diary graph is invalid")
			}
		}
		body, err := json.Marshal(record)
		if err != nil {
			return err
		}
		if _, err := decodeRecord(body, int64(record.Revision)); err != nil {
			return err
		}
	}
	for principal := range data.Accounts.Accounts {
		if _, ok := data.Records[principal]; !ok {
			return errors.New("account diary is absent from import")
		}
	}
	return nil
}

// Import validates before writes and imports everything in one transaction into
// an empty target. It never merges histories or overwrites a running database.
func (s *Store) Import(ctx context.Context, data ImportData, now time.Time) (ImportResult, error) {
	result := ImportResult{Diaries: len(data.Records), Accounts: len(data.Accounts.Accounts)}
	if err := validateImport(data, now); err != nil {
		return ImportResult{}, err
	}
	err := s.transaction(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", authLock); err != nil {
			return dbError(err)
		}
		// Lock all target relations: an empty check must not race initial traffic.
		if _, err := tx.Exec(ctx, `LOCK TABLE rhythm_identities,rhythm_diaries,rhythm_accounts,rhythm_sessions,rhythm_legacy_revocations,rhythm_metadata,rhythm_imports IN ACCESS EXCLUSIVE MODE`); err != nil {
			return dbError(err)
		}
		if err := s.checkSchema(ctx, tx); err != nil {
			return err
		}
		var imported bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM rhythm_imports WHERE fingerprint=$1)", data.Fingerprint).Scan(&imported); err != nil {
			return dbError(err)
		}
		if imported {
			result.AlreadyImported = true
			return nil
		}
		var nonempty bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM rhythm_identities) OR EXISTS(SELECT 1 FROM rhythm_metadata) OR EXISTS(SELECT 1 FROM rhythm_imports)").Scan(&nonempty); err != nil {
			return dbError(err)
		}
		if nonempty {
			return errors.New("target database is not empty; import refused")
		}
		keys := make([]string, 0, len(data.Records))
		for key := range data.Records {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, owner := range keys {
			record := data.Records[owner]
			key := diaryKey(owner)
			if err := identity(ctx, tx, key); err != nil {
				return err
			}
			if owner != key {
				if err := identity(ctx, tx, owner); err != nil {
					return err
				}
			}
			body, err := json.Marshal(record)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, "INSERT INTO rhythm_diaries(diary_key,owner,revision,document) VALUES ($1,$2,$3,$4)", key, owner, int64(record.Revision), body); err != nil {
				return dbError(err)
			}
			var saved []byte
			if err := tx.QueryRow(ctx, "SELECT document FROM rhythm_diaries WHERE diary_key=$1", key).Scan(&saved); err != nil {
				return dbError(err)
			}
			var roundtrip, expected rhythm.Record
			if json.Unmarshal(saved, &roundtrip) != nil || json.Unmarshal(body, &expected) != nil || !reflect.DeepEqual(expected, roundtrip) {
				return errors.New("diary import read-back mismatch")
			}
		}
		if err := saveLedgerDiff(ctx, tx, accounts.EmptyLedger(), data.Accounts); err != nil {
			return err
		}
		ledger, err := readLedger(ctx, tx, accounts.Lookup{})
		if err != nil {
			return err
		}
		// PostgreSQL timestamptz has microsecond precision; credential bytes and
		// all ownership/session relationships must otherwise survive unchanged.
		expected := accounts.EmptyLedger()
		for id, account := range data.Accounts.Accounts {
			account.CreatedAt = account.CreatedAt.UTC().Truncate(time.Microsecond)
			expected.Accounts[id] = account
		}
		for digest, session := range data.Accounts.Sessions {
			session.CreatedAt = session.CreatedAt.UTC().Truncate(time.Microsecond)
			session.ExpiresAt = session.ExpiresAt.UTC().Truncate(time.Microsecond)
			expected.Sessions[digest] = session
		}
		for id, expiry := range data.Accounts.BlockedLegacy {
			expected.BlockedLegacy[id] = expiry.UTC().Truncate(time.Microsecond)
		}
		if !reflect.DeepEqual(expected, ledger) {
			return errors.New("account import read-back mismatch")
		}
		if _, err := tx.Exec(ctx, "INSERT INTO rhythm_metadata(key,value) VALUES ('session-secret',$1)", data.Secret); err != nil {
			return dbError(err)
		}
		var key []byte
		if err := tx.QueryRow(ctx, "SELECT value FROM rhythm_metadata WHERE key='session-secret'").Scan(&key); err != nil {
			return dbError(err)
		}
		if !bytes.Equal(key, data.Secret) {
			return errors.New("identity key read-back mismatch")
		}
		if _, err := tx.Exec(ctx, "INSERT INTO rhythm_imports(fingerprint,diary_count,account_count) VALUES ($1,$2,$3)", data.Fingerprint, result.Diaries, result.Accounts); err != nil {
			return dbError(err)
		}
		return nil
	})
	return result, err
}
