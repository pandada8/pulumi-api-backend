package store

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/pandada8/pulumid/internal/core"
	"github.com/pandada8/pulumid/internal/pulumicompat"
	"strconv"
)

func (s *Store) EventsWrite(ctx context.Context, tx *sql.Tx, st Stack, u Update, token string, events []json.RawMessage) error {
	if e := s.LeaseIdentity(ctx, tx, st, u, token); e != nil {
		return e
	}
	if len(events) > 1000 {
		return Fail(413, "Too many events")
	}
	now, e := dbNow(ctx, tx)
	if e != nil {
		return e
	}
	allowed := now.Before(u.Closed) && u.Complete.Valid && u.Status != "cancelled"
	if u.Status == "running" {
		if e = s.Lease(ctx, tx, st, u, token); e != nil {
			return e
		}
		allowed = true
	}
	count := u.EventCount
	for _, b := range events {
		var m map[string]any
		if e = pulumicompat.Decode(b, &m); e != nil {
			return Fail(400, "Invalid event")
		}
		n, ok := m["sequence"].(json.Number)
		if !ok {
			return Fail(400, "Missing event sequence")
		}
		seq, e := n.Int64()
		if e != nil || seq < 0 {
			return Fail(400, "Invalid event sequence")
		}
		canonical, _ := json.Marshal(m)
		hash := core.Digest(canonical)
		var old []byte
		e = tx.QueryRowContext(ctx, `SELECT digest FROM engine_events WHERE update_id=$1 AND sequence=$2`, u.ID, seq).Scan(&old)
		if e == nil {
			if !hmac.Equal(old, hash) {
				return Fail(409, "Conflicting event sequence")
			}
			continue
		}
		if e != sql.ErrNoRows {
			return e
		}
		if !allowed {
			return Fail(409, "Events closed")
		}
		typ := ""
		for k := range m {
			if k != "sequence" && k != "timestamp" {
				if typ != "" {
					return Fail(400, "Event must contain exactly one payload")
				}
				typ = k
			}
		}
		if typ == "" {
			return Fail(400, "Missing event payload")
		}
		urn := ""
		if v, ok := m[typ].(map[string]any); ok {
			if meta, ok := v["metadata"].(map[string]any); ok {
				urn, _ = meta["urn"].(string)
			}
			if v, ok := v["urn"].(string); ok {
				urn = v
			}
		}
		count++
		_, e = tx.ExecContext(ctx, `INSERT INTO engine_events(update_id,sequence,ingest_order,event_type,urn,payload,digest) VALUES($1,$2,$3,$4,$5,$6,$7)`, u.ID, seq, count, typ, urn, canonical, hash)
		if e != nil {
			return e
		}
	}
	_, e = tx.ExecContext(ctx, `UPDATE updates SET event_count=$2 WHERE id=$1`, u.ID, count)
	return e
}
func (s *Store) Cursor(ctx context.Context, tx *sql.Tx, a Actor, kind, scope, filter, token string) (core.Cursor, error) {
	var generation string
	if e := tx.QueryRowContext(ctx, `SELECT generation FROM service_meta`).Scan(&generation); e != nil {
		return core.Cursor{}, e
	}
	c := core.Cursor{Kind: kind, Principal: a.PrincipalID, Scope: scope, Filter: fmt.Sprintf("%x", core.Digest([]byte(filter))), Generation: generation}
	if token != "" {
		var e error
		c, e = core.DecodeCursor(core.Purpose(s.Config.Master, "cursor"), token, c)
		if e != nil {
			return c, Fail(400, e.Error())
		}
	}
	return c, nil
}
func (s *Store) EventsRead(ctx context.Context, tx *sql.Tx, a Actor, u Update, token string, types []string, urn string, legacy bool) (map[string]any, error) {
	filter, _ := json.Marshal([]any{types, urn, legacy})
	c, e := s.Cursor(ctx, tx, a, "events", u.ID, string(filter), token)
	if e != nil {
		return nil, e
	}
	pos := int64(0)
	if c.Position != "" {
		pos, e = strconv.ParseInt(c.Position, 10, 64)
		if e != nil {
			return nil, Fail(400, "Invalid continuation token")
		}
	}
	rows, e := tx.QueryContext(ctx, `SELECT ingest_order,event_type,urn,payload FROM engine_events WHERE update_id=$1 AND ingest_order>$2 ORDER BY ingest_order LIMIT 100`, u.ID, pos)
	if e != nil {
		return nil, e
	}
	out := []any{}
	last := pos
	for rows.Next() {
		var t, r string
		var raw []byte
		if e = rows.Scan(&last, &t, &r, &raw); e != nil {
			rows.Close()
			return nil, e
		}
		if urn != "" && urn != r {
			continue
		}
		if len(types) > 0 {
			found := false
			for _, typ := range types {
				if typ == t {
					found = true
				}
			}
			if !found {
				continue
			}
		}
		var m map[string]any
		if e = pulumicompat.Decode(raw, &m); e != nil {
			rows.Close()
			return nil, e
		}
		if legacy {
			if t != "stdoutEvent" && t != "diagnosticEvent" {
				continue
			}
			payload, _ := m[t].(map[string]any)
			text, _ := payload["message"].(string)
			kind := "stdout"
			if payload["severity"] == "error" || payload["severity"] == "warning" {
				kind = "stderr"
			}
			out = append(out, map[string]any{"index": strconv.FormatInt(last, 10), "kind": kind, "fields": map[string]any{"text": text}})
		} else {
			out = append(out, m)
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	now, e := dbNow(ctx, tx)
	if e != nil {
		return nil, e
	}
	var next any
	if last < u.EventCount || u.Status == "running" || now.Before(u.Closed) {
		c.Position = strconv.FormatInt(last, 10)
		next = core.EncodeCursor(core.Purpose(s.Config.Master, "cursor"), c)
	}
	result := map[string]any{"events": out, "continuationToken": next}
	if legacy {
		status := u.Status
		if status == "created" {
			status = "not started"
		}
		if status == "cancelled" {
			status = "failed"
		}
		result["status"] = status
	}
	return result, nil
}
func (s *Store) Audit(ctx context.Context, tx *sql.Tx, a Actor, st Stack, action string) error {
	_, e := tx.ExecContext(ctx, `INSERT INTO audit_log(principal_id,org_id,stack_id,action,result,request_id) VALUES($1,$2,$3,$4,'success',gen_random_uuid())`, a.PrincipalID, st.OrgID, st.ID, action)
	return e
}
