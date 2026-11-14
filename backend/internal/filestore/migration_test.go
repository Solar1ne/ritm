package filestore

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"rhythm/internal/rhythm"
)

func TestSnapshotLockCancellationAndCutover(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	key, err := s.SessionSecret(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	var fingerprint string
	err = WithSnapshot(ctx, dir, "", func(snapshot Snapshot) error {
		if !bytes.Equal(snapshot.Secret, key) {
			t.Fatal("secret lost")
		}
		fingerprint = snapshot.Fingerprint
		limited, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
		defer cancel()
		if _, err := s.Read(limited, principal("a")); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("snapshot failed to exclude reader: %v", err)
		}
		return MarkCutover(ctx, dir, fingerprint, "pending")
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(dir); !errors.Is(err, ErrCutover) {
		t.Fatal(err)
	}
	if _, err := s.Update(ctx, principal("a"), func(r rhythm.Record) (rhythm.Record, error) { t.Error("write passed pending marker"); return r, nil }); !errors.Is(err, ErrCutover) {
		t.Fatal(err)
	}
	if err := WithSnapshot(ctx, dir, "", func(snapshot Snapshot) error {
		if snapshot.Fingerprint != fingerprint {
			t.Fatal("retry fingerprint changed")
		}
		return MarkCutover(ctx, dir, fingerprint, "completed")
	}); err != nil {
		t.Fatal(err)
	}
	if err := MarkCutover(ctx, dir, strings.Repeat("b", 64), "completed"); err == nil {
		t.Fatal("different source accepted")
	}
}
func TestSnapshotRequiresOriginalKey(t *testing.T) {
	dir := t.TempDir()
	if _, err := New(dir); err != nil {
		t.Fatal(err)
	}
	if err := WithSnapshot(context.Background(), dir, "", func(Snapshot) error { t.Error("missing key accepted"); return nil }); err == nil {
		t.Fatal("key invented")
	}
	original := strings.Repeat("a", 32)
	if err := WithSnapshot(context.Background(), dir, original, func(s Snapshot) error {
		if string(s.Secret) != original {
			t.Fatal("configured key ignored")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
