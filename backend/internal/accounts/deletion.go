package accounts

import (
	"context"
	"fmt"
	"time"
)

var ErrDeletionUnavailable = fmt.Errorf("%w: atomic account deletion is unsupported", ErrUnavailable)

// Deletion identifies the diary revision the owner explicitly reviewed. The
// adapter deletes credentials, diary and every related session atomically.
type Deletion struct {
	Principal string
	Revision  uint64
}

// Kept separate from Repository because single-file adapters cannot provide an
// atomic deletion across their ledger and diary files.
type deletionRepository interface {
	DeleteAccount(context.Context, Deletion, func(Ledger) error) error
}

func (s *Service) Delete(ctx context.Context, bearer, password string, revision uint64, now time.Time) error {
	l, err := s.repo.ReadAccounts(ctx, Lookup{SessionDigest: TokenHash(bearer)})
	if err != nil {
		return err
	}
	principal, account, err := l.accountSession(bearer, now)
	if err != nil || !verifyPassword(account.Password, password) {
		return ErrUnauthorized
	}
	repo, ok := s.repo.(deletionRepository)
	if !ok {
		return ErrDeletionUnavailable
	}
	return repo.DeleteAccount(ctx, Deletion{Principal: principal, Revision: revision}, func(current Ledger) error {
		id, latest, err := current.accountSession(bearer, now)
		if err != nil || id != principal || latest.Password != account.Password || latest.RecoveryDigest != account.RecoveryDigest {
			return ErrUnauthorized
		}
		return nil
	})
}
