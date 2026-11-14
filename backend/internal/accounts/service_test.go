package accounts_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"rhythm/internal/accounts"
	"rhythm/internal/filestore"
)

// Hold the real repository snapshot while another service changes credentials.
// Only the read boundary is delayed; hashing and transactional checks stay real.
type delayedLoginRead struct {
	accounts.Repository
	read   chan struct{}
	resume chan struct{}
}

func (r *delayedLoginRead) ReadAccounts(ctx context.Context, lookup accounts.Lookup) (accounts.Ledger, error) {
	ledger, err := r.Repository.ReadAccounts(ctx, lookup)
	if err != nil {
		return accounts.Ledger{}, err
	}
	close(r.read)
	select {
	case <-r.resume:
		return ledger, nil
	case <-ctx.Done():
		return accounts.Ledger{}, ctx.Err()
	}
}

func TestInFlightLoginCannotRestoreSessionAfterPasswordChange(t *testing.T) {
	store, err := filestore.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := accounts.New(store)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	const oldPassword = "four quiet mornings together"
	const newPassword = "another gentle week begins"
	first, err := service.Register(ctx, "g_"+strings.Repeat("a", 64), 0, "owner", oldPassword, now)
	if err != nil {
		t.Fatal(err)
	}
	delayed := &delayedLoginRead{Repository: store, read: make(chan struct{}), resume: make(chan struct{})}
	staleService, err := accounts.New(delayed)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := staleService.Login(ctx, "owner", oldPassword, now)
		done <- err
	}()
	select {
	case <-delayed.read:
	case <-ctx.Done():
		t.Fatal("login did not reach its credential snapshot")
	}
	current, changeErr := service.ChangePassword(ctx, first.Token, oldPassword, newPassword, now)
	close(delayed.resume)
	if err := <-done; !errors.Is(err, accounts.ErrUnauthorized) {
		t.Fatalf("stale login recreated a session after password change: %v", err)
	}
	if changeErr != nil {
		t.Fatal(changeErr)
	}
	if _, err := service.Resolve(ctx, first.Token, now); !errors.Is(err, accounts.ErrUnauthorized) {
		t.Fatalf("old session survived password change: %v", err)
	}
	if principal, err := service.Resolve(ctx, current.Token, now); err != nil || principal != first.Principal {
		t.Fatalf("new session is unavailable: principal=%q err=%v", principal, err)
	}
	info, err := service.Info(ctx, current.Token, now)
	if err != nil || len(info.Sessions) != 1 || !info.Sessions[0].Current {
		t.Fatalf("password change left extra sessions: %+v err=%v", info.Sessions, err)
	}
}
