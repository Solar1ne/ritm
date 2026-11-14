// Package filestore persists aggregates for one server process. Each update is
// serialized and replaces one private file atomically; no success is cached.
package filestore

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"

	"rhythm/internal/rhythm"
)

var principalPattern = regexp.MustCompile(`^[gtu]_[a-f0-9]{64}$`)

// MaxRecordBytes applies before writes and during reads, so a successful commit
// can never create a record too large for a subsequent load.
const MaxRecordBytes = 4 * 1024 * 1024

type Store struct {
	dir             string
	lock            chan struct{}
	migrationReader bool
	operationLock   *os.File
}

func New(dir string) (*Store, error) {
	if err := checkCutover(dir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return nil, fmt.Errorf("secure data directory: %w", err)
	}
	return &Store{dir: dir, lock: make(chan struct{}, 1)}, nil
}

func (s *Store) acquire(ctx context.Context) error {
	select {
	case s.lock <- struct{}{}:
		if err := ctx.Err(); err != nil {
			s.release()
			return err
		}
		file, err := migrationLock(ctx, s.dir, false)
		if err != nil {
			<-s.lock
			return err
		}
		s.operationLock = file
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *Store) release() {
	if s.operationLock != nil {
		// Closing releases the advisory lock even after a cancelled operation.
		s.operationLock.Close()
		s.operationLock = nil
	}
	<-s.lock
}

func (s *Store) Read(ctx context.Context, principal string) (rhythm.Record, error) {
	if err := s.acquire(ctx); err != nil {
		return rhythm.Record{}, err
	}
	defer s.release()
	return s.read(ctx, principal)
}

func (s *Store) Update(ctx context.Context, principal string, apply func(rhythm.Record) (rhythm.Record, error)) (rhythm.Record, error) {
	if err := s.acquire(ctx); err != nil {
		return rhythm.Record{}, err
	}
	defer s.release()
	current, err := s.read(ctx, principal)
	if err != nil {
		return rhythm.Record{}, err
	}
	next, err := apply(current)
	if err != nil {
		return rhythm.Record{}, err
	}
	if next.Revision == current.Revision {
		return rhythm.Clone(current), nil
	}
	data, err := json.Marshal(next)
	if err != nil {
		return rhythm.Record{}, fmt.Errorf("encode record: %w", err)
	}
	if len(data) > MaxRecordBytes {
		return rhythm.Record{}, rhythm.ErrCapacity
	}
	if err := s.commit(ctx, filepath.Join(s.dir, recordFile(principal)), data); err != nil {
		return rhythm.Record{}, err
	}
	return rhythm.Clone(next), nil
}

func (s *Store) read(ctx context.Context, principal string) (rhythm.Record, error) {
	if err := ctx.Err(); err != nil {
		return rhythm.Record{}, err
	}
	if !principalPattern.MatchString(principal) {
		return rhythm.Record{}, errors.New("invalid storage principal")
	}
	if err := s.checkDiaryOwner(ctx, principal); err != nil {
		return rhythm.Record{}, err
	}
	// Missing data directory is degradation, not a fresh empty user.
	if err := s.Check(ctx); err != nil {
		return rhythm.Record{}, err
	}
	f, err := os.Open(filepath.Join(s.dir, recordFile(principal)))
	if errors.Is(err, os.ErrNotExist) {
		return rhythm.Blank(), nil
	}
	if err != nil {
		return rhythm.Record{}, fmt.Errorf("open record: %w", err)
	}
	defer f.Close() // Read-only handle: close does not affect durability.
	info, err := f.Stat()
	if err != nil {
		return rhythm.Record{}, fmt.Errorf("stat record: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > MaxRecordBytes {
		return rhythm.Record{}, errors.New("invalid record file")
	}
	dec := json.NewDecoder(io.LimitReader(f, MaxRecordBytes+1))
	dec.DisallowUnknownFields()
	var r rhythm.Record
	if err := dec.Decode(&r); err != nil {
		return rhythm.Record{}, fmt.Errorf("decode record: %w", err)
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return rhythm.Record{}, errors.New("record contains trailing data")
	}
	if len(r.Entries) > rhythm.MaxEntries || len(r.Receipts) > rhythm.MaxEntries || len(r.Weeks) > rhythm.MaxWeeks || len(r.Workouts) > rhythm.MaxWorkouts || len(r.HRVEntries) > rhythm.MaxWellnessEntries || len(r.HRVReceipts) > rhythm.MaxWellnessEntries || len(r.BodyEntries) > rhythm.MaxWellnessEntries || len(r.BodyReceipts) > rhythm.MaxWellnessEntries || len(r.MealSelections) > rhythm.MaxMealSelections {
		return rhythm.Record{}, errors.New("record exceeds history limit")
	}
	if r.Profile != nil {
		if err := rhythm.ValidateProfile(*r.Profile); err != nil {
			return rhythm.Record{}, fmt.Errorf("invalid stored profile: %v", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return rhythm.Record{}, err
	}
	if r.Entries == nil {
		r.Entries = []rhythm.Entry{}
	}
	return r, nil
}

func (s *Store) Check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !s.migrationReader {
		if err := checkCutover(s.dir); err != nil {
			return err
		}
	}
	info, err := os.Stat(s.dir)
	if err != nil {
		return fmt.Errorf("stat data directory: %w", err)
	}
	if !info.IsDir() {
		return errors.New("data directory is not a directory")
	}
	return nil
}

func (s *Store) commit(ctx context.Context, path string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := os.CreateTemp(s.dir, ".record-*")
	if err != nil {
		return fmt.Errorf("create temporary record: %w", err)
	}
	name := f.Name()
	defer os.Remove(name) // Best-effort cleanup after close or successful rename.
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return fmt.Errorf("secure record: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("write record: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync record: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close record: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("replace record: %w", err)
	}
	// The rename is the commit point. Sync the directory before acknowledging.
	dir, err := os.Open(s.dir)
	if err != nil {
		return fmt.Errorf("open directory after commit: %w", err)
	}
	if err := dir.Sync(); err != nil {
		dir.Close()
		return fmt.Errorf("sync directory after commit: %w", err)
	}
	if err := dir.Close(); err != nil {
		return fmt.Errorf("close directory after commit: %w", err)
	}
	return nil
}

// SessionSecret creates a private stable signing key when configuration does not
// supply one. Deployments must preserve this file alongside the user data.
func (s *Store) SessionSecret(ctx context.Context, configured string) ([]byte, error) {
	if len(configured) > 0 {
		if len(configured) < 32 {
			return nil, errors.New("SESSION_SECRET must contain at least 32 bytes")
		}
		return []byte(configured), nil
	}
	if err := s.acquire(ctx); err != nil {
		return nil, err
	}
	defer s.release()
	path := filepath.Join(s.dir, ".session-secret")
	data, err := os.ReadFile(path)
	if err == nil {
		key, decodeErr := base64.RawURLEncoding.DecodeString(string(data))
		if decodeErr != nil || len(key) != 32 {
			return nil, errors.New("invalid persisted session secret")
		}
		if err := os.Chmod(path, 0600); err != nil {
			return nil, fmt.Errorf("secure session secret: %w", err)
		}
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read session secret: %w", err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate session secret: %w", err)
	}
	if err := s.commit(ctx, path, []byte(base64.RawURLEncoding.EncodeToString(key))); err != nil {
		return nil, err
	}
	return key, nil
}
