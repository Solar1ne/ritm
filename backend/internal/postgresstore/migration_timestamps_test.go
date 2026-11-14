package postgresstore

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type migrationLedgerRow struct {
	ID, Version int64
	Applied     bool
	At          time.Time
}

func migrationLedger(t *testing.T, s *Store) []migrationLedgerRow {
	t.Helper()
	rows, err := s.pool.Query(context.Background(), "SELECT id,version_id,is_applied,tstamp FROM goose_db_version ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result []migrationLedgerRow
	for rows.Next() {
		var row migrationLedgerRow
		if err := rows.Scan(&row.ID, &row.Version, &row.Applied, &row.At); err != nil {
			t.Fatal(err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func previousNumericGooseFixture(t *testing.T, head int64) *Store {
	t.Helper()
	s, _ := testDatabase(t, false)
	if err := s.MigrateTo(context.Background(), head); err != nil {
		t.Fatal(err)
	}
	if err := writeProfile(s, guest("a"), 0); err != nil {
		t.Fatal(err)
	}
	// The former Goose release has exactly the same SQL schema and row fields;
	// only its stored version IDs differ. Keep IDs and timestamps untouched.
	mustExec(t, s, "UPDATE goose_db_version SET version_id=CASE version_id WHEN $1::bigint THEN 1 WHEN $2::bigint THEN 2 ELSE version_id END", int64(initialVersion), int64(schemaVersion))
	return s
}

func TestPreviousNumericGooseHistoryNormalizesWithoutReplayingDDL(t *testing.T) {
	for _, head := range []int64{1, 2} {
		t.Run(fmt.Sprint(head), func(t *testing.T) {
			s := previousNumericGooseFixture(t, head)
			ctx := context.Background()
			before := migrationLedger(t, s)
			data := businessRows(t, s)
			shape, err := catalogFingerprint(ctx, s.pool, "rhythm_")
			if err != nil {
				t.Fatal(err)
			}
			canonical := migrationVersions()[head-1]
			status, err := s.Status(ctx)
			if err != nil || !status.NeedsVersionNormalization || status.CurrentVersion != canonical || status.TargetVersion != schemaVersion || status.Legacy {
				t.Fatalf("numeric status: %+v %v", status, err)
			}
			if err := s.Check(ctx); head == 2 && err != nil || head == 1 && !errors.Is(err, ErrSchema) {
				t.Fatalf("readiness changed for known old ledger: %v", err)
			}
			if !reflect.DeepEqual(before, migrationLedger(t, s)) {
				t.Fatal("read-only status/startup rewrote numeric versions")
			}
			if err := s.MigrateTo(ctx, canonical); err != nil {
				t.Fatal(err)
			}
			after := migrationLedger(t, s)
			want := append([]migrationLedgerRow(nil), before...)
			for i := range want {
				if want[i].Version > 0 {
					want[i].Version = migrationVersions()[want[i].Version-1]
				}
			}
			if !reflect.DeepEqual(want, after) {
				t.Fatal("normalization changed IDs, timestamps, row order or applied flags")
			}
			current, err := s.Status(ctx)
			if err != nil || current.NeedsVersionNormalization || current.CurrentVersion != canonical {
				t.Fatalf("normalized status: %+v %v", current, err)
			}
			updatedShape, err := catalogFingerprint(ctx, s.pool, "rhythm_")
			if err != nil || shape != updatedShape || !reflect.DeepEqual(data, businessRows(t, s)) {
				t.Fatal("normalization replayed DDL or changed user/legacy data")
			}
			if err := s.MigrateTo(ctx, head); err != nil {
				t.Fatalf("numeric CLI alias stopped working: %v", err)
			}
			if !reflect.DeepEqual(after, migrationLedger(t, s)) {
				t.Fatal("repeat normalization rewrote version records")
			}
			assertLegacyReceipt(t, s)
			if head == 1 {
				if err := s.Migrate(ctx); err != nil {
					t.Fatal(err)
				}
				latest := migrationLedger(t, s)
				if len(latest) != 3 || latest[2].Version != schemaVersion || !reflect.DeepEqual(after, latest[:2]) || !reflect.DeepEqual(data, businessRows(t, s)) {
					t.Fatal("numeric-v1 upgrade replayed initial migration or lost data")
				}
			}
		})
	}
}

func TestTimestampMigrationRejectsMixedHistoriesWithoutRekey(t *testing.T) {
	for _, change := range []string{
		fmt.Sprintf("UPDATE goose_db_version SET version_id=1 WHERE version_id=%d", initialVersion),
		fmt.Sprintf("UPDATE goose_db_version SET version_id=2 WHERE version_id=%d", schemaVersion),
		"UPDATE goose_db_version SET version_id=2 WHERE version_id>0",
		"INSERT INTO goose_db_version(version_id,is_applied) VALUES(1,true)",
	} {
		s, _ := testDatabase(t, true)
		mustExec(t, s, change)
		before := migrationLedger(t, s)
		if err := s.Migrate(context.Background()); !errors.Is(err, ErrSchema) {
			t.Fatalf("mixed/unknown history accepted: %v", err)
		}
		if _, err := s.Status(context.Background()); !errors.Is(err, ErrSchema) {
			t.Fatalf("mixed/unknown status accepted: %v", err)
		}
		if !reflect.DeepEqual(before, migrationLedger(t, s)) {
			t.Fatal("invalid history was partially normalized")
		}
	}
}

type normalizationQueryKey struct{}

type normalizationCancelTracer struct {
	cancel  context.CancelFunc
	updated *atomic.Bool
}

func (normalizationCancelTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.HasPrefix(data.SQL, "UPDATE goose_db_version SET version_id=CASE") {
		return context.WithValue(ctx, normalizationQueryKey{}, true)
	}
	return ctx
}

func (t normalizationCancelTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if ctx.Value(normalizationQueryKey{}) == true && data.Err == nil {
		t.updated.Store(true)
		t.cancel()
	}
}

func TestNumericVersionNormalizationRollsBackAfterUpdateCancellation(t *testing.T) {
	s := previousNumericGooseFixture(t, 2)
	before := migrationLedger(t, s)
	data := businessRows(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updated := &atomic.Bool{}
	config := s.pool.Config()
	config.ConnConfig.Tracer = normalizationCancelTracer{cancel: cancel, updated: updated}
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	injected := &Store{pool: pool}
	if err := injected.Migrate(ctx); !errors.Is(err, context.Canceled) || !updated.Load() {
		t.Fatalf("expected cancellation after version UPDATE: %v", err)
	}
	if !reflect.DeepEqual(before, migrationLedger(t, s)) || !reflect.DeepEqual(data, businessRows(t, s)) {
		t.Fatal("failed normalization partially changed versions or data")
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("normalization retry after rollback: %v", err)
	}
}

func TestNumericNormalizationPermitsActiveApplicationWrite(t *testing.T) {
	s := previousNumericGooseFixture(t, 2)
	tx, err := s.pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := tx.Rollback(context.Background()); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			t.Error(err)
		}
	}()
	if _, err := tx.Exec(context.Background(), "LOCK TABLE rhythm_diaries IN ROW EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("normalization blocked an ordinary active write: %v", err)
	}
	if err := s.checkSchema(ctx, tx); err != nil {
		t.Fatalf("application readiness read blocked: %v", err)
	}
}

func TestConcurrentNumericVersionNormalizationIsIdempotent(t *testing.T) {
	s := previousNumericGooseFixture(t, 2)
	before := migrationLedger(t, s)
	var workers sync.WaitGroup
	errs := make(chan error, 3)
	for range 3 {
		workers.Go(func() { errs <- s.Migrate(context.Background()) })
	}
	workers.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	after := migrationLedger(t, s)
	if len(after) != len(before) {
		t.Fatal("concurrent normalization appended duplicate migrations")
	}
	for i := range after {
		if after[i].ID != before[i].ID || !after[i].At.Equal(before[i].At) {
			t.Fatal("concurrent normalization rewrote version metadata")
		}
	}
}
