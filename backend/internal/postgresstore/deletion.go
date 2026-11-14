package postgresstore

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"

	"rhythm/internal/accounts"
	"rhythm/internal/rhythm"
)

func (s *Store) DeleteAccount(ctx context.Context, deletion accounts.Deletion, authorize func(accounts.Ledger) error) error {
	if !accounts.ValidPrincipal(deletion.Principal) || !strings.HasPrefix(deletion.Principal, "u_") {
		return accounts.ErrUnauthorized
	}
	return s.transaction(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		// Use the same order as registration: auth lock, then the diary row.
		if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", authLock); err != nil {
			return dbError(err)
		}
		ledger, err := readLedger(ctx, tx, accounts.Lookup{Principal: deletion.Principal})
		if err != nil {
			return err
		}
		if err := authorize(ledger); err != nil {
			return err
		}
		record, err := lockedRecord(ctx, tx, deletion.Principal)
		if err != nil {
			return err
		}
		if record.Revision != deletion.Revision {
			return rhythm.ErrConflict
		}
		guest := diaryKey(deletion.Principal)
		for _, statement := range []string{
			"DELETE FROM rhythm_sessions WHERE principal IN ($1,$2)",
			"DELETE FROM rhythm_legacy_revocations WHERE principal IN ($1,$2)",
		} {
			if _, err := tx.Exec(ctx, statement, deletion.Principal, guest); err != nil {
				return dbError(err)
			}
		}
		if _, err := tx.Exec(ctx, "DELETE FROM rhythm_accounts WHERE principal=$1", deletion.Principal); err != nil {
			return dbError(err)
		}
		if _, err := tx.Exec(ctx, "DELETE FROM rhythm_diaries WHERE owner=$1 AND diary_key=$2", deletion.Principal, guest); err != nil {
			return dbError(err)
		}
		// Opaque identity rows remain as tombstones. They contain no credentials
		// or diary; retaining them prevents an admitted old guest request from
		// creating the deleted diary again, even after legacy cookies expire.
		return nil
	})
}

func claimedGuest(ctx context.Context, q querier, principal string) (bool, error) {
	if !strings.HasPrefix(principal, "g_") {
		return false, nil
	}
	var claimed bool
	err := q.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM rhythm_identities WHERE id=$1)", "u_"+principal[2:]).Scan(&claimed)
	return claimed, dbError(err)
}
