package store

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/pandada8/pulumi-api-backend/internal/core"
	"github.com/pandada8/pulumi-api-backend/internal/faults"
	"strings"
)

func (s *Store) ExpirePath(ctx context.Context, path string) error {
	p := strings.Split(strings.Trim(path, "/"), "/")
	if len(p) < 5 || p[0] != "api" || p[1] != "stacks" {
		return nil
	}
	return s.Tx(context.WithValue(ctx, faults.SkipKey{}, true), func(tx *sql.Tx) error {
		st, e := s.Stack(ctx, tx, p[2], p[3], p[4], true)
		if api, ok := e.(*Error); ok && api.Code == 404 {
			return nil
		}
		if e != nil {
			return e
		}
		return s.ExpireActive(ctx, tx, &st)
	})
}

// WorkOne claims a bounded durable job. Replay never destroys the original journal.
func (s *Store) WorkOne(ctx context.Context) error {
	var id string
	var payload []byte
	e := s.Tx(ctx, func(tx *sql.Tx) error {
		e := tx.QueryRowContext(ctx, `SELECT id,payload FROM jobs WHERE (status='pending' AND available_at<=clock_timestamp()) OR (status='running' AND locked_until<clock_timestamp()) ORDER BY available_at FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&id, &payload)
		if e == sql.ErrNoRows {
			return nil
		}
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `UPDATE jobs SET status='running',attempts=attempts+1,locked_until=clock_timestamp()+interval '120 seconds' WHERE id=$1`, id)
		return e
	})
	if e != nil || id == "" {
		return e
	}
	var p struct {
		UpdateID string `json:"updateID"`
	}
	if e = json.Unmarshal(payload, &p); e == nil {
		e = s.materialize(ctx, p.UpdateID)
	}
	if e != nil {
		_, err := s.DB.ExecContext(ctx, `UPDATE jobs SET status='pending',available_at=clock_timestamp()+make_interval(secs=>LEAST(300,power(2,LEAST(attempts,8)))::integer),last_error='materialization failed' WHERE id=$1`, id)
		if err != nil {
			return err
		}
		return e
	}
	_, e = s.DB.ExecContext(ctx, `UPDATE jobs SET status='done',last_error=NULL WHERE id=$1`, id)
	return e
}
func (s *Store) materialize(ctx context.Context, id string) error {
	var stack string
	if e := s.DB.QueryRowContext(ctx, `SELECT stack_id FROM updates WHERE id=$1`, id).Scan(&stack); e != nil {
		return e
	}
	return s.Tx(ctx, func(tx *sql.Tx) error {
		var ignored string
		if e := tx.QueryRowContext(ctx, `SELECT id FROM stacks WHERE id=$1 FOR UPDATE`, stack).Scan(&ignored); e != nil {
			return e
		}
		u, e := s.Update(ctx, tx, stack, id)
		if e != nil {
			return e
		}
		if u.Status == "running" || u.Status == "created" {
			return Fail(409, "Update is not terminal")
		}
		if u.Final != "" {
			return nil
		}
		var raw, hash []byte
		if e = tx.QueryRowContext(ctx, `SELECT raw_bytes,sha256 FROM snapshots WHERE id=$1`, u.Base).Scan(&raw, &hash); e != nil {
			return e
		}
		if !hmac.Equal(hash, core.Digest(raw)) {
			return Fail(500, "Snapshot integrity failure")
		}
		raw, e = s.Replay(ctx, tx, u.ID, raw, u.JournalCount)
		if e != nil {
			return e
		}
		sid, e := s.InsertSnapshot(ctx, tx, stack, raw, false)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO materializations(stack_id,update_id,upto,replayer_version,snapshot_id) VALUES($1,$2,$3,'21bf19ba',$4) ON CONFLICT DO NOTHING`, stack, id, u.JournalCount, sid)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `UPDATE updates SET final_snapshot_id=$2 WHERE id=$1`, id, sid)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `UPDATE stack_heads SET snapshot_id=$4,journal_update_id=NULL,journal_upto=0,generation=generation+1 WHERE stack_id=$1 AND journal_update_id=$2 AND journal_upto=$3`, stack, id, u.JournalCount, sid)
		return e
	})
}
func (s *Store) QueueMaterialization(ctx context.Context, id string) error {
	_, e := s.DB.ExecContext(ctx, `INSERT INTO jobs(id,job_key,kind,payload,status) VALUES($1,$2,'materialize',$3,'pending') ON CONFLICT DO NOTHING`, uuid.NewString(), "materialize:"+id, json.RawMessage(`{"updateID":"`+id+`"}`))
	return e
}
