package store

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/pandada8/pulumid/internal/core"
	"github.com/pandada8/pulumid/internal/pulumicompat"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
	"time"
)

type Update struct {
	ID, StackID, Actor, Kind, Status, Mode, Base, Final, Generation string
	Dry                                                             bool
	Version, Fence, JournalCount, EventCount                        int64
	Digest, StartDigest, Response                                   []byte
	Request                                                         json.RawMessage
	Expires, Started, Ended, Closed                                 time.Time
	Complete                                                        sql.NullString
}

func dbNow(ctx context.Context, tx *sql.Tx) (time.Time, error) {
	var now time.Time
	e := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now)
	return now, e
}
func (s *Store) Update(ctx context.Context, tx *sql.Tx, stack, id string) (Update, error) {
	var u Update
	e := tx.QueryRowContext(ctx, `SELECT id,stack_id,actor_token_id,kind,status,mode,dry_run,COALESCE(base_snapshot_id::text,''),COALESCE(final_snapshot_id::text,''),COALESCE(version,0),COALESCE(fence,0),COALESCE(lease_generation::text,''),lease_digest,start_request_digest,start_response_encrypted,request,COALESCE(lease_expires_at,'epoch'),COALESCE(started_at,'epoch'),COALESCE(ended_at,'epoch'),COALESCE(event_closed_at,'epoch'),complete_status,journal_count,event_count FROM updates WHERE stack_id=$1 AND id=$2 FOR UPDATE`, stack, id).Scan(&u.ID, &u.StackID, &u.Actor, &u.Kind, &u.Status, &u.Mode, &u.Dry, &u.Base, &u.Final, &u.Version, &u.Fence, &u.Generation, &u.Digest, &u.StartDigest, &u.Response, &u.Request, &u.Expires, &u.Started, &u.Ended, &u.Closed, &u.Complete, &u.JournalCount, &u.EventCount)
	return u, Translate(e)
}
func (s *Store) CreateUpdate(ctx context.Context, tx *sql.Tx, a Actor, st Stack, kind string, req apitype.UpdateProgramRequest) (string, error) {
	if req.Name != st.Project {
		return "", Fail(400, "Project name must match stack path")
	}
	id := uuid.NewString()
	raw, _ := json.Marshal(req)
	dry := kind == "preview" || req.Options.DryRun
	_, e := tx.ExecContext(ctx, `INSERT INTO updates(id,stack_id,actor_token_id,kind,status,mode,dry_run,request) VALUES($1,$2,$3,$4,'created','none',$5,$6)`, id, st.ID, a.TokenID, kind, dry, raw)
	return id, e
}
func (s *Store) Start(ctx context.Context, tx *sql.Tx, a Actor, st *Stack, u *Update, req apitype.StartUpdateRequest) (apitype.StartUpdateResponse, error) {
	var out apitype.StartUpdateResponse
	normalized := map[string]any{"journalVersion": req.JournalVersion}
	if req.Tags != nil {
		normalized["tags"] = req.Tags
	}
	raw, _ := json.Marshal(normalized)
	digest := core.Digest(raw)
	if u.Actor != a.TokenID {
		return out, Fail(403, "Update belongs to another token")
	}
	if u.Status == "running" {
		now, e := dbNow(ctx, tx)
		if e != nil {
			return out, e
		}
		if !hmac.Equal(u.StartDigest, digest) || !now.Before(u.Expires) || st.Active != u.ID {
			return out, Fail(409, "Update already started")
		}
		b, e := core.Open(core.Purpose(s.Config.Master, "start-response"), u.Response, "start:"+u.ID+":"+u.Generation)
		if e != nil {
			return out, e
		}
		e = json.Unmarshal(b, &out)
		return out, e
	}
	if u.Status != "created" || st.Active != "" {
		return out, Fail(409, "Update in progress or terminal")
	}
	base, invalid, e := s.Head(ctx, tx, st.ID)
	if e != nil {
		return out, e
	}
	if invalid && !u.Dry {
		return out, Fail(409, "Invalid state requires explicit import")
	}
	sid, e := s.InsertSnapshot(ctx, tx, st.ID, base, invalid)
	if e != nil {
		return out, e
	}
	version := st.Version
	if !u.Dry {
		version++
	}
	mode := "full"
	if u.Dry {
		mode = "none"
	} else if s.Config.Journal && req.JournalVersion >= 1 {
		mode = "journal"
		out.JournalVersion = 1
	}
	now, e := dbNow(ctx, tx)
	if e != nil {
		return out, e
	}
	var generation string
	if e = tx.QueryRowContext(ctx, `SELECT generation FROM service_meta`).Scan(&generation); e != nil {
		return out, e
	}
	out.Token = core.RandomToken()
	out.Version = int(version)
	out.TokenExpiration = now.Add(300 * time.Second).Unix()
	response, _ := json.Marshal(out)
	sealed, e := core.Seal(core.Purpose(s.Config.Master, "start-response"), response, "start:"+u.ID+":"+generation)
	if e != nil {
		return out, e
	}
	_, e = tx.ExecContext(ctx, `UPDATE updates SET status='running',mode=$2,base_snapshot_id=$3,version=$4,fence=$5,lease_digest=$6,lease_generation=$7,lease_expires_at=$8,start_request_digest=$9,start_response_encrypted=$10,started_at=$11 WHERE id=$1`, u.ID, mode, sid, version, st.Fence+1, core.Digest([]byte(out.Token)), generation, now.Add(300*time.Second), digest, sealed, now)
	if e != nil {
		return out, e
	}
	_, e = tx.ExecContext(ctx, `UPDATE stacks SET active_update_id=$2,last_version=$3,fence=fence+1 WHERE id=$1`, st.ID, u.ID, version)
	if e != nil {
		return out, e
	}
	if req.Tags != nil {
		tags, _ := json.Marshal(req.Tags)
		_, e = tx.ExecContext(ctx, `UPDATE stacks SET tags=$2 WHERE id=$1`, st.ID, tags)
	}
	return out, e
}
func (s *Store) LeaseIdentity(ctx context.Context, tx *sql.Tx, st Stack, u Update, token string) error {
	if !hmac.Equal(u.Digest, core.Digest([]byte(token))) {
		return Fail(403, "Invalid update token")
	}
	var digest []byte
	if e := tx.QueryRowContext(ctx, `SELECT digest FROM api_tokens WHERE id=$1`, u.Actor).Scan(&digest); e != nil {
		return Fail(403, "Invalid update token")
	}
	a, e := s.Actor(ctx, tx, digest)
	if e != nil {
		return Fail(403, "Update authorization revoked")
	}
	if e = s.Permission(ctx, tx, a, st.OrgID, true, false); e != nil {
		return Fail(403, "Update authorization revoked")
	}
	var generation string
	if e = tx.QueryRowContext(ctx, `SELECT generation FROM service_meta`).Scan(&generation); e != nil {
		return e
	}
	if generation != u.Generation {
		return Fail(403, "Invalid update generation")
	}
	return nil
}
func (s *Store) Lease(ctx context.Context, tx *sql.Tx, st Stack, u Update, token string) error {
	if e := s.LeaseIdentity(ctx, tx, st, u, token); e != nil {
		return e
	}
	now, e := dbNow(ctx, tx)
	if e != nil {
		return e
	}
	if u.Status != "running" || st.Active != u.ID || st.Fence != u.Fence || !now.Before(u.Expires) {
		return Fail(403, "Expired or cancelled update token")
	}
	return nil
}
func (s *Store) Renew(ctx context.Context, tx *sql.Tx, st Stack, u Update, token string, duration int) (map[string]any, error) {
	if duration < 1 || duration > 300 {
		return nil, Fail(400, "duration must be 1..300")
	}
	if e := s.Lease(ctx, tx, st, u, token); e != nil {
		return nil, e
	}
	now, e := dbNow(ctx, tx)
	if e != nil {
		return nil, e
	}
	expiry := now.Add(300 * time.Second)
	_, e = tx.ExecContext(ctx, `UPDATE updates SET lease_expires_at=$2 WHERE id=$1`, u.ID, expiry)
	return map[string]any{"token": token, "tokenExpiration": expiry.Unix()}, e
}
func (s *Store) finish(ctx context.Context, tx *sql.Tx, st *Stack, u *Update, status, reason string, grace bool) error {
	var sid any
	var e error
	if u.Dry {
		sid = u.Base
	} else if u.Mode == "journal" {
		_, e = tx.ExecContext(ctx, `INSERT INTO jobs(id,job_key,kind,payload,status) VALUES($1,$2,'materialize',$3,'pending') ON CONFLICT(job_key) DO NOTHING`, uuid.NewString(), "materialize:"+u.ID, json.RawMessage(`{"updateID":"`+u.ID+`"}`))
		if e != nil {
			return e
		}
	} else {
		var snapshot string
		if e = tx.QueryRowContext(ctx, `SELECT snapshot_id FROM stack_heads WHERE stack_id=$1`, st.ID).Scan(&snapshot); e != nil {
			return e
		}
		sid = snapshot
	}
	s.afterFinish(tx, func(committed bool) {
		if committed {
			s.validators.Delete(u.ID)
		}
	})
	now, e := dbNow(ctx, tx)
	if e != nil {
		return e
	}
	closed := now
	if grace {
		closed = now.Add(30 * time.Second)
	}
	_, e = tx.ExecContext(ctx, `UPDATE updates SET status=$2,complete_status=$2,reason=$3,final_snapshot_id=$4,ended_at=$5,event_closed_at=$6 WHERE id=$1`, u.ID, status, reason, sid, now, closed)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `UPDATE stacks SET active_update_id=NULL,fence=fence+1 WHERE id=$1 AND active_update_id=$2`, st.ID, u.ID)
	return e
}
func (s *Store) Complete(ctx context.Context, tx *sql.Tx, st Stack, u Update, token, status string) error {
	if status != "succeeded" && status != "failed" && status != "cancelled" {
		return Fail(400, "Invalid completion status")
	}
	if e := s.LeaseIdentity(ctx, tx, st, u, token); e != nil {
		return e
	}
	if u.Complete.Valid {
		if u.Complete.String == status {
			return nil
		}
		return Fail(409, "Completion status differs")
	}
	if e := s.Lease(ctx, tx, st, u, token); e != nil {
		return e
	}
	return s.finish(ctx, tx, &st, &u, status, "", true)
}
func (s *Store) Cancel(ctx context.Context, tx *sql.Tx, st Stack, u Update) error {
	if u.Status == "cancelled" {
		return nil
	}
	if u.Status != "running" {
		return Fail(409, "Update is not running")
	}
	return s.finish(ctx, tx, &st, &u, "cancelled", "user_cancelled", false)
}
func (s *Store) SaveFull(ctx context.Context, tx *sql.Tx, st Stack, u Update, token string, req apitype.PatchUpdateCheckpointRequest) error {
	if e := s.Lease(ctx, tx, st, u, token); e != nil {
		return e
	}
	if u.Mode != "full" {
		return Fail(409, "Checkpoint requires full mode")
	}
	if req.Version != 3 || len(req.Features) > 0 {
		return Fail(422, "Unsupported checkpoint version/features")
	}
	var raw []byte
	if len(req.Deployment) == 0 || string(req.Deployment) == "null" {
		if !req.IsInvalid {
			return Fail(400, "Missing deployment")
		}
		var e error
		raw, _, e = s.Head(ctx, tx, st.ID)
		if e != nil {
			return e
		}
	} else {
		var e error
		raw = append(append([]byte(`{"version":3,"deployment":`), req.Deployment...), '}')
		if e != nil {
			return e
		}
		if e = pulumicompat.Validate(raw); e != nil {
			return Fail(422, e.Error())
		}
	}
	sid, e := s.InsertSnapshot(ctx, tx, st.ID, raw, req.IsInvalid)
	if e != nil {
		return e
	}
	return s.SetHead(ctx, tx, st.ID, sid)
}
func (s *Store) ExportVersion(ctx context.Context, tx *sql.Tx, st Stack, version int) ([]byte, error) {
	var id, status, final, base, mode string
	var count int64
	e := tx.QueryRowContext(ctx, `SELECT id,status,COALESCE(final_snapshot_id::text,''),COALESCE(base_snapshot_id::text,''),mode,journal_count FROM updates WHERE stack_id=$1 AND version=$2 AND NOT dry_run`, st.ID, version).Scan(&id, &status, &final, &base, &mode, &count)
	if e != nil {
		return nil, Translate(e)
	}
	if status == "running" {
		return nil, Fail(409, "History is not finalized")
	}
	var raw, hash []byte
	if final == "" && mode == "journal" {
		if e = tx.QueryRowContext(ctx, `SELECT raw_bytes,sha256 FROM snapshots WHERE id=$1 AND stack_id=$2`, base, st.ID).Scan(&raw, &hash); e != nil {
			return nil, e
		}
		if !hmac.Equal(hash, core.Digest(raw)) {
			return nil, Fail(500, "Snapshot integrity failure")
		}
		return s.Replay(ctx, tx, id, raw, count)
	}
	e = tx.QueryRowContext(ctx, `SELECT raw_bytes,sha256 FROM snapshots WHERE id=$1 AND stack_id=$2`, final, st.ID).Scan(&raw, &hash)
	if e != nil {
		return nil, e
	}
	if !hmac.Equal(hash, core.Digest(raw)) {
		return nil, Fail(500, "Snapshot integrity failure")
	}
	return raw, nil
}
func (s *Store) History(ctx context.Context, tx *sql.Tx, st Stack, page, size int, latest bool) ([]map[string]any, error) {
	if page < 1 || size < 1 || size > 100 {
		return nil, Fail(400, "Invalid history pagination")
	}
	q := `SELECT id,kind,status,request,COALESCE(version,0),COALESCE(started_at,created_at),COALESCE(ended_at,'epoch'),(SELECT payload->'summaryEvent'->'resourceChanges' FROM engine_events e WHERE e.update_id=updates.id AND e.event_type='summaryEvent' ORDER BY ingest_order DESC LIMIT 1) FROM updates WHERE stack_id=$1 AND NOT dry_run`
	if latest {
		q += ` AND status IN ('succeeded','failed','cancelled')`
	}
	q += ` ORDER BY version DESC NULLS LAST LIMIT $2 OFFSET $3`
	rows, e := tx.QueryContext(ctx, q, st.ID, size, (page-1)*size)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, kind, status string
		var req apitype.UpdateProgramRequest
		var raw, summary []byte
		var v int64
		var start, end time.Time
		if e = rows.Scan(&id, &kind, &status, &raw, &v, &start, &end, &summary); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(raw, &req); e != nil {
			return nil, e
		}
		result := status
		switch status {
		case "running":
			result = "in-progress"
		case "created":
			result = "not-started"
		case "cancelled":
			result = "failed"
		}
		if req.Config == nil {
			req.Config = map[string]apitype.ConfigValue{}
		}
		if req.Metadata.Environment == nil {
			req.Metadata.Environment = map[string]string{}
		}
		item := map[string]any{"updateID": id, "kind": kind, "startTime": start.Unix(), "endTime": end.Unix(), "message": req.Metadata.Message, "environment": req.Metadata.Environment, "config": req.Config, "result": result, "version": v}
		if len(summary) > 0 {
			var changes any
			if e = json.Unmarshal(summary, &changes); e != nil {
				return nil, e
			}
			item["resourceChanges"] = changes
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
func (s *Store) Sweep(ctx context.Context) error {
	rows, e := s.DB.QueryContext(ctx, `SELECT o.name,n.project,n.name FROM stacks s JOIN organizations o ON o.id=s.org_id JOIN stack_names n ON n.stack_id=s.id AND n.is_current WHERE s.active_update_id IS NOT NULL`)
	if e != nil {
		return e
	}
	type path struct{ o, p, n string }
	paths := []path{}
	for rows.Next() {
		var p path
		if e = rows.Scan(&p.o, &p.p, &p.n); e != nil {
			rows.Close()
			return e
		}
		paths = append(paths, p)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, p := range paths {
		if e = s.Tx(ctx, func(tx *sql.Tx) error {
			st, e := s.Stack(ctx, tx, p.o, p.p, p.n, true)
			if e != nil {
				return e
			}
			return s.ExpireActive(ctx, tx, &st)
		}); e != nil {
			return e
		}
	}
	return nil
}
