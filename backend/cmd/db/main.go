// Command db applies schema migrations or imports an offline JSON snapshot.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"syscall"
	"text/template"
	"time"
	_ "time/tzdata"

	"github.com/pressly/goose/v3"

	"rhythm/internal/filestore"
	"rhythm/internal/postgresstore"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		return errors.New("usage: rhythm-db migrate | migrate-to VERSION | status | create [-dir DIR] NAME | import-json -dir DATA_DIR")
	}
	command := os.Args[1]
	var target int64
	switch command {
	case "create":
		return createMigration(os.Args[2:])
	case "migrate", "status":
		if len(os.Args) != 2 {
			return fmt.Errorf("%s accepts no extra arguments", command)
		}
	case "migrate-to":
		if len(os.Args) != 3 {
			return errors.New("usage: rhythm-db migrate-to VERSION")
		}
		var err error
		target, err = strconv.ParseInt(os.Args[2], 10, 64)
		if err != nil || target < 1 {
			return errors.New("migration target must be a positive integer")
		}
	case "import-json":
	default:
		return errors.New("unknown command; use migrate, migrate-to, status, create or import-json")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return errors.New("DATABASE_URL is required")
	}
	s, err := postgresstore.Open(ctx, url)
	if err != nil {
		return err
	}
	defer s.Close()
	switch command {
	case "migrate", "migrate-to":
		var err error
		if command == "migrate" {
			err = s.Migrate(ctx)
		} else {
			err = s.MigrateTo(ctx, target)
		}
		if err != nil {
			return err
		}
		status, err := s.Status(ctx)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(status)
	case "status":
		status, err := s.Status(ctx)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(status)
	case "import-json":
		flags := flag.NewFlagSet("import-json", flag.ContinueOnError)
		dir := flags.String("dir", ".data", "offline source directory (stop the old server first)")
		if err := flags.Parse(os.Args[2:]); err != nil {
			return err
		}
		if flags.NArg() != 0 {
			return errors.New("unexpected import argument")
		}
		if err := s.Check(ctx); err != nil {
			return err
		}
		zone := os.Getenv("TIME_ZONE")
		if zone == "" {
			zone = "Europe/Moscow"
		}
		location, err := time.LoadLocation(zone)
		if err != nil {
			return errors.New("invalid TIME_ZONE")
		}
		return filestore.WithSnapshot(ctx, *dir, os.Getenv("SESSION_SECRET"), func(snapshot filestore.Snapshot) error {
			data := postgresstore.ImportData{Records: snapshot.Records, Accounts: snapshot.Accounts, Secret: snapshot.Secret, Fingerprint: snapshot.Fingerprint}
			now := time.Now().In(location)
			if err := s.PreflightImport(ctx, data, now); err != nil {
				return err
			}
			if err := filestore.MarkCutover(ctx, *dir, snapshot.Fingerprint, "pending"); err != nil {
				return err
			}
			result, err := s.Import(ctx, data, now)
			if err != nil {
				return err
			}
			if err := filestore.MarkCutover(ctx, *dir, snapshot.Fingerprint, "completed"); err != nil {
				return err
			}
			return json.NewEncoder(os.Stdout).Encode(result)
		})
	default:
		return errors.New("unknown database command")
	}
}

// createMigration is an offline authoring command. Goose's default version is
// the current UTC timestamp; runtime providers never use global registration.
func createMigration(args []string) error {
	flags := flag.NewFlagSet("create", flag.ContinueOnError)
	dir := flags.String("dir", "database/migrations", "directory containing UTC timestamp SQL migrations")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("usage: rhythm-db create [-dir DIR] NAME")
	}
	name := flags.Arg(0)
	valid, err := regexp.MatchString(`^[a-z][a-z0-9_]{0,63}$`, name)
	if err != nil {
		return err
	}
	if !valid {
		return errors.New("migration name must start with a lowercase letter and contain only lowercase letters, digits and underscores (maximum 64 characters)")
	}
	info, err := os.Stat(*dir)
	if err != nil {
		return fmt.Errorf("migration directory: %w", err)
	}
	if !info.IsDir() {
		return errors.New("migration directory must be a directory")
	}
	tmpl, err := template.New("forward.sql").Parse(`-- +goose Up
-- Replace SELECT 1 with the reviewed additive schema change before applying.
-- Applied migrations are immutable. This project does not provide destructive Down.
SELECT 1;
`)
	if err != nil {
		return err
	}
	return goose.CreateWithTemplate(nil, *dir, tmpl, name, "sql")
}
