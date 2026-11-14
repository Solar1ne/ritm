package postgresstore

import (
	"context"
	"errors"
	"maps"
	"strings"

	"github.com/jackc/pgx/v5"

	"rhythm/internal/accounts"
)

func (s *Store) CheckAccounts(ctx context.Context) error {
	var ok bool
	// Touch actual relations without reading or validating every user's hashes.
	err := s.pool.QueryRow(ctx, "SELECT NOT EXISTS (SELECT 1 FROM rhythm_accounts LIMIT 0) AND NOT EXISTS (SELECT 1 FROM rhythm_sessions LIMIT 0) AND NOT EXISTS (SELECT 1 FROM rhythm_legacy_revocations LIMIT 0)").Scan(&ok)
	return dbError(err)
}

func (s *Store) ReadAccounts(ctx context.Context, lookup accounts.Lookup) (accounts.Ledger, error) {
	var ledger accounts.Ledger
	err := s.transaction(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		var err error
		ledger, err = readLedger(ctx, tx, lookup)
		return err
	})
	return ledger, err
}

func readLedger(ctx context.Context, q querier, lookup accounts.Lookup) (accounts.Ledger, error) {
	l := accounts.EmptyLedger()
	all := lookup == (accounts.Lookup{})
	principal := lookup.Principal
	if lookup.SessionDigest != "" {
		err := q.QueryRow(ctx, "SELECT principal FROM rhythm_sessions WHERE digest=$1", lookup.SessionDigest).Scan(&principal)
		if errors.Is(err, pgx.ErrNoRows) {
			return l, nil
		}
		if err != nil {
			return l, dbError(err)
		}
	}
	statement := "SELECT principal,login,password_salt,password_hash,password_iterations,recovery_digest,created_at FROM rhythm_accounts"
	args := []any{}
	if lookup.Login != "" {
		statement += " WHERE login=$1"
		args = append(args, lookup.Login)
	} else if !all {
		owner := principal
		if strings.HasPrefix(owner, "g_") {
			owner = "u_" + owner[2:]
		}
		statement += " WHERE principal=$1"
		args = append(args, owner)
	}
	statement += " LIMIT 10001"
	rows, err := q.Query(ctx, statement, args...)
	if err != nil {
		return l, dbError(err)
	}
	for rows.Next() {
		var id string
		var a accounts.Account
		if err := rows.Scan(&id, &a.Login, &a.Password.Salt, &a.Password.Digest, &a.Password.Iterations, &a.RecoveryDigest, &a.CreatedAt); err != nil {
			rows.Close()
			return l, dbError(err)
		}
		a.CreatedAt = a.CreatedAt.UTC()
		l.Accounts[id] = a
		if lookup.Login != "" {
			principal = id
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return l, dbError(err)
	}
	if strings.HasPrefix(lookup.Principal, "g_") && len(l.Accounts) == 0 {
		claimed, err := claimedGuest(ctx, q, lookup.Principal)
		if err != nil {
			return l, err
		}
		if claimed {
			return l, accounts.ErrUnauthorized
		}
	}
	if !all && principal == "" {
		return l, l.Validate()
	}
	where := ""
	args = nil
	if !all {
		where, args = " WHERE principal=$1", []any{principal}
	}
	rows, err = q.Query(ctx, "SELECT digest,principal,created_at,expires_at FROM rhythm_sessions"+where+" LIMIT 50001", args...)
	if err != nil {
		return l, dbError(err)
	}
	for rows.Next() {
		var digest string
		var session accounts.Session
		if err := rows.Scan(&digest, &session.Principal, &session.CreatedAt, &session.ExpiresAt); err != nil {
			rows.Close()
			return l, dbError(err)
		}
		session.CreatedAt, session.ExpiresAt = session.CreatedAt.UTC(), session.ExpiresAt.UTC()
		l.Sessions[digest] = session
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return l, dbError(err)
	}
	rows, err = q.Query(ctx, "SELECT principal,expires_at FROM rhythm_legacy_revocations"+where+" LIMIT 50001", args...)
	if err != nil {
		return l, dbError(err)
	}
	for rows.Next() {
		var principal string
		var expiry accounts.Session
		if err := rows.Scan(&principal, &expiry.ExpiresAt); err != nil {
			rows.Close()
			return l, dbError(err)
		}
		l.BlockedLegacy[principal] = expiry.ExpiresAt.UTC()
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return l, dbError(err)
	}
	return l, l.Validate()
}

// Auth writes use a bounded snapshot under a database-wide transaction lock.
// Only changed rows are written. Diary writes/read authentication do not take
// this lock; the existing service callback retains its atomicity and quotas.
func (s *Store) UpdateAccounts(ctx context.Context, claim *accounts.Claim, apply func(*accounts.Ledger) error) error {
	return s.transaction(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", authLock); err != nil {
			return dbError(err)
		}
		before, err := readLedger(ctx, tx, accounts.Lookup{})
		if err != nil {
			return err
		}
		if claim != nil {
			if !accounts.ValidPrincipal(claim.Guest) || !strings.HasPrefix(claim.Guest, "g_") {
				return accounts.ErrConflict
			}
			if _, exists := before.Accounts["u_"+claim.Guest[2:]]; exists {
				return accounts.ErrConflict
			}
			record, err := lockedRecord(ctx, tx, claim.Guest)
			if err != nil {
				return err
			}
			if record.Revision != claim.Revision {
				return accounts.ErrConflict
			}
		}
		next := accounts.Ledger{Version: before.Version, Accounts: maps.Clone(before.Accounts), Sessions: maps.Clone(before.Sessions), BlockedLegacy: maps.Clone(before.BlockedLegacy)}
		if err := apply(&next); err != nil {
			return err
		}
		if err := next.Validate(); err != nil {
			return err
		}
		if err := saveLedgerDiff(ctx, tx, before, next); err != nil {
			return err
		}
		if claim != nil {
			owner := "u_" + claim.Guest[2:]
			if _, exists := next.Accounts[owner]; !exists {
				return accounts.ErrConflict
			}
			if _, err := tx.Exec(ctx, "UPDATE rhythm_diaries SET owner=$2 WHERE diary_key=$1", claim.Guest, owner); err != nil {
				return dbError(err)
			}
		}
		return nil
	})
}

