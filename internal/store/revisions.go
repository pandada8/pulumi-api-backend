package store

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/pandada8/pulumi-api-backend/internal/core"
	"github.com/pandada8/pulumi-api-backend/internal/pulumicompat"
)

type Revision struct {
	Version  int64     `json:"version"`
	ID       string    `json:"id"`
	Parent   string    `json:"parent,omitempty"`
	UpdateID string    `json:"updateID,omitempty"`
	Created  time.Time `json:"createdAt"`
}

// RevisionState always resolves a fixed representation, never the mutable stack head.
func (s *Store) RevisionState(ctx context.Context, tx *sql.Tx, stack, id string) ([]byte, bool, error) {
	var raw, hash []byte
	var invalid bool
	var journal sql.NullString
	var upto int64
	e := tx.QueryRowContext(ctx, `SELECT p.raw_bytes,p.sha256,p.is_invalid,r.journal_update_id,r.journal_upto FROM state_revisions r JOIN snapshots p ON p.id=r.snapshot_id WHERE r.stack_id=$1 AND r.id=$2`, stack, id).Scan(&raw, &hash, &invalid, &journal, &upto)
	if e != nil {
		return nil, false, Translate(e)
	}
	if !hmac.Equal(hash, core.Digest(raw)) {
		return nil, false, Fail(500, "Snapshot integrity failure")
	}
	if journal.Valid {
		raw, e = s.Replay(ctx, tx, journal.String, raw, upto)
	}
	return raw, invalid, e
}

func (s *Store) RevisionTree(ctx context.Context, tx *sql.Tx, st Stack, offset int) (map[string]any, error) {
	if offset < 0 {
		return nil, Fail(400, "Invalid offset")
	}
	var head string
	var epoch int64
	if e := tx.QueryRowContext(ctx, `SELECT current_revision,activation_epoch FROM stacks WHERE id=$1`, st.ID).Scan(&head, &epoch); e != nil {
		return nil, e
	}
	rows, e := tx.QueryContext(ctx, `SELECT r.id,COALESCE(r.parent_id::text,''),COALESCE(r.update_id::text,''),r.created_at,COALESCE(u.version,0) FROM state_revisions r LEFT JOIN updates u ON u.id=r.update_id WHERE r.stack_id=$1 ORDER BY r.created_at,r.id LIMIT 101 OFFSET $2`, st.ID, offset)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	nodes := []Revision{}
	for rows.Next() {
		var r Revision
		if e = rows.Scan(&r.ID, &r.Parent, &r.UpdateID, &r.Created, &r.Version); e != nil {
			return nil, e
		}
		nodes = append(nodes, r)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	more := len(nodes) > 100
	if more {
		nodes = nodes[:100]
	}
	return map[string]any{"head": head, "activationEpoch": epoch, "activeUpdate": st.Active, "nodes": nodes, "hasMore": more, "nextOffset": offset + len(nodes)}, nil
}

// publishRevision is called while holding the stack lock, in the state write transaction.
func (s *Store) publishRevision(ctx context.Context, tx *sql.Tx, stack, id, parent, snapshot, journal string, upto int64) error {
	var p, j any
	if parent != "" {
		p = parent
	}
	if journal != "" {
		j = journal
	}
	_, e := tx.ExecContext(ctx, `INSERT INTO state_revisions(id,stack_id,parent_id,update_id,snapshot_id,journal_update_id,journal_upto) VALUES($1,$2,$3,$1,$4,$5,$6)`, id, stack, p, snapshot, j, upto)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `UPDATE stacks SET current_revision=$2 WHERE id=$1`, stack, id)
	return e
}

type ActivateRequest struct {
	Target        string `json:"target"`
	ExpectedHead  string `json:"expectedHead"`
	ExpectedEpoch int64  `json:"expectedEpoch"`
	RequestID     string `json:"requestID"`
	Reason        string `json:"reason"`
}

