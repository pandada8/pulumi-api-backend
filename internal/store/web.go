package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/pandada8/pulumi-api-backend/internal/core"
	"github.com/pandada8/pulumi-api-backend/internal/pulumicompat"
	"strconv"
	"time"
)

func (s *Store) CreateSession(ctx context.Context, token string) (string, string, error) {
	sid, csrf := core.RandomToken(), core.RandomToken()
	e := s.Tx(ctx, func(tx *sql.Tx) error {
		a, e := s.Actor(ctx, tx, core.Digest([]byte(token)))
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO web_sessions(digest,token_id,csrf_digest,expires_at) VALUES($1,$2,$3,clock_timestamp()+interval '8 hours')`, core.Digest([]byte(sid)), a.TokenID, core.Digest([]byte(csrf)))
		return e
	})
	return sid, csrf, e
}
func (s *Store) Session(ctx context.Context, tx *sql.Tx, sid, csrf string, checkCSRF bool) (Actor, error) {
	var digest []byte
	var a Actor
	q := `SELECT t.digest FROM web_sessions w JOIN api_tokens t ON t.id=w.token_id WHERE w.digest=$1 AND w.expires_at>clock_timestamp()`
	if checkCSRF {
		q += ` AND w.csrf_digest=$2`
		if e := tx.QueryRowContext(ctx, q, core.Digest([]byte(sid)), core.Digest([]byte(csrf))).Scan(&digest); e != nil {
			return a, Fail(401, "Invalid session")
		}
	} else {
		if e := tx.QueryRowContext(ctx, q, core.Digest([]byte(sid))).Scan(&digest); e != nil {
			return a, Fail(401, "Invalid session")
		}
	}
	return s.Actor(ctx, tx, digest)
}
func (s *Store) Logout(ctx context.Context, tx *sql.Tx, sid string) error {
	_, e := tx.ExecContext(ctx, `DELETE FROM web_sessions WHERE digest=$1`, core.Digest([]byte(sid)))
	return e
}
func (s *Store) WebStack(ctx context.Context, tx *sql.Tx, a Actor, id string) (map[string]any, error) {
	var org, project, name string
	if e := tx.QueryRowContext(ctx, `SELECT o.name,n.project,n.name FROM stack_names n JOIN organizations o ON o.id=n.org_id WHERE n.stack_id=$1 AND n.is_current`, id).Scan(&org, &project, &name); e != nil {
		return nil, Translate(e)
	}
	st, e := s.Stack(ctx, tx, org, project, name, false)
	if e != nil {
		return nil, e
	}
	if e = s.Permission(ctx, tx, a, st.OrgID, false, false); e != nil {
		return nil, e
	}
	return s.WebStackResolved(ctx, tx, a, st)
}
func (s *Store) WebStackResolved(ctx context.Context, tx *sql.Tx, a Actor, st Stack) (map[string]any, error) {
	raw, invalid, e := s.Head(ctx, tx, st.ID)
	if e != nil {
		return nil, e
	}
	var m map[string]any
	if e = pulumicompat.Decode(raw, &m); e != nil {
		return nil, e
	}
	state := pulumicompat.Redact(m).(map[string]any)["deployment"]
	var generation int64
	if e = tx.QueryRowContext(ctx, `SELECT generation FROM stack_heads WHERE stack_id=$1`, st.ID).Scan(&generation); e != nil {
		return nil, e
	}
	history, e := s.History(ctx, tx, st, 1, 100, false)
	if e != nil {
		return nil, e
	}
	status := "idle"
	if st.Active != "" {
		status = "running"
	}
	if invalid {
		status = "invalid"
	}
	var genericHistory any
	historyRaw, e := json.Marshal(history)
	if e != nil {
		return nil, e
	}
	if e = pulumicompat.Decode(historyRaw, &genericHistory); e != nil {
		return nil, e
	}
	return map[string]any{"id": st.ID, "org": st.Org, "project": st.Project, "name": st.Name, "version": st.Version, "status": status, "activeUpdate": st.Active, "headGeneration": generation, "resourceCount": pulumicompat.ResourceCount(raw), "state": state, "history": pulumicompat.Redact(genericHistory)}, nil
}
func (s *Store) WebUpdate(ctx context.Context, tx *sql.Tx, a Actor, id string) (map[string]any, error) {
	var sid string
	if e := tx.QueryRowContext(ctx, `SELECT stack_id FROM updates WHERE id=$1`, id).Scan(&sid); e != nil {
		return nil, Translate(e)
	}
	summary, e := s.WebStack(ctx, tx, a, sid)
	if e != nil {
		return nil, e
	}
	u, e := s.Update(ctx, tx, sid, id)
	if e != nil {
		return nil, e
	}
	var request any
	json.Unmarshal(u.Request, &request)
	now, e := dbNow(ctx, tx)
	if e != nil {
		return nil, e
	}
	out := map[string]any{"id": u.ID, "version": u.Version, "kind": u.Kind, "dryRun": u.Dry, "status": u.Status, "startedAt": u.Started, "endedAt": u.Ended, "eventsClosed": u.Status != "running" && now.After(u.Closed), "request": pulumicompat.Redact(request), "stack": summary}
	if !u.Dry && u.Status != "running" && u.Status != "created" {
		var raw []byte
		if raw, e = s.ExportVersion(ctx, tx, Stack{ID: sid}, int(u.Version)); e != nil {
			return nil, e
		}
		var m any
		pulumicompat.Decode(raw, &m)
		out["state"] = pulumicompat.Redact(m)
		var base []byte
		if u.Base != "" {
			if e = tx.QueryRowContext(ctx, `SELECT raw_bytes FROM snapshots WHERE id=$1`, u.Base).Scan(&base); e != nil {
				return nil, e
			}
		}
		diff, e := pulumicompat.Diff(base, raw)
		if e != nil {
			return nil, e
		}
		out["diff"] = diff
	}
	return out, nil
}
func (s *Store) TokenIDs(ctx context.Context) ([]map[string]any, error) {
	rows, e := s.DB.QueryContext(ctx, `SELECT t.id,p.login,COALESCE(t.revoked_at,'epoch') FROM api_tokens t JOIN principals p ON p.id=t.principal_id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, login string
		var revoked time.Time
		if e = rows.Scan(&id, &login, &revoked); e != nil {
			return nil, e
		}
		out = append(out, map[string]any{"id": id, "user": login, "revoked": revoked.Unix() > 0})
	}
	return out, rows.Err()
}
func (s *Store) UpdateIDForVersion(ctx context.Context, tx *sql.Tx, stack string, version int) (string, error) {
	var id string
	e := tx.QueryRowContext(ctx, `SELECT id FROM updates WHERE stack_id=$1 AND version=$2 AND NOT dry_run`, stack, version).Scan(&id)
	return id, Translate(e)
}
func (s *Store) PageItems(ctx context.Context, tx *sql.Tx, a Actor, kind, scope, filter, token string, items []any) ([]any, string, error) {
	c, e := s.Cursor(ctx, tx, a, kind, scope, filter, token)
	if e != nil {
		return nil, "", e
	}
	position := 0
	if c.Position != "" {
		position, e = strconv.Atoi(c.Position)
		if e != nil || position < 0 || position > len(items) {
			return nil, "", Fail(400, "Invalid continuation token")
		}
	}
	end := position + 100
	if end >= len(items) {
		return items[position:], "", nil
	}
	c.Position = strconv.Itoa(end)
	return items[position:end], core.EncodeCursor(core.Purpose(s.Config.Master, "cursor"), c), nil
}
func (s *Store) PageStacks(ctx context.Context, tx *sql.Tx, a Actor, filter, token string, items []map[string]any) ([]map[string]any, string, error) {
	c, e := s.Cursor(ctx, tx, a, "web-stacks", "", filter, token)
	if e != nil {
		return nil, "", e
	}
	filtered := []map[string]any{}
	for _, item := range items {
		if item["id"].(string) > c.Position {
			filtered = append(filtered, item)
		}
	}
	if len(filtered) <= 100 {
		return filtered, "", nil
	}
	c.Position = filtered[99]["id"].(string)
	return filtered[:100], core.EncodeCursor(core.Purpose(s.Config.Master, "cursor"), c), nil
}
