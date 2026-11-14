package filestore

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"rhythm/internal/accounts"
	"rhythm/internal/rhythm"
)

const cutoverName = ".postgres-cutover.json"

var ErrCutover = errors.New("this DATA_DIR is migrating to PostgreSQL; file mode is disabled, use DATABASE_URL")

func checkCutover(dir string) error {
	_, err := os.Stat(filepath.Join(dir, cutoverName))
	if err == nil {
		return ErrCutover
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("check storage migration: %w", err)
}

func migrationLock(ctx context.Context, dir string, exclusive bool) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(dir, ".migration-lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	mode := syscall.LOCK_SH
	if exclusive {
		mode = syscall.LOCK_EX
	}
	for {
		if err := ctx.Err(); err != nil {
			f.Close()
			return nil, err
		}
		err := syscall.Flock(int(f.Fd()), mode|syscall.LOCK_NB)
		if err == nil {
			return f, nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			f.Close()
			return nil, err
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			f.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// Snapshot is an offline storage snapshot, not the public diary backup format.
// Its secret/credential fields must never be logged or exposed by HTTP.
type Snapshot struct {
	Records     map[string]rhythm.Record
	Accounts    accounts.Ledger
	Secret      []byte
	Fingerprint string
}

// WithSnapshot excludes new file-store I/O for the entire import callback.
// Older binaries must be stopped before calling this migration command.
func WithSnapshot(ctx context.Context, dir, configuredSecret string, apply func(Snapshot) error) error {
	lock, err := migrationLock(ctx, dir, true)
	if err != nil {
		return err
	}
	defer lock.Close() // Read-only snapshot lock; close releases it on every path.
	s := &Store{dir: dir, migrationReader: true}
	ledger, err := s.readAccounts(ctx)
	if err != nil {
		return err
	}
	key := []byte(configuredSecret)
	if configuredSecret == "" {
		encoded, err := os.ReadFile(filepath.Join(dir, ".session-secret"))
		if err != nil {
			return errors.New("source signing secret is missing; provide the original SESSION_SECRET")
		}
		key, err = base64.RawURLEncoding.DecodeString(string(encoded))
		if err != nil {
			return errors.New("source signing secret is invalid")
		}
	}
	if len(key) < 32 {
		return errors.New("source signing secret is too short")
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	snapshot := Snapshot{Records: map[string]rhythm.Record{}, Accounts: ledger, Secret: append([]byte(nil), key...)}
	total := int64(0)
	for _, file := range files {
		if strings.HasPrefix(file.Name(), ".") || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		principal := strings.TrimSuffix(file.Name(), ".json")
		if !accounts.ValidPrincipal(principal) || strings.HasPrefix(principal, "u_") || file.Type()&os.ModeSymlink != 0 {
			return errors.New("unexpected source diary file")
		}
		info, err := file.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		if total > 128*1024*1024 || len(snapshot.Records) >= 10000 {
			return errors.New("offline import exceeds 128 MiB or 10000 diaries; use a batched migration")
		}
		owner := principal
		if _, claimed := ledger.Accounts["u_"+principal[2:]]; strings.HasPrefix(principal, "g_") && claimed {
			owner = "u_" + principal[2:]
		}
		record, err := s.read(ctx, owner)
		if err != nil {
			return fmt.Errorf("source diary validation failed: %w", err)
		}
		snapshot.Records[owner] = record
	}
	for principal := range ledger.Accounts {
		if _, exists := snapshot.Records[principal]; !exists {
			snapshot.Records[principal] = rhythm.Blank()
		}
	}
	// encoding/json sorts map keys; the fingerprint covers credentials and the
	// effective signing key as well as every revision, receipt and user record.
	body, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(body)
	snapshot.Fingerprint = hex.EncodeToString(digest[:])
	return apply(snapshot)
}

// MarkCutover writes and fsyncs pending BEFORE SQL commit. Pending and completed
// both disable file mode. An interrupted migration can only retry the same source.
func MarkCutover(ctx context.Context, dir, fingerprint, phase string) error {
	if len(fingerprint) != 64 || (phase != "pending" && phase != "completed") {
		return errors.New("invalid cutover marker")
	}
	path := filepath.Join(dir, cutoverName)
	if body, err := os.ReadFile(path); err == nil {
		var previous struct {
			Fingerprint string `json:"fingerprint"`
		}
		if json.Unmarshal(body, &previous) != nil || previous.Fingerprint != fingerprint {
			return errors.New("cutover source differs; do not remove the marker or mix histories")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	body, err := json.Marshal(map[string]string{"fingerprint": fingerprint, "phase": phase})
	if err != nil {
		return err
	}
	s := &Store{dir: dir}
	return s.commit(ctx, path, body)
}
