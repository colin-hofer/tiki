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

	_ "modernc.org/sqlite"
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
	s := &Store{write: db, passwordSlots: make(chan struct{}, 2), changed: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
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

// Changes closes after a successful write. Subscribe before reading Revision so
// a commit between the read and the wait cannot be missed. Wakeups coalesce and
// never wait for consumers; Revision remains the durable source of truth.
func (s *Store) Changes() <-chan struct{} {
	s.changeMu.Lock()
	defer s.changeMu.Unlock()
	return s.changed
}

func (s *Store) notify() {
	s.changeMu.Lock()
	defer s.changeMu.Unlock()
	close(s.changed)
	s.changed = make(chan struct{})
}

// Revision identifies the current item/activity and user-directory state.
// Every item mutation appends activity; user-directory writes increment a
// durable revision so role changes and removals also reach live clients.
type Revision struct{ Activity, Users ID }

func (s *Store) Revision(ctx context.Context) (Revision, error) {
	var revision Revision
	err := s.read.QueryRowContext(ctx, `SELECT
		(SELECT coalesce(max(id), 0) FROM activity),
		(SELECT revision FROM user_revision WHERE id=1)`).Scan(&revision.Activity, &revision.Users)
	return revision, err
}
