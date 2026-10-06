package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"github.com/lib/pq"
	"github.com/pandada8/pulumid/internal/config"
	"github.com/pandada8/pulumid/internal/faults"
	"sync"
)

//go:embed schema.sql
var migrations embed.FS

type Store struct {
	DB         *sql.DB
	Config     config.Config
	finalizers sync.Map
}
type Error struct {
	Code    int
	Message string
}

func (e *Error) Error() string            { return e.Message }
func Fail(code int, message string) error { return &Error{code, message} }
func Translate(e error) error {
	if e == nil {
		return nil
	}
	var api *Error
	if errors.As(e, &api) {
		return e
	}
	if errors.Is(e, sql.ErrNoRows) {
		return Fail(404, "Not found")
	}
	var p *pq.Error
	if errors.As(e, &p) {
		switch p.Code {
		case "23505":
			return Fail(409, "Conflict")
		case "55P03", "57014", "40001", "40P01":
			return Fail(503, "Database busy")
		}
	}
	return e
}
func New(c config.Config) (*Store, error) {
	db, e := sql.Open("postgres", c.DatabaseURL)
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(20)
	s := &Store{DB: db, Config: c}
	return s, nil
}
func (s *Store) Ready(ctx context.Context) error {
	var v int
	e := s.DB.QueryRowContext(ctx, "SELECT max(version) FROM schema_migrations").Scan(&v)
	if e != nil {
		return e
	}
	if v != 1 {
		return fmt.Errorf("unsupported migration version %d", v)
	}
	return nil
}
func (s *Store) Migrate(ctx context.Context) error {
	c, e := s.DB.Conn(ctx)
	if e != nil {
		return e
	}
	defer c.Close()
	if _, e = c.ExecContext(ctx, "SELECT pg_advisory_lock(71842913)"); e != nil {
		return e
	}
	defer c.ExecContext(context.Background(), "SELECT pg_advisory_unlock(71842913)")
	var found bool
	if e = c.QueryRowContext(ctx, "SELECT to_regclass('schema_migrations') IS NOT NULL").Scan(&found); e != nil {
		return e
	}
	if found {
		return s.Ready(ctx)
	}
	raw, _ := migrations.ReadFile("schema.sql")
	_, e = c.ExecContext(ctx, string(raw))
	return e
}
func (s *Store) Tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, e := s.DB.BeginTx(ctx, nil)
	if e != nil {
		return Fail(503, "Database unavailable")
	}
	committed := false
	defer func() {
		tx.Rollback()
		if hooks, ok := s.finalizers.LoadAndDelete(tx); ok {
			for _, fn := range *hooks.(*[]func(bool)) {
				fn(committed)
			}
		}
	}()
	if _, e = tx.ExecContext(ctx, "SET LOCAL lock_timeout='5s'; SET LOCAL statement_timeout='30s'"); e != nil {
		return e
	}
	if e = fn(tx); e != nil {
		return Translate(e)
	}
	if e = faults.Hit(ctx, "before_db_commit"); e != nil {
		return e
	}
	e = tx.Commit()
	committed = e == nil
	return Translate(e)
}

// afterFinish publishes caches only after the database outcome is known.
func (s *Store) afterFinish(tx *sql.Tx, fn func(bool)) {
	hooks, _ := s.finalizers.LoadOrStore(tx, &[]func(bool){})
	p := hooks.(*[]func(bool))
	*p = append(*p, fn)
}
