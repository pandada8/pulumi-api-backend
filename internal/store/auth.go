package store

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/google/uuid"
	"github.com/pandada8/pulumi-api-backend/internal/core"
	"os"
)

type Actor struct {
	TokenID, PrincipalID, Login, Name string
	Write, Decrypt                    bool
}

func (s *Store) Actor(ctx context.Context, tx *sql.Tx, digest []byte) (Actor, error) {
	var a Actor
	q := `SELECT t.id,p.id,p.login,p.display_name,t.can_write,t.can_decrypt FROM api_tokens t JOIN principals p ON p.id=t.principal_id WHERE digest=$1 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>clock_timestamp()) AND NOT p.disabled FOR SHARE OF t,p`
	e := tx.QueryRowContext(ctx, q, digest).Scan(&a.TokenID, &a.PrincipalID, &a.Login, &a.Name, &a.Write, &a.Decrypt)
	if e == sql.ErrNoRows {
		return a, Fail(401, "Invalid credentials")
	}
	return a, e
}
func (s *Store) Permission(ctx context.Context, tx *sql.Tx, a Actor, org string, write, decrypt bool) error {
	var role string
	var can bool
	e := tx.QueryRowContext(ctx, `SELECT role,can_decrypt FROM memberships WHERE org_id=$1 AND principal_id=$2 FOR SHARE`, org, a.PrincipalID).Scan(&role, &can)
	if e == sql.ErrNoRows {
		return Fail(404, "Not found")
	}
	if e != nil {
		return e
	}
	if write && (!a.Write || role == "reader") {
		return Fail(403, "Write permission required")
	}
	if decrypt && (!a.Decrypt || !can) {
		return Fail(403, "Decrypt permission required")
	}
	return nil
}
func (s *Store) Bootstrap(ctx context.Context, org, user, path string) error {
	token := core.RandomToken()
	e := s.Tx(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, `INSERT INTO service_meta(singleton,generation) VALUES(true,$1) ON CONFLICT DO NOTHING`, uuid.NewString())
		if e != nil {
			return e
		}
		var pid, oid string
		e = tx.QueryRowContext(ctx, `INSERT INTO principals(id,login,display_name) VALUES($1,$2,$2) ON CONFLICT(login) DO UPDATE SET login=EXCLUDED.login RETURNING id`, uuid.NewString(), user).Scan(&pid)
		if e != nil {
			return e
		}
		e = tx.QueryRowContext(ctx, `INSERT INTO organizations(id,name) VALUES($1,$2) ON CONFLICT(name) DO UPDATE SET name=EXCLUDED.name RETURNING id`, uuid.NewString(), org).Scan(&oid)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO memberships(org_id,principal_id,role,can_decrypt) VALUES($1,$2,'admin',true) ON CONFLICT DO NOTHING`, oid, pid)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO api_tokens(id,principal_id,digest,can_write,can_decrypt) VALUES($1,$2,$3,true,true)`, uuid.NewString(), pid, core.Digest([]byte(token)))
		return e
	})
	if e != nil {
		return e
	}
	return writeToken(path, token)
}
func writeToken(path, token string) error {
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	_, e = f.WriteString(token + "\n")
	if e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	return ce
}
func (s *Store) TokenCreate(ctx context.Context, user string, write, decrypt bool, path string) error {
	token := core.RandomToken()
	e := s.Tx(ctx, func(tx *sql.Tx) error {
		res, e := tx.ExecContext(ctx, `INSERT INTO api_tokens(id,principal_id,digest,can_write,can_decrypt) SELECT $1,id,$2,$3,$4 FROM principals WHERE login=$5 AND NOT disabled`, uuid.NewString(), core.Digest([]byte(token)), write, decrypt, user)
		if e != nil {
			return e
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			return Fail(404, "User not found")
		}
		return nil
	})
	if e != nil {
		return e
	}
	return writeToken(path, token)
}
func (s *Store) Revoke(ctx context.Context, id string) error {
	_, e := s.DB.ExecContext(ctx, `UPDATE api_tokens SET revoked_at=clock_timestamp() WHERE id=$1`, id)
	return e
}
func (s *Store) Member(ctx context.Context, org, user, role string, decrypt bool) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		var pid, oid string
		e := tx.QueryRowContext(ctx, `INSERT INTO principals(id,login,display_name) VALUES($1,$2,$2) ON CONFLICT(login) DO UPDATE SET login=EXCLUDED.login RETURNING id`, uuid.NewString(), user).Scan(&pid)
		if e != nil {
			return e
		}
		if e = tx.QueryRowContext(ctx, `SELECT id FROM organizations WHERE name=$1`, org).Scan(&oid); e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO memberships(org_id,principal_id,role,can_decrypt) VALUES($1,$2,$3,$4) ON CONFLICT(org_id,principal_id) DO UPDATE SET role=EXCLUDED.role,can_decrypt=EXCLUDED.can_decrypt`, oid, pid, role, decrypt)
		return e
	})
}
func (s *Store) RestoreGeneration(ctx context.Context) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, `UPDATE service_meta SET generation=$1`, uuid.NewString())
		return e
	})
}
func (s *Store) Organizations(ctx context.Context, tx *sql.Tx, a Actor) ([]map[string]any, error) {
	rows, e := tx.QueryContext(ctx, `SELECT o.name FROM organizations o JOIN memberships m ON o.id=m.org_id WHERE principal_id=$1 ORDER BY o.name`, a.PrincipalID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var n string
		if e = rows.Scan(&n); e != nil {
			return nil, e
		}
		out = append(out, map[string]any{"githubLogin": n, "name": n})
	}
	return out, rows.Err()
}
func wrapAAD(id string, v int) string { return fmt.Sprintf("wrap:%s:%d:file-v1", id, v) }
