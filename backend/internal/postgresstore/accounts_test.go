package postgresstore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"rhythm/internal/accounts"
	"rhythm/internal/filestore"
)

func TestCanceledLedgerCommitKeepsSessionsAndRevocations(t *testing.T) {
	s, _ := testDatabase(t, true)
	a := service(t, s)
	ctx := context.Background()
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	telegram := "t_" + strings.Repeat("a", 64)
	grant, err := a.NewTelegramSession(ctx, telegram, now)
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	defer cancel()
	err = s.UpdateAccounts(canceled, nil, func(ledger *accounts.Ledger) error {
		delete(ledger.Sessions, accounts.TokenHash(grant.Token))
		ledger.BlockedLegacy[guest("b")] = now.Add(accounts.Lifetime)
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled ledger commit returned %v", err)
	}
	if who, err := a.Resolve(ctx, grant.Token, now); err != nil || who != telegram {
		t.Fatalf("canceled commit revoked a persisted session: principal=%q err=%v", who, err)
	}
	if err := a.AllowLegacy(ctx, guest("b"), now); err != nil {
		t.Fatalf("canceled commit persisted a guest revocation: %v", err)
	}
}

func TestScopedLedgerReadsExcludeOtherAccountsAndSessions(t *testing.T) {
	s, _ := testDatabase(t, true)
	a := service(t, s)
	ctx := context.Background()
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	first, err := a.Register(ctx, guest("a"), 0, "alice", "a long synthetic password", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Register(ctx, guest("b"), 0, "bob", "a different synthetic password", now); err != nil {
		t.Fatal(err)
	}
	if _, err := a.NewTelegramSession(ctx, "t_"+strings.Repeat("c", 64), now); err != nil {
		t.Fatal(err)
	}
	if err := a.Logout(ctx, "", guest("d"), now); err != nil {
		t.Fatal(err)
	}
	for _, lookup := range []accounts.Lookup{
		{Login: "alice"},
		{Principal: first.Principal},
		{SessionDigest: accounts.TokenHash(first.Token)},
	} {
		ledger, err := s.ReadAccounts(ctx, lookup)
		if err != nil {
			t.Fatal(err)
		}
		account, exists := ledger.Accounts[first.Principal]
		session, current := ledger.Sessions[accounts.TokenHash(first.Token)]
		if len(ledger.Accounts) != 1 || !exists || account.Login != "alice" || len(ledger.Sessions) != 1 || !current || session.Principal != first.Principal || len(ledger.BlockedLegacy) != 0 {
			t.Fatalf("scoped lookup leaked or lost identities: lookup=%+v accounts=%d sessions=%d revocations=%d", lookup, len(ledger.Accounts), len(ledger.Sessions), len(ledger.BlockedLegacy))
		}
	}
	unknown, err := s.ReadAccounts(ctx, accounts.Lookup{SessionDigest: strings.Repeat("f", 64)})
	if err != nil || len(unknown.Accounts) != 0 || len(unknown.Sessions) != 0 || len(unknown.BlockedLegacy) != 0 {
		t.Fatalf("unknown session exposed a ledger: accounts=%d sessions=%d revocations=%d err=%v", len(unknown.Accounts), len(unknown.Sessions), len(unknown.BlockedLegacy), err)
	}
}

func TestOfflineImportPreservesTelegramSessionAndGuestRevocation(t *testing.T) {
	s, _ := testDatabase(t, true)
	ctx := context.Background()
	dir := t.TempDir()
	file, err := filestore.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.SessionSecret(ctx, ""); err != nil {
		t.Fatal(err)
	}
	source, err := accounts.New(file)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	telegram := "t_" + strings.Repeat("a", 64)
	grant, err := source.NewTelegramSession(ctx, telegram, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := source.Logout(ctx, "", guest("b"), now); err != nil {
		t.Fatal(err)
	}
	err = filestore.WithSnapshot(ctx, dir, "", func(snapshot filestore.Snapshot) error {
		_, err := s.Import(ctx, ImportData{Records: snapshot.Records, Accounts: snapshot.Accounts, Secret: snapshot.Secret, Fingerprint: snapshot.Fingerprint}, now)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	imported := service(t, s)
	if who, err := imported.Resolve(ctx, grant.Token, now); err != nil || who != telegram {
		t.Fatalf("migration lost Telegram session: principal=%q err=%v", who, err)
	}
	if err := imported.AllowLegacy(ctx, guest("b"), now); !errors.Is(err, accounts.ErrUnauthorized) {
		t.Fatalf("migration restored a revoked guest cookie: %v", err)
	}
	if err := imported.AllowLegacy(ctx, guest("b"), now.Add(accounts.Lifetime)); err != nil {
		t.Fatalf("migration changed the guest revocation expiry: %v", err)
	}
}
