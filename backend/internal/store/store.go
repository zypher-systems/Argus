package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var pgxErrNoRows = pgx.ErrNoRows

//go:embed migrations/*.sql
var migrationsFS embed.FS

type Store struct {
	Pool   *pgxpool.Pool
	hosted bool
}

func New(ctx context.Context, dbURL string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		return nil, fmt.Errorf("parse db url: %w", err)
	}
	cfg.MaxConns = 20
	cfg.MinConns = 2
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	ctxPing, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(ctxPing); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db ping: %w", err)
	}
	s := &Store{Pool: pool}
	if err := s.migrate(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Store) Close() {
	s.Pool.Close()
}

// migrateLock is the advisory lock held while migrations are applied.
const migrateLock int64 = 0x4172677573 // "Argus"

func (s *Store) migrate(ctx context.Context) error {
	// api and probe start together and both migrate. Without a lock they race
	// to create the same tables and the loser exits. The lock belongs to one
	// session, so it is taken and given back on a connection held for this.
	conn, err := s.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer func() {
		// A connection that could not unlock must not go back to the pool
		// still holding the lock.
		if _, err := conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, migrateLock); err != nil {
			_ = conn.Conn().Close(context.Background())
		}
		conn.Release()
	}()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrateLock); err != nil {
		return err
	}
	if _, err := s.Pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`); err != nil {
		return err
	}
	entries, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(entries)
	for _, name := range entries {
		version := strings.TrimSuffix(filepath.Base(name), ".sql")
		var exists bool
		if err := s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, version).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		content, err := migrationsFS.ReadFile(name)
		if err != nil {
			return err
		}
		tx, err := s.Pool.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(content)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply %s: %w", version, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	return nil
}

const (
	ChannelConfig   = "argus_config"
	ChannelEvents   = "argus_events"
	ChannelCheckNow = "argus_check_now"
)

func (s *Store) NotifyConfig(ctx context.Context) error {
	_, err := s.Pool.Exec(ctx, `SELECT pg_notify($1, '')`, ChannelConfig)
	return err
}

func (s *Store) PublishEvent(ctx context.Context, payload string) error {
	_, err := s.Pool.Exec(ctx, `SELECT pg_notify($1, $2)`, ChannelEvents, payload)
	return err
}

func (s *Store) NotifyCheckNow(ctx context.Context, requestID int64) error {
	_, err := s.Pool.Exec(ctx, `SELECT pg_notify($1, $2)`, ChannelCheckNow, fmt.Sprintf("%d", requestID))
	return err
}
