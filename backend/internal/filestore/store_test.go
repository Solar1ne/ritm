package filestore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"rhythm/internal/rhythm"
)

func principal(n string) string { return "g_" + strings.Repeat(n, 64) }
func profile() rhythm.Profile {
	return rhythm.Profile{Goal: "tone", Experience: "beginner", Days: []int{0, 2, 4}, Minutes: 45}
}

func TestPersistRestartAndIsolation(t *testing.T) {
	dir := t.TempDir()
	store, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	got, err := store.Read(ctx, principal("a"))
	if err != nil || got.Profile != nil || got.Revision != 0 {
		t.Fatalf("blank read: %+v %v", got, err)
	}
	got, err = store.Update(ctx, principal("a"), func(r rhythm.Record) (rhythm.Record, error) { return rhythm.SetProfile(r, 0, profile()) })
	if err != nil {
		t.Fatal(err)
	}
	got.Profile.Days[0] = 6
	reopened, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err = reopened.Read(ctx, principal("a"))
	if err != nil || got.Profile == nil || got.Profile.Days[0] != 0 || got.Revision != 1 {
		t.Fatalf("restart: %+v %v", got, err)
	}
	other, err := store.Read(ctx, principal("b"))
	if err != nil || other.Profile != nil {
		t.Fatalf("identity leak: %+v %v", other, err)
	}
	for _, p := range []struct {
		path string
		mode os.FileMode
	}{{dir, 0700}, {filepath.Join(dir, principal("a")+".json"), 0600}} {
		info, err := os.Stat(p.path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != p.mode {
			t.Fatalf("permissions %s: %o", p.path, info.Mode().Perm())
		}
	}
}

func TestConcurrentRevisionOneWinner(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 24)
	for i := 0; i < 24; i++ {
		wg.Go(func() {
			_, err := s.Update(context.Background(), principal("a"), func(r rhythm.Record) (rhythm.Record, error) { return rhythm.SetProfile(r, 0, profile()) })
			results <- err
		})
	}
	wg.Wait()
	close(results)
	succeeded, conflicts := 0, 0
	for err := range results {
		if err == nil {
			succeeded++
		} else if errors.Is(err, rhythm.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if succeeded != 1 || conflicts != 23 {
		t.Fatalf("updates=%d conflicts=%d", succeeded, conflicts)
	}
	r, err := s.Read(context.Background(), principal("a"))
	if err != nil || r.Revision != 1 {
		t.Fatalf("read after race: %+v %v", r, err)
	}
}

func TestCancellationWhileWaitingDoesNotReleaseOwner(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		_, err := s.Update(context.Background(), principal("a"), func(r rhythm.Record) (rhythm.Record, error) {
			close(entered)
			<-release
			return rhythm.SetProfile(r, 0, profile())
		})
		finished <- err
	}()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Read(ctx, principal("a")); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled: %v", err)
	}
	if len(s.lock) != 1 {
		t.Fatal("canceled waiter released owner's lock")
	}
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read(context.Background(), principal("a")); err != nil {
		t.Fatal(err)
	}
}

func TestFailedCommitDoesNotAcknowledgeOrCache(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(s.dir, principal("a")+".json")
	_, err = s.Update(context.Background(), principal("a"), func(r rhythm.Record) (rhythm.Record, error) {
		if err := os.Mkdir(path, 0700); err != nil {
			return rhythm.Record{}, err
		}
		return rhythm.SetProfile(r, 0, profile())
	})
	if err == nil {
		t.Fatal("failed rename reported success")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	r, err := s.Read(context.Background(), principal("a"))
	if err != nil || r.Profile != nil || r.Revision != 0 {
		t.Fatalf("failed write cached: %+v %v", r, err)
	}
	files, err := filepath.Glob(filepath.Join(s.dir, ".record-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("temporary files leaked: %v", files)
	}
}

func TestCorruptionUnavailableAndTraversal(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read(context.Background(), "../other"); err == nil {
		t.Fatal("accepted traversal")
	}
	path := filepath.Join(dir, principal("a")+".json")
	if err := os.WriteFile(path, []byte(`{"profile":{"goal":"invalid"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = s.Read(context.Background(), principal("a"))
	if err == nil {
		t.Fatal("corruption accepted")
	}
	var validation *rhythm.ValidationError
	if errors.As(err, &validation) {
		t.Fatal("storage corruption leaked as client validation")
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read(context.Background(), principal("a")); err == nil {
		t.Fatal("missing data directory treated as empty profile")
	}
}

func TestSecretSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := s.SessionSecret(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	other, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	again, err := other.SessionSecret(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(secret) != 32 || !bytes.Equal(secret, again) {
		t.Fatal("session key changed after restart")
	}
	if _, err := other.SessionSecret(context.Background(), "short"); err == nil {
		t.Fatal("short key allowed")
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if _, err := other.SessionSecret(ctx, ""); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("canceled secret read: %v", err)
	}
}

func TestOversizedWriteCannotPoisonReadableRecord(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, err = s.Update(ctx, principal("a"), func(r rhythm.Record) (rhythm.Record, error) { return rhythm.SetProfile(r, 0, profile()) })
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Update(ctx, principal("a"), func(r rhythm.Record) (rhythm.Record, error) {
		r = rhythm.Clone(r)
		r.Profile.Name = strings.Repeat("x", MaxRecordBytes)
		r.Revision++
		return r, nil
	})
	if !errors.Is(err, rhythm.ErrCapacity) {
		t.Fatalf("oversized write: %v", err)
	}
	r, err := s.Read(ctx, principal("a"))
	if err != nil || r.Revision != 1 || r.Profile.Name != "" {
		t.Fatalf("failed oversized write poisoned record: revision=%d err=%v", r.Revision, err)
	}
}

func TestLegacyMigrationAndRolloverPersistOnce(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := profile()
	legacy := rhythm.Record{Revision: 7, Profile: &p, Entries: []rhythm.Entry{{ID: "old", Date: "2026-10-09", SessionID: "2026-10-09", Status: "done"}}}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, principal("a")+".json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	r, err := s.Update(context.Background(), principal("a"), func(r rhythm.Record) (rhythm.Record, error) { return rhythm.EnsureWeeks(r, now) })
	if err != nil {
		t.Fatal(err)
	}
	if r.Revision != 8 || r.Version != rhythm.RecordVersion || len(r.Weeks) != 2 {
		t.Fatalf("migration not persisted: %+v", r)
	}
	restarted, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	r, err = restarted.Read(context.Background(), principal("a"))
	if err != nil || r.Weeks[0].Plan.Sessions[0].DetailsKnown {
		t.Fatal("restart lost unknown legacy details")
	}
	var wg sync.WaitGroup
	failures := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Go(func() {
			_, err := restarted.Update(context.Background(), principal("a"), func(r rhythm.Record) (rhythm.Record, error) { return rhythm.EnsureWeeks(r, now.AddDate(0, 0, 2)) })
			failures <- err
		})
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	r, err = restarted.Read(context.Background(), principal("a"))
	if err != nil || r.Revision != 9 || len(r.Weeks) != 3 {
		t.Fatalf("rollover was committed more than once: revision=%d weeks=%d err=%v", r.Revision, len(r.Weeks), err)
	}
}
