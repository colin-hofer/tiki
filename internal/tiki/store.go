package tiki

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Store owns a SQLite database. Reads use a bounded pool; writes are serialized
// on one connection. Mutations and their activity records commit together.
type Store struct {
	write         *sql.DB
	read          *sql.DB
	passwordSlots chan struct{}
	changeMu      sync.Mutex
	changed       chan struct{}
}

// Open creates or migrates a database and opens its reader and writer pools.
// Existing migrations run atomically; unsupported schemas are left untouched.
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
	q := url.Values{"_pragma": {"foreign_keys(1)", "busy_timeout(5000)", "synchronous(FULL)"}, "_txlock": {"immediate"}}
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: abs, RawQuery: q.Encode()}).String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &Store{write: db, passwordSlots: make(chan struct{}, 2), changed: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := enableWAL(ctx, db); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable WAL: %w", err)
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		return migrate(ctx, tx)
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

// Close releases both pools after callers have stopped using the store.
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
	if err := tx.Commit(); err != nil {
		return err
	}
	s.notify()
	return nil
}

// Enabling WAL can require a lock upgrade that SQLite's busy handler cannot
// wait for. Retry only this idempotent startup step, never a caller's mutation.
func enableWAL(ctx context.Context, db *sql.DB) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		_, err := db.ExecContext(ctx, "PRAGMA journal_mode=WAL")
		var sqliteErr *sqlite.Error
		if !errors.As(err, &sqliteErr) || sqliteErr.Code() != sqlite3.SQLITE_BUSY {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// sql.Row and sql.Rows share the same projections.
type scanner interface{ Scan(...any) error }

// sql.DB and sql.Tx can read the same record, with or without a snapshot.
type rowQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}