func saveLedgerDiff(ctx context.Context, tx pgx.Tx, before, next accounts.Ledger) error {
	for digest := range before.Sessions {
		if _, exists := next.Sessions[digest]; !exists {
			if _, err := tx.Exec(ctx, "DELETE FROM rhythm_sessions WHERE digest=$1", digest); err != nil {
				return dbError(err)
			}
		}
	}
	for principal := range before.Accounts {
		if _, exists := next.Accounts[principal]; !exists {
			// Account deletion is not part of the current use-case contract.
			return accounts.ErrUnavailable
		}
	}
	for principal, a := range next.Accounts {
		if old, exists := before.Accounts[principal]; exists && old == a {
			continue
		}
		if err := identity(ctx, tx, principal); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO rhythm_accounts (principal,login,password_salt,password_hash,password_iterations,recovery_digest,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (principal) DO UPDATE SET login=EXCLUDED.login,password_salt=EXCLUDED.password_salt,password_hash=EXCLUDED.password_hash,password_iterations=EXCLUDED.password_iterations,recovery_digest=EXCLUDED.recovery_digest`, principal, a.Login, a.Password.Salt, a.Password.Digest, a.Password.Iterations, a.RecoveryDigest, a.CreatedAt)
		if err != nil {
			return dbError(err)
		}
	}
	for digest, session := range next.Sessions {
		if old, exists := before.Sessions[digest]; exists && old == session {
			continue
		}
		if err := identity(ctx, tx, session.Principal); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO rhythm_sessions (digest,principal,created_at,expires_at) VALUES ($1,$2,$3,$4)
		ON CONFLICT (digest) DO UPDATE SET principal=EXCLUDED.principal,created_at=EXCLUDED.created_at,expires_at=EXCLUDED.expires_at`, digest, session.Principal, session.CreatedAt, session.ExpiresAt)
		if err != nil {
			return dbError(err)
		}
	}
	for principal := range before.BlockedLegacy {
		if _, exists := next.BlockedLegacy[principal]; !exists {
			if _, err := tx.Exec(ctx, "DELETE FROM rhythm_legacy_revocations WHERE principal=$1", principal); err != nil {
				return dbError(err)
			}
		}
	}
	for principal, expiry := range next.BlockedLegacy {
		if old, exists := before.BlockedLegacy[principal]; exists && old.Equal(expiry) {
			continue
		}
		if err := identity(ctx, tx, principal); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "INSERT INTO rhythm_legacy_revocations (principal,expires_at) VALUES ($1,$2) ON CONFLICT (principal) DO UPDATE SET expires_at=EXCLUDED.expires_at", principal, expiry); err != nil {
			return dbError(err)
		}
	}
	return nil
}
