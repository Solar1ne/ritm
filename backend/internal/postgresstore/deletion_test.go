package postgresstore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"rhythm/internal/accounts"
	"rhythm/internal/rhythm"
)

const deletionPassword = "four quiet mornings together"

func TestAccountDeletionRemovesPrivateDataAndRevokesFormerOwners(t *testing.T) {
	s, _ := testDatabase(t, true)
	a := service(t, s)
	ctx := context.Background()
	now := time.Now().UTC()
	g := guest("a")
	if err := writeProfile(s, g, 0); err != nil {
		t.Fatal(err)
	}
	first, err := a.Register(ctx, g, 1, "owner", deletionPassword, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.Login(ctx, "owner", deletionPassword, now)
	if err != nil {
		t.Fatal(err)
	}
	other, err := a.Register(ctx, guest("b"), 0, "another", deletionPassword, now)
	if err != nil {
		t.Fatal(err)
	}
	guestToken := strings.Repeat("a", 43)
	if err := s.UpdateAccounts(ctx, nil, func(l *accounts.Ledger) error {
		l.Sessions[accounts.TokenHash(guestToken)] = accounts.Session{Principal: g, CreatedAt: now, ExpiresAt: now.Add(accounts.Lifetime)}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.Delete(ctx, first.Token, deletionPassword, 1, now); err != nil {
		t.Fatal(err)
	}
	var privateRows int
	if err := s.pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM rhythm_accounts WHERE principal=$1) + (SELECT count(*) FROM rhythm_diaries WHERE diary_key=$2) + (SELECT count(*) FROM rhythm_sessions WHERE principal IN ($1,$2))`, first.Principal, g).Scan(&privateRows); err != nil || privateRows != 0 {
		t.Fatalf("private rows=%d err=%v", privateRows, err)
	}
	for _, token := range []string{first.Token, second.Token, guestToken} {
		if _, err := a.Resolve(ctx, token, now); !errors.Is(err, accounts.ErrUnauthorized) {
			t.Fatalf("session survived deletion: %v", err)
		}
	}
	for _, id := range []string{g, first.Principal} {
		if _, err := s.Read(ctx, id); !errors.Is(err, accounts.ErrUnauthorized) {
			t.Fatalf("deleted owner read: %v", err)
		}
		if _, err := s.Update(ctx, id, func(r rhythm.Record) (rhythm.Record, error) { r.Revision++; return r, nil }); !errors.Is(err, accounts.ErrUnauthorized) {
			t.Fatalf("deleted owner recreated diary: %v", err)
		}
	}
	if err := a.AllowLegacy(ctx, g, now.Add(2*accounts.Lifetime)); !errors.Is(err, accounts.ErrUnauthorized) {
		t.Fatalf("old guest resurrected after cookie expiry: %v", err)
	}
	if _, err := a.Register(ctx, g, 0, "resurrected", deletionPassword, now); !errors.Is(err, accounts.ErrUnauthorized) {
		t.Fatalf("old guest reclaimed identity: %v", err)
	}
	if _, err := a.Resolve(ctx, other.Token, now); err != nil {
		t.Fatalf("another account changed: %v", err)
	}
	if _, err := a.Login(ctx, "owner", deletionPassword, now); !errors.Is(err, accounts.ErrUnauthorized) {
		t.Fatalf("deleted credentials still work: %v", err)
	}
	if _, err := a.Register(ctx, guest("c"), 0, "owner", deletionPassword, now); err != nil {
		t.Fatalf("deleted login not reusable: %v", err)
	}
}

func TestAccountDeletionRejectsWrongPasswordAndStaleRevision(t *testing.T) {
	s, _ := testDatabase(t, true)
	a := service(t, s)
	ctx := context.Background()
	now := time.Now().UTC()
	grant, err := a.Register(ctx, guest("a"), 0, "owner", deletionPassword, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, password string
		revision       uint64
		want           error
	}{
		{"password", "wrong password phrase", 0, accounts.ErrUnauthorized},
		{"revision", deletionPassword, 1, rhythm.ErrConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := a.Delete(ctx, grant.Token, tc.password, tc.revision, now); !errors.Is(err, tc.want) {
				t.Fatalf("error=%v want=%v", err, tc.want)
			}
			if _, err := a.Resolve(ctx, grant.Token, now); err != nil {
				t.Fatalf("failed deletion revoked session: %v", err)
			}
			if _, err := s.Read(ctx, grant.Principal); err != nil {
				t.Fatalf("failed deletion removed diary: %v", err)
			}
		})
	}
}

func TestAccountDeletionRollsBackEveryWriteOnDiaryDeleteFailure(t *testing.T) {
	s, _ := testDatabase(t, true)
	a := service(t, s)
	ctx := context.Background()
	now := time.Now().UTC()
	grant, err := a.Register(ctx, guest("a"), 0, "owner", deletionPassword, now)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, s, `CREATE FUNCTION refuse_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic delete failure'; END $$`)
	mustExec(t, s, `CREATE TRIGGER refuse_delete BEFORE DELETE ON rhythm_diaries FOR EACH ROW EXECUTE FUNCTION refuse_delete()`)
	if err := a.Delete(ctx, grant.Token, deletionPassword, 0, now); !errors.Is(err, accounts.ErrUnavailable) {
		t.Fatalf("delete error=%v", err)
	}
	if _, err := a.Resolve(ctx, grant.Token, now); err != nil {
		t.Fatalf("rollback lost session: %v", err)
	}
	if _, err := a.Login(ctx, "owner", deletionPassword, now); err != nil {
		t.Fatalf("rollback lost credentials: %v", err)
	}
	if _, err := s.Read(ctx, grant.Principal); err != nil {
		t.Fatalf("rollback lost diary: %v", err)
	}
}

// Signal after transactional authorization, before waiting for the diary lock.
type observedDeletion struct {
	*Store
	authorized chan struct{}
}

func (s *observedDeletion) DeleteAccount(ctx context.Context, d accounts.Deletion, authorize func(accounts.Ledger) error) error {
	return s.Store.DeleteAccount(ctx, d, func(l accounts.Ledger) error {
		if err := authorize(l); err != nil {
			return err
		}
		close(s.authorized)
		return nil
	})
}

func TestAccountDeletionWaitsForInFlightWriteAndRechecksRevision(t *testing.T) {
	s, _ := testDatabase(t, true)
	a := service(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	now := time.Now().UTC()
	grant, err := a.Register(ctx, guest("a"), 0, "owner", deletionPassword, now)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	written := make(chan error, 1)
	go func() {
		_, err := s.Update(ctx, grant.Principal, func(r rhythm.Record) (rhythm.Record, error) {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return r, ctx.Err()
			}
			r.Revision++
			return r, nil
		})
		written <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	repo := &observedDeletion{Store: s, authorized: make(chan struct{})}
	deleting, err := accounts.New(repo)
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	deleted := make(chan error, 1)
	go func() { deleted <- deleting.Delete(ctx, grant.Token, deletionPassword, 0, now) }()
	select {
	case <-repo.authorized:
	case <-ctx.Done():
		close(release)
		t.Fatal(ctx.Err())
	}
	close(release)
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	if err := <-deleted; !errors.Is(err, rhythm.ErrConflict) {
		t.Fatalf("deletion ignored concurrent revision: %v", err)
	}
	if err := a.Delete(ctx, grant.Token, deletionPassword, 1, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update(ctx, grant.Principal, func(r rhythm.Record) (rhythm.Record, error) { r.Revision++; return r, nil }); !errors.Is(err, accounts.ErrUnauthorized) {
		t.Fatalf("stale admitted owner wrote after deletion: %v", err)
	}
}

func TestAccountDeletionRejectsGuestPausedBeforeDiaryInsert(t *testing.T) {
	s, _ := testDatabase(t, true)
	a := service(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	now := time.Now().UTC()
	g := guest("a")
	if err := writeProfile(s, g, 0); err != nil {
		t.Fatal(err)
	}
	const gate int64 = 72687955
	mustExec(t, s, `CREATE FUNCTION pause_guest_insert() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN
		IF current_setting('rhythm.pause_guest_insert', true) = 'on' THEN
			PERFORM pg_advisory_xact_lock(72687955);
		END IF;
		RETURN NEW;
	END $$`)
	mustExec(t, s, `CREATE TRIGGER pause_guest_insert BEFORE INSERT ON rhythm_diaries FOR EACH ROW EXECUTE FUNCTION pause_guest_insert()`)
	barrier, err := s.pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer barrier.Release()
	if _, err := barrier.Exec(ctx, "SELECT pg_advisory_lock($1)", gate); err != nil {
		t.Fatal(err)
	}
	barrierLocked := true
	defer func() {
		if !barrierLocked {
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if _, err := barrier.Exec(cleanup, "SELECT pg_advisory_unlock($1)", gate); err != nil {
			t.Error(err)
		}
	}()
	written := make(chan error, 1)
	go func() {
		written <- s.transaction(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, "SET LOCAL rhythm.pause_guest_insert='on'; SET LOCAL lock_timeout='15s'; SET LOCAL statement_timeout='20s'"); err != nil {
				return err
			}
			_, err := lockedRecord(ctx, tx, g)
			return err
		})
	}()
	// The BEFORE INSERT barrier is after the initial tombstone check and
	// before the old guest has locked or inserted its diary row.
	for {
		var waiting bool
		if err := s.pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_locks WHERE locktype='advisory' AND objid=$1::oid AND NOT granted)", gate).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case <-time.After(10 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	grant, err := a.Register(ctx, g, 1, "owner", deletionPassword, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Delete(ctx, grant.Token, deletionPassword, 1, now); err != nil {
		t.Fatal(err)
	}
	if _, err := barrier.Exec(ctx, "SELECT pg_advisory_unlock($1)", gate); err != nil {
		t.Fatal(err)
	}
	barrierLocked = false
	select {
	case err := <-written:
		if !errors.Is(err, accounts.ErrUnauthorized) {
			t.Fatalf("paused old guest recreated deleted diary: %v", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	var count int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM rhythm_diaries WHERE diary_key=$1", g).Scan(&count); err != nil || count != 0 {
		t.Fatalf("resurrected diary rows=%d err=%v", count, err)
	}
}
