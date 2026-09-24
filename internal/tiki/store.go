package tiki

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

// Store owns a SQLite database. Reads use a bounded pool; writes are serialized
// on one connection. Mutations and their activity records commit together.
type Store struct {
	write         *sql.DB
	read          *sql.DB
	passwordSlots chan struct{}
}

func Open(path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, invalid("database path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(abs), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(abs, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	q := url.Values{"_pragma": {"foreign_keys(1)", "busy_timeout(5000)", "journal_mode(WAL)", "synchronous(FULL)"}, "_txlock": {"immediate"}}
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: abs, RawQuery: q.Encode()}).String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &Store{write: db, passwordSlots: make(chan struct{}, 2)}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		var version int
		if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
			return err
		}
		if version == 2 {
			return nil
		}
		if version != 0 {
			return fmt.Errorf("unsupported database schema %d", version)
		}
		_, err := tx.ExecContext(ctx, schema)
		return err
	})
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize database: %w", err)
	}
	// Each new connection receives these settings. Deferred transactions keep
	// multi-query reads on one snapshot without acquiring the writer lock.
	q = url.Values{"mode": {"ro"}, "_pragma": {"foreign_keys(1)", "busy_timeout(5000)", "query_only(1)"}, "_txlock": {"deferred"}}
	s.read, err = sql.Open("sqlite", (&url.URL{Scheme: "file", Path: abs, RawQuery: q.Encode()}).String())
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("open database readers: %w", err)
	}
	readers := min(max(runtime.GOMAXPROCS(0), 2), 8)
	s.read.SetMaxOpenConns(readers)
	s.read.SetMaxIdleConns(readers)
	if err = s.read.PingContext(ctx); err != nil {
		s.Close()
		return nil, fmt.Errorf("connect database readers: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error { return errors.Join(s.read.Close(), s.write.Close()) }

// Ping checks database availability for readiness probes.
func (s *Store) Ping(ctx context.Context) error { return s.read.PingContext(ctx) }

func (s *Store) transaction(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}
