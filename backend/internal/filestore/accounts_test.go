package filestore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"rhythm/internal/accounts"
)

func TestCanceledLedgerCommitKeepsSessionsAndRevocations(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := accounts.New(s)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	telegram := "t_" + strings.Repeat("a", 64)
	grant, err := service.NewTelegramSession(ctx, telegram, now)
	if err != nil {
		t.Fatal(err)
	}
	guest := principal("b")
	canceled, cancel := context.WithCancel(ctx)
	defer cancel()
	err = s.UpdateAccounts(canceled, nil, func(ledger *accounts.Ledger) error {
		delete(ledger.Sessions, accounts.TokenHash(grant.Token))
		ledger.BlockedLegacy[guest] = now.Add(accounts.Lifetime)
		cancel()
		return nil
	})
	if !errors.Is(err, accounts.ErrUnavailable) {
		t.Fatalf("canceled ledger commit returned %v", err)
	}
	if who, err := service.Resolve(ctx, grant.Token, now); err != nil || who != telegram {
		t.Fatalf("canceled commit revoked a persisted session: principal=%q err=%v", who, err)
	}
	if err := service.AllowLegacy(ctx, guest, now); err != nil {
		t.Fatalf("canceled commit persisted a guest revocation: %v", err)
	}
	// A new store must observe the same durable state as the original instance.
	reopened, err := New(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := reopened.ReadAccounts(ctx, accounts.Lookup{})
	if err != nil || len(ledger.Sessions) != 1 || len(ledger.BlockedLegacy) != 0 {
		t.Fatalf("restart observed a partial canceled commit: sessions=%d revocations=%d err=%v", len(ledger.Sessions), len(ledger.BlockedLegacy), err)
	}
}

func TestLedgerSnapshotsDoNotShareMutableMaps(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := accounts.New(s)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	guest := principal("b")
	grant, err := service.NewTelegramSession(ctx, "t_"+strings.Repeat("a", 64), now)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Logout(ctx, "", guest, now); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.ReadAccounts(ctx, accounts.Lookup{})
	if err != nil {
		t.Fatal(err)
	}
	clear(snapshot.Sessions)
	clear(snapshot.BlockedLegacy)
	if who, err := service.Resolve(ctx, grant.Token, now); err != nil || who != grant.Principal {
		t.Fatalf("caller changed stored sessions through a read snapshot: principal=%q err=%v", who, err)
	}
	if err := service.AllowLegacy(ctx, guest, now); !errors.Is(err, accounts.ErrUnauthorized) {
		t.Fatalf("caller removed stored revocation through a read snapshot: %v", err)
	}
}
