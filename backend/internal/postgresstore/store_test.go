package postgresstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"rhythm/internal/accounts"
	"rhythm/internal/filestore"
	"rhythm/internal/rhythm"
)

// Every test owns a random schema in an explicitly named test database.
// Never point this test at a production database.
func testDatabase(t *testing.T, migrate bool) (*Store, string) {
	t.Helper()
	raw := os.Getenv("RHYTHM_TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("RHYTHM_TEST_DATABASE_URL is required for PostgreSQL integration tests")
	}
	u, err := url.Parse(raw)
	if err != nil || !strings.HasSuffix(u.Path, "_test") {
		t.Fatal("test database name must end in _test")
	}
	ctx := context.Background()
	admin, err := Open(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("test_%d", time.Now().UnixNano())
	ident := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.pool.Exec(ctx, "CREATE SCHEMA "+ident); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	t.Cleanup(func() {
		defer admin.Close()
		if _, err := admin.pool.Exec(ctx, "DROP SCHEMA "+ident+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	s, err := Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if migrate {
		if err := s.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	return s, u.String()
}
func guest(n string) string { return "g_" + strings.Repeat(n, 64) }
func testProfile() rhythm.Profile {
	return rhythm.Profile{Goal: "tone", Experience: "beginner", Days: []int{0, 2, 4}, Minutes: 45}
}
func writeProfile(s *Store, id string, rev uint64) error {
	_, err := s.Update(context.Background(), id, func(r rhythm.Record) (rhythm.Record, error) { return rhythm.SetProfile(r, rev, testProfile()) })
	return err
}
func mustExec(t *testing.T, s *Store, sql string, args ...any) {
	t.Helper()
	if _, err := s.pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}
func service(t *testing.T, s *Store) *accounts.Service {
	t.Helper()
	a, err := accounts.New(s)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestMigrationsConcurrentAndDrift(t *testing.T) {
	s, raw := testDatabase(t, false)
	ctx := context.Background()
	other, err := Open(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, db := range []*Store{s, other} {
		wg.Go(func() { errs <- db.Migrate(ctx) })
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Check(ctx); err != nil {
		t.Fatal(err)
	}
	mustExec(t, s, "UPDATE rhythm_schema_migrations SET checksum='drift'")
	if err := s.Migrate(ctx); !errors.Is(err, ErrSchema) {
		t.Fatalf("checksum accepted: %v", err)
	}
	mustExec(t, s, "UPDATE rhythm_schema_migrations SET version=99")
	if err := s.Check(ctx); !errors.Is(err, ErrSchema) {
		t.Fatalf("unknown version accepted: %v", err)
	}
}
func TestMigrationDDLFailureRollsBack(t *testing.T) {
	s, _ := testDatabase(t, false)
	mustExec(t, s, "CREATE TABLE rhythm_accounts (fixture integer)")
	if err := s.Migrate(context.Background()); err == nil {
		t.Fatal("expected DDL collision")
	}
	var absent bool
	if err := s.pool.QueryRow(context.Background(), "SELECT to_regclass('rhythm_identities') IS NULL AND to_regclass('rhythm_schema_migrations') IS NULL").Scan(&absent); err != nil || !absent {
		t.Fatalf("partial DDL: %v %v", absent, err)
	}
}
func TestPersistenceIsolationConcurrentRevisionAndRollback(t *testing.T) {
	s, raw := testDatabase(t, true)
	ctx := context.Background()
	other, err := Open(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		db := s
		if i%2 == 1 {
			db = other
		}
		wg.Go(func() { errs <- writeProfile(db, guest("a"), 0) })
	}
	wg.Wait()
	close(errs)
	wins, conflicts := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, rhythm.ErrConflict):
			conflicts++
		default:
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 19 {
		t.Fatalf("wins=%d conflicts=%d", wins, conflicts)
	}
	got, err := other.Read(ctx, guest("a"))
	if err != nil || got.Revision != 1 || got.Profile == nil {
		t.Fatalf("persist: %+v %v", got, err)
	}
	got.Profile.Days[0] = 6
	got, err = s.Read(ctx, guest("a"))
	if err != nil || got.Profile.Days[0] != 0 {
		t.Fatal("mutable data leaked")
	}
	blank, err := s.Read(ctx, guest("b"))
	if err != nil || blank.Profile != nil {
		t.Fatal("identity isolation failed")
	}
	sentinel := errors.New("synthetic callback failure")
	_, err = s.Update(ctx, guest("a"), func(r rhythm.Record) (rhythm.Record, error) { r.Revision++; return r, sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	got, err = s.Read(ctx, guest("a"))
	if err != nil || got.Revision != 1 {
		t.Fatal("rollback failed")
	}
	_, err = s.Update(ctx, guest("a"), func(r rhythm.Record) (rhythm.Record, error) { r.Revision = math.MaxUint64; return r, nil })
	if !errors.Is(err, rhythm.ErrCapacity) {
		t.Fatal(err)
	}
	_, err = s.Update(ctx, guest("a"), func(r rhythm.Record) (rhythm.Record, error) { r.Revision = 9007199254740993; return r, nil })
	if err != nil {
		t.Fatal(err)
	}
	got, err = s.Read(ctx, guest("a"))
	if err != nil || got.Revision != 9007199254740993 {
		t.Fatal("JSONB lost integer precision")
	}
}
func TestCancellationFreesTransaction(t *testing.T) {
	s, _ := testDatabase(t, true)
	ctx := context.Background()
	if err := writeProfile(s, guest("a"), 0); err != nil {
		t.Fatal(err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT 1 FROM rhythm_diaries WHERE diary_key=$1 FOR UPDATE", guest("a")); err != nil {
		t.Fatal(err)
	}
	deadline, cancel := context.WithTimeout(ctx, 80*time.Millisecond)
	defer cancel()
	_, err = s.Update(deadline, guest("a"), func(r rhythm.Record) (rhythm.Record, error) { return rhythm.SetProfile(r, 1, testProfile()) })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := writeProfile(s, guest("a"), 1); err != nil {
		t.Fatal(err)
	}
	// pgx destroys connections interrupted during I/O asynchronously.
	deadlineAt := time.Now().Add(2 * time.Second)
	for s.pool.Stat().AcquiredConns() != 0 && time.Now().Before(deadlineAt) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := s.pool.Stat().AcquiredConns(); n != 0 {
		t.Fatalf("leaked %d connections", n)
	}
}
func TestAccountClaimRecoveryAndScopedReads(t *testing.T) {
	s, raw := testDatabase(t, true)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	a := service(t, s)
	if err := writeProfile(s, guest("a"), 0); err != nil {
		t.Fatal(err)
	}
	grant, err := a.Register(ctx, guest("a"), 1, "alice", "a long synthetic password", now)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []func() error{func() error { _, e := s.Read(ctx, guest("a")); return e }, func() error { return writeProfile(s, guest("a"), 1) }} {
		if e := op(); !errors.Is(e, accounts.ErrUnauthorized) {
			t.Fatalf("old guest: %v", e)
		}
	}
	if _, err := s.pool.Exec(ctx, "DELETE FROM rhythm_diaries WHERE diary_key=$1", guest("a")); err == nil {
		t.Fatal("account diary deletion accepted")
	}
	other, err := Open(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	a2 := service(t, other)
	login, err := a2.Login(ctx, "alice", "a long synthetic password", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.LogoutOthers(ctx, grant.Token, now); err != nil {
		t.Fatal(err)
	}
	if _, err := a2.Resolve(ctx, login.Token, now); !errors.Is(err, accounts.ErrUnauthorized) {
		t.Fatal(err)
	}
	scoped, err := s.ReadAccounts(ctx, accounts.Lookup{Login: "alice"})
	if err != nil || len(scoped.Accounts) != 1 || len(scoped.Sessions) != 1 {
		t.Fatalf("scoped lookup: %v", err)
	}
	unknown, err := s.ReadAccounts(ctx, accounts.Lookup{Login: "unknown"})
	if err != nil || len(unknown.Accounts) != 0 || len(unknown.Sessions) != 0 {
		t.Fatal("lookup leak")
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, svc := range []*accounts.Service{a, a2} {
		wg.Go(func() {
			_, e := svc.Recover(ctx, "alice", grant.RecoveryCode, "another long synthetic password", now)
			errs <- e
		})
	}
	wg.Wait()
	close(errs)
	wins := 0
	for e := range errs {
		if e == nil {
			wins++
		} else if !errors.Is(e, accounts.ErrUnauthorized) {
			t.Fatal(e)
		}
	}
	if wins != 1 {
		t.Fatalf("recovery used %d times", wins)
	}
	if _, err := a.Resolve(ctx, grant.Token, now); !errors.Is(err, accounts.ErrUnauthorized) {
		t.Fatal("recovery did not revoke session")
	}
	if _, err := a.Login(ctx, "alice", "another long synthetic password", now); err != nil {
		t.Fatal(err)
	}
}
func TestAccountFailureRollsBackClaim(t *testing.T) {
	s, _ := testDatabase(t, true)
	ctx := context.Background()
	a := service(t, s)
	mustExec(t, s, `CREATE FUNCTION reject_session() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic private detail'; END $$; CREATE TRIGGER reject_session BEFORE INSERT ON rhythm_sessions FOR EACH ROW EXECUTE FUNCTION reject_session()`)
	_, err := a.Register(ctx, guest("a"), 0, "alice", "a long synthetic password", time.Now())
	if !errors.Is(err, accounts.ErrUnavailable) || strings.Contains(err.Error(), "private") {
		t.Fatalf("fault: %v", err)
	}
	ledger, err := s.ReadAccounts(ctx, accounts.Lookup{})
	if err != nil || len(ledger.Accounts) != 0 {
		t.Fatal("partial account saved")
	}
	if err := writeProfile(s, guest("a"), 0); err != nil {
		t.Fatal("claim was not rolled back", err)
	}
}
func TestClaimAndDiaryWriteOrder(t *testing.T) {
	s, _ := testDatabase(t, true)
	ctx := context.Background()
	if err := writeProfile(s, guest("a"), 0); err != nil {
		t.Fatal(err)
	}
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		_, e := s.Update(ctx, guest("a"), func(r rhythm.Record) (rhythm.Record, error) {
			close(entered)
			<-release
			return rhythm.SetProfile(r, 1, testProfile())
		})
		done <- e
	}()
	<-entered
	claimDone := make(chan error, 1)
	go func() {
		claimDone <- s.UpdateAccounts(ctx, &accounts.Claim{Guest: guest("a"), Revision: 1}, func(*accounts.Ledger) error { t.Error("stale claim reached callback"); return nil })
	}()
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-claimDone; !errors.Is(err, accounts.ErrConflict) {
		t.Fatal(err)
	}
	// Opposite order: ownership changes before a waiting old guest can write.
	a := service(t, s)
	fixture, err := a.Register(ctx, guest("b"), 0, "fixture", "a long synthetic password", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := s.ReadAccounts(ctx, accounts.Lookup{Principal: fixture.Principal})
	if err != nil {
		t.Fatal(err)
	}
	account := ledger.Accounts[fixture.Principal]
	account.Login = "alice"
	entered, release = make(chan struct{}), make(chan struct{})
	go func() {
		done <- s.UpdateAccounts(ctx, &accounts.Claim{Guest: guest("a"), Revision: 2}, func(l *accounts.Ledger) error {
			close(entered)
			<-release
			l.Accounts["u_"+guest("a")[2:]] = account
			return nil
		})
	}()
	<-entered
	writeDone := make(chan error, 1)
	go func() { writeDone <- writeProfile(s, guest("a"), 2) }()
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-writeDone; !errors.Is(err, accounts.ErrUnauthorized) {
		t.Fatal(err)
	}
}
func TestSessionKeyInitializationAndMissingKey(t *testing.T) {
	s, raw := testDatabase(t, true)
	ctx := context.Background()
	other, err := Open(ctx, raw)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	keys := make(chan []byte, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, db := range []*Store{s, other} {
		wg.Go(func() { key, e := db.SessionSecret(ctx, ""); keys <- key; errs <- e })
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	if !bytes.Equal(<-keys, <-keys) {
		t.Fatal("concurrent key generation diverged")
	}
	if _, err := s.SessionSecret(ctx, strings.Repeat("x", 32)); err == nil {
		t.Fatal("key rotation accepted")
	}
	if err := writeProfile(s, guest("a"), 0); err != nil {
		t.Fatal(err)
	}
	mustExec(t, s, "DELETE FROM rhythm_metadata")
	if _, err := s.SessionSecret(ctx, ""); err == nil {
		t.Fatal("missing identity key regenerated")
	}
}
func TestOfflineImportPreservesAccountAndKey(t *testing.T) {
	s, _ := testDatabase(t, true)
	ctx := context.Background()
	dir := t.TempDir()
	file, err := filestore.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	key, err := file.SessionSecret(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Update(ctx, guest("a"), func(r rhythm.Record) (rhythm.Record, error) { return rhythm.SetProfile(r, 0, testProfile()) }); err != nil {
		t.Fatal(err)
	}
	a, err := accounts.New(file)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	grant, err := a.Register(ctx, guest("a"), 1, "alice", "a long synthetic password", now)
	if err != nil {
		t.Fatal(err)
	}
	var data ImportData
	err = filestore.WithSnapshot(ctx, dir, "", func(snap filestore.Snapshot) error {
		data = ImportData{Records: snap.Records, Accounts: snap.Accounts, Secret: snap.Secret, Fingerprint: snap.Fingerprint}
		if err := s.PreflightImport(ctx, data, now); err != nil {
			return err
		}
		if err := filestore.MarkCutover(ctx, dir, data.Fingerprint, "pending"); err != nil {
			return err
		}
		if _, err := s.Import(ctx, data, now); err != nil {
			return err
		}
		return filestore.MarkCutover(ctx, dir, data.Fingerprint, "completed")
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := filestore.New(dir); !errors.Is(err, filestore.ErrCutover) {
		t.Fatal("file fallback enabled", err)
	}
	if _, err := file.Read(ctx, grant.Principal); !errors.Is(err, filestore.ErrCutover) {
		t.Fatal("old store bypassed marker", err)
	}
	imported, err := s.Read(ctx, grant.Principal)
	if err != nil || imported.Revision != 1 || imported.Profile == nil {
		t.Fatal("diary lost", err)
	}
	got, err := s.SessionSecret(ctx, "")
	if err != nil || !bytes.Equal(key, got) {
		t.Fatal("key changed", err)
	}
	pgAccount := service(t, s)
	if who, err := pgAccount.Resolve(ctx, grant.Token, now); err != nil || who != grant.Principal {
		t.Fatal("session lost", err)
	}
	if _, err := pgAccount.Login(ctx, "alice", "a long synthetic password", now); err != nil {
		t.Fatal("credentials lost", err)
	}
	result, err := s.Import(ctx, data, now)
	if err != nil || !result.AlreadyImported {
		t.Fatal("same snapshot retry", err)
	}
	data.Fingerprint = strings.Repeat("b", 64)
	if err := s.PreflightImport(ctx, data, now); err == nil {
		t.Fatal("nonempty target overwrite")
	}
}
func TestDatabaseErrorsRedactSecrets(t *testing.T) {
	for _, raw := range []string{
		"postgres://private:do-not-print@example.org/rhythm",
		"postgres://private:do-not-print@example.org/rhythm?sslmode=prefer",
		"postgres://private:do-not-print@example.org/rhythm?sslmode=require",
		"postgres://private:do-not-print@example.org/rhythm?sslmode=verify-ca",
		"host=localhost,example.org user=private password=do-not-print dbname=rhythm sslmode=disable",
	} {
		_, err := Open(context.Background(), raw)
		if err == nil || !strings.Contains(err.Error(), "verified TLS") || strings.Contains(err.Error(), "do-not-print") {
			t.Fatalf("remote endpoint did not fail before connection: %v", err)
		}
	}
	_, err := Open(context.Background(), "postgres://private:do-not-print@%wrong")
	if err == nil || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "do-not-print") {
		t.Fatal("DSN disclosure")
	}
	_, err = Open(context.Background(), "postgres://private:do-not-print@example.org/rhythm?sslmode=disable")
	if err == nil || strings.Contains(err.Error(), "do-not-print") {
		t.Fatal("remote plaintext accepted")
	}
}

type metadataInsertFailureTracer struct{ fail *atomic.Bool }

func (t metadataInsertFailureTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if t.fail.Load() && strings.HasPrefix(data.SQL, "INSERT INTO rhythm_metadata") {
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		return canceled
	}
	return ctx
}

func (metadataInsertFailureTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestImportPendingFailureAndCommittedRetry(t *testing.T) {
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
	if _, err := file.Update(ctx, guest("a"), func(r rhythm.Record) (rhythm.Record, error) { r.Revision++; return r, nil }); err != nil {
		t.Fatal(err)
	}
	// Inject a late SQL cancellation, after diary inserts, without changing the
	// canonical schema. A synthetic trigger would now correctly fail preflight's
	// structural compatibility check before the source receives a pending marker.
	fail := &atomic.Bool{}
	fail.Store(true)
	config := s.pool.Config()
	config.ConnConfig.Tracer = metadataInsertFailureTracer{fail: fail}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	s = &Store{pool: pool}
	var result ImportResult
	run := func() error {
		return filestore.WithSnapshot(ctx, dir, "", func(snapshot filestore.Snapshot) error {
			data := ImportData{Records: snapshot.Records, Accounts: snapshot.Accounts, Secret: snapshot.Secret, Fingerprint: snapshot.Fingerprint}
			if err := s.PreflightImport(ctx, data, time.Now()); err != nil {
				return err
			}
			if err := filestore.MarkCutover(ctx, dir, data.Fingerprint, "pending"); err != nil {
				return err
			}
			result, err = s.Import(ctx, data, time.Now())
			return err // Simulates loss of the completed-marker write.
		})
	}
	if err := run(); !errors.Is(err, accounts.ErrUnavailable) {
		t.Fatal("injected commit path failure missing", err)
	}
	var count int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM rhythm_identities").Scan(&count); err != nil || count != 0 {
		t.Fatal("partial import committed", err)
	}
	if _, err := filestore.New(dir); !errors.Is(err, filestore.ErrCutover) {
		t.Fatal("pending marker allowed fallback", err)
	}
	fail.Store(false)
	if err := run(); err != nil || result.AlreadyImported {
		t.Fatal("failed import retry", err)
	}
	if err := writeProfile(s, guest("a"), 1); err != nil {
		t.Fatal(err)
	}
	if err := run(); err != nil || !result.AlreadyImported {
		t.Fatal("committed/pending retry", err)
	}
	saved, err := s.Read(ctx, guest("a"))
	if err != nil || saved.Revision != 2 || saved.Profile == nil {
		t.Fatal("retry overwrote subsequent SQL data", err)
	}
}
func TestImportPreflightRejectsInvalidHistoryAndOversizeBeforeMarker(t *testing.T) {
	s, _ := testDatabase(t, true)
	ctx := context.Background()
	now := time.Now()
	data := ImportData{Records: map[string]rhythm.Record{guest("a"): rhythm.Blank()}, Accounts: accounts.EmptyLedger(), Secret: []byte(strings.Repeat("x", 32)), Fingerprint: strings.Repeat("a", 64)}
	if err := s.PreflightImport(ctx, data, now); err != nil {
		t.Fatal("valid blank rejected", err)
	}
	r := rhythm.Blank()
	r.Entries = []rhythm.Entry{{ID: "orphan"}}
	data.Records[guest("a")] = r
	if err := s.PreflightImport(ctx, data, now); err == nil {
		t.Fatal("history without profile accepted")
	}
	// Exercise the representation boundary even for a legacy receipt journal.
	r = rhythm.Blank()
	r.Receipts = []rhythm.Entry{{ID: ""}}
	body, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	r.Receipts[0].ID = strings.Repeat("a", maxRecordBytes-len(body)-1)
	data.Records[guest("a")] = r
	if err := s.PreflightImport(ctx, data, now); !errors.Is(err, rhythm.ErrCapacity) {
		t.Fatalf("canonical JSONB boundary: %v", err)
	}
	var count int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM rhythm_identities").Scan(&count); err != nil || count != 0 {
		t.Fatal("preflight mutated database")
	}
}

func TestUnexpectedPanicReleasesTransaction(t *testing.T) {
	s, _ := testDatabase(t, true)
	ctx := context.Background()
	func() {
		defer func() {
			if recover() == nil {
				t.Error("expected injected panic")
			}
		}()
		_, _ = s.Update(ctx, guest("a"), func(r rhythm.Record) (rhythm.Record, error) { panic("synthetic programming bug") })
	}()
	if err := writeProfile(s, guest("a"), 0); err != nil {
		t.Fatal("panic retained diary lock", err)
	}
	if n := s.pool.Stat().AcquiredConns(); n != 0 {
		t.Fatalf("panic leaked %d connections", n)
	}
}