func (s *Store) Activate(ctx context.Context, tx *sql.Tx, a Actor, st Stack, req ActivateRequest) (map[string]any, error) {
	for _, v := range []string{req.Target, req.ExpectedHead, req.RequestID} {
		if _, e := uuid.Parse(v); e != nil {
			return nil, Fail(400, "Revision and request IDs must be UUIDs")
		}
	}
	if req.ExpectedEpoch < 0 || strings.TrimSpace(req.Reason) == "" || len(req.Reason) > 2000 {
		return nil, Fail(400, "Reason required (max 2000 bytes) and epoch must be nonnegative")
	}
	if e := s.Permission(ctx, tx, a, st.OrgID, true, true); e != nil {
		return nil, e
	}
	// A retry returns the original result; it never activates again after later writes.
	var actor, from, target, reason, id string
	var epoch, version int64
	e := tx.QueryRowContext(ctx, `SELECT x.actor_token_id,x.from_revision,x.target_revision,x.expected_epoch,x.reason,x.update_id,u.version FROM state_activations x JOIN updates u ON u.id=x.update_id WHERE x.stack_id=$1 AND x.request_id=$2`, st.ID, req.RequestID).Scan(&actor, &from, &target, &epoch, &reason, &id, &version)
	if e == nil {
		if actor != a.TokenID || from != req.ExpectedHead || target != req.Target || epoch != req.ExpectedEpoch || reason != req.Reason {
			return nil, Fail(409, "Request ID already used with different arguments")
		}
		return map[string]any{"updateID": id, "version": version, "activatedRevision": target, "activationEpoch": epoch + 1, "replayed": true}, nil
	}
	if e != sql.ErrNoRows {
		return nil, e
	}
	if st.Active != "" {
		return nil, Fail(409, "Finish the active update before activation")
	}
	if e = tx.QueryRowContext(ctx, `SELECT current_revision,activation_epoch FROM stacks WHERE id=$1`, st.ID).Scan(&from, &epoch); e != nil {
		return nil, e
	}
	if from != req.ExpectedHead || epoch != req.ExpectedEpoch {
		return nil, Fail(409, "Head changed; reload history")
	}
	if from == req.Target {
		return nil, Fail(409, "Revision is already current")
	}
	raw, invalid, e := s.RevisionState(ctx, tx, st.ID, req.Target)
	if e != nil {
		return nil, e
	}
	if invalid {
		return nil, Fail(422, "Invalid checkpoint cannot be activated")
	}
	if e = pulumicompat.Validate(raw); e != nil {
		return nil, Fail(422, "Invalid historical state")
	}
	var dep struct {
		Deployment struct {
			Resources []struct {
				URN string `json:"urn"`
			} `json:"resources"`
			Pending []json.RawMessage `json:"pending_operations"`
		} `json:"deployment"`
	}
	if e = json.Unmarshal(raw, &dep); e != nil {
		return nil, e
	}
	if len(dep.Deployment.Pending) > 0 {
		return nil, Fail(422, "Pending operations require explicit repair, not activation")
	}
	for _, r := range dep.Deployment.Resources {
		if !strings.HasPrefix(r.URN, "urn:pulumi:"+st.Name+"::"+st.Project+"::") {
			return nil, Fail(422, "Historical state uses a different stack/project name")
		}
	}
	if e = s.ValidateServiceSecrets(ctx, tx, st, raw); e != nil {
		return nil, e
	}
	// Reuse the original full snapshot; only journal targets need materialization.
	var snapshot string
	var j sql.NullString
	if e = tx.QueryRowContext(ctx, `SELECT snapshot_id,journal_update_id FROM state_revisions WHERE stack_id=$1 AND id=$2`, st.ID, req.Target).Scan(&snapshot, &j); e != nil {
		return nil, e
	}
	if j.Valid {
		snapshot, e = s.InsertSnapshot(ctx, tx, st.ID, raw, false)
		if e != nil {
			return nil, e
		}
	}
	var before string
	var beforeJournal sql.NullString
	if e = tx.QueryRowContext(ctx, `SELECT snapshot_id,journal_update_id FROM stack_heads WHERE stack_id=$1`, st.ID).Scan(&before, &beforeJournal); e != nil {
		return nil, e
	}
	if beforeJournal.Valid {
		old, _, err := s.Head(ctx, tx, st.ID)
		if err != nil {
			return nil, err
		}
		before, e = s.InsertSnapshot(ctx, tx, st.ID, old, false)
		if e != nil {
			return nil, e
		}
	}
	// Preserve latest config metadata explicitly; activation only changes state, not config/tags.
	request := []byte(`{"config":{},"metadata":{}}`)
	e = tx.QueryRowContext(ctx, `SELECT request FROM updates WHERE stack_id=$1 AND NOT dry_run AND status IN ('succeeded','failed','cancelled') ORDER BY version DESC LIMIT 1`, st.ID).Scan(&request)
	if e != nil && e != sql.ErrNoRows {
		return nil, e
	}
	var meta map[string]any
	if e = json.Unmarshal(request, &meta); e != nil {
		return nil, e
	}
	md, _ := meta["metadata"].(map[string]any)
	if md == nil {
		md = map[string]any{}
	}
	md["message"] = "Activate revision " + req.Target + ": " + req.Reason
	meta["metadata"] = md
	request, e = json.Marshal(meta)
	if e != nil {
		return nil, e
	}
	id = uuid.NewString()
	version = st.Version + 1
	_, e = tx.ExecContext(ctx, `INSERT INTO updates(id,stack_id,actor_token_id,kind,dry_run,status,mode,request,base_snapshot_id,final_snapshot_id,base_revision,version,started_at,ended_at,event_closed_at) VALUES($1,$2,$3,'import',false,'succeeded','none',$4,$5,$6,$7,$8,clock_timestamp(),clock_timestamp(),clock_timestamp())`, id, st.ID, a.TokenID, request, before, snapshot, from, version)
	if e != nil {
		return nil, e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO state_activations(stack_id,request_id,actor_token_id,from_revision,target_revision,expected_epoch,update_id,reason) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, st.ID, req.RequestID, a.TokenID, from, req.Target, epoch, id, req.Reason)
	if e != nil {
		return nil, e
	}
	if e = s.SetHead(ctx, tx, st.ID, snapshot); e != nil {
		return nil, e
	}
	_, e = tx.ExecContext(ctx, `UPDATE stacks SET current_revision=$2,last_version=$3,activation_epoch=activation_epoch+1,fence=fence+1 WHERE id=$1`, st.ID, req.Target, version)
	if e != nil {
		return nil, e
	}
	return map[string]any{"updateID": id, "version": version, "activatedRevision": req.Target, "activationEpoch": epoch + 1, "replayed": false}, nil
}
