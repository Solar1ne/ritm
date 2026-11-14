package filestore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"rhythm/internal/accounts"
)

const maxAccountBytes = 32 * 1024 * 1024

func (s *Store) CheckAccounts(ctx context.Context) error {
	_, err := s.ReadAccounts(ctx, accounts.Lookup{})
	return err
}

// Account mutations and diary mutations share the same lock. Claiming a guest
// therefore checks the exact diary revision and invalidates the old ownership
// atomically. There is no multi-file copy, rename or compensating transaction.
func (s *Store) ReadAccounts(ctx context.Context, _ accounts.Lookup) (accounts.Ledger, error) {
	if err := s.acquire(ctx); err != nil {
		return accounts.Ledger{}, fmt.Errorf("%w: acquire ledger: %w", accounts.ErrUnavailable, err)
	}
	defer s.release()
	ledger, err := s.readAccounts(ctx)
	if err != nil {
		return accounts.Ledger{}, fmt.Errorf("%w: read ledger: %w", accounts.ErrUnavailable, err)
	}
	return ledger, nil
}

func (s *Store) UpdateAccounts(ctx context.Context, claim *accounts.Claim, apply func(*accounts.Ledger) error) error {
	if err := s.acquire(ctx); err != nil {
		return err
	}
	defer s.release()
	ledger, err := s.readAccounts(ctx)
	if err != nil {
		return err
	}
	if claim != nil {
		if !accounts.ValidPrincipal(claim.Guest) || !strings.HasPrefix(claim.Guest, "g_") {
			return accounts.ErrConflict
		}
		if _, claimed := ledger.Accounts["u_"+claim.Guest[2:]]; claimed {
			return accounts.ErrConflict
		}
		record, err := s.read(ctx, claim.Guest)
		if err != nil {
			return err
		}
		if record.Revision != claim.Revision {
			return accounts.ErrConflict
		}
	}
	// The callback exclusively owns this freshly decoded ledger. It must not
	// retain references; no mutable objects are cached or shared between calls.
	if err := apply(&ledger); err != nil {
		return err
	}
	if err := ledger.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(ledger)
	if err != nil {
		return fmt.Errorf("%w: encode ledger: %v", accounts.ErrUnavailable, err)
	}
	if len(data) > maxAccountBytes {
		return accounts.ErrCapacity
	}
	if err := s.commit(ctx, filepath.Join(s.dir, ".accounts.json"), data); err != nil {
		return fmt.Errorf("%w: commit ledger: %v", accounts.ErrUnavailable, err)
	}
	return nil
}

func (s *Store) readAccounts(ctx context.Context) (accounts.Ledger, error) {
	if err := s.Check(ctx); err != nil {
		return accounts.Ledger{}, fmt.Errorf("%w: %w", accounts.ErrUnavailable, err)
	}
	f, err := os.Open(filepath.Join(s.dir, ".accounts.json"))
	if errors.Is(err, os.ErrNotExist) {
		return accounts.EmptyLedger(), nil
	}
	if err != nil {
		return accounts.Ledger{}, fmt.Errorf("%w: open ledger: %v", accounts.ErrUnavailable, err)
	}
	defer f.Close() // read-only handle
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxAccountBytes {
		return accounts.Ledger{}, accounts.ErrUnavailable
	}
	decoder := json.NewDecoder(io.LimitReader(f, maxAccountBytes+1))
	decoder.DisallowUnknownFields()
	var ledger accounts.Ledger
	if err := decoder.Decode(&ledger); err != nil {
		return accounts.Ledger{}, fmt.Errorf("%w: decode ledger: %v", accounts.ErrUnavailable, err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return accounts.Ledger{}, accounts.ErrUnavailable
	}
	if err := ledger.Validate(); err != nil {
		return accounts.Ledger{}, err
	}
	if err := ctx.Err(); err != nil {
		return accounts.Ledger{}, err
	}
	return ledger, nil
}

// u_<id> owns the diary formerly addressed by g_<id>. Only ownership changes;
// the data file remains stable. This mapping is local to the storage adapter.
func recordFile(principal string) string {
	if strings.HasPrefix(principal, "u_") {
		principal = "g_" + principal[2:]
	}
	return principal + ".json"
}

func (s *Store) checkDiaryOwner(ctx context.Context, principal string) error {
	if strings.HasPrefix(principal, "t_") {
		return nil
	}
	ledger, err := s.readAccounts(ctx)
	if err != nil {
		return err
	}
	_, claimed := ledger.Accounts["u_"+principal[2:]]
	if strings.HasPrefix(principal, "u_") != claimed {
		return accounts.ErrUnauthorized
	}
	return nil
}
