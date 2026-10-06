package store

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/pandada8/pulumid/internal/core"
	"github.com/pandada8/pulumid/internal/pulumicompat"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
)

type Stack struct {
	ID, OrgID, Org, Project, Name, Active string
	Version, Fence                        int64
	Tags                                  json.RawMessage
	Current                               bool
}

func (s *Store) Stack(ctx context.Context, tx *sql.Tx, org, project, name string, lock bool) (Stack, error) {
	var st Stack
	q := `SELECT s.id,s.org_id,o.name,n.project,n.name,COALESCE(s.active_update_id::text,''),s.last_version,s.fence,s.tags,n.is_current FROM stacks s JOIN stack_names n ON n.stack_id=s.id JOIN organizations o ON o.id=s.org_id WHERE o.name=$1 AND n.project=$2 AND n.name=$3 AND s.deleted_at IS NULL`
	if lock {
		q += ` FOR UPDATE OF s`
	}
	e := tx.QueryRowContext(ctx, q, org, project, name).Scan(&st.ID, &st.OrgID, &st.Org, &st.Project, &st.Name, &st.Active, &st.Version, &st.Fence, &st.Tags, &st.Current)
	if e == nil && lock {
		// The join may have been evaluated before waiting for the stack lock.
		// A second READ COMMITTED statement observes any committed rename.
		return s.Stack(ctx, tx, org, project, name, false)
	}
	return st, Translate(e)
}
func (s *Store) ExpireActive(ctx context.Context, tx *sql.Tx, st *Stack) error {
	if st.Active == "" {
		return nil
	}
	u, e := s.Update(ctx, tx, st.ID, st.Active)
	if e != nil {
		return e
	}
	var generation string
	if e = tx.QueryRowContext(ctx, `SELECT generation FROM service_meta WHERE singleton`).Scan(&generation); e != nil {
		return e
	}
	var valid bool
	e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM api_tokens t JOIN principals p ON p.id=t.principal_id JOIN memberships m ON m.principal_id=p.id WHERE t.id=$1 AND m.org_id=$2 AND t.revoked_at IS NULL AND (t.expires_at IS NULL OR t.expires_at>clock_timestamp()) AND NOT p.disabled AND t.can_write AND m.role!='reader')`, u.Actor, st.OrgID).Scan(&valid)
	if e != nil {
		return e
	}
	now, e := dbNow(ctx, tx)
	if e != nil {
		return e
	}
	if u.Status == "running" && (u.Expires.Before(now) || u.Generation != generation || !valid) {
		if e = s.finish(ctx, tx, st, &u, "failed", "lease_expired", false); e != nil {
			return e
		}
		st.Active = ""
		st.Fence++
	}
	return nil
}
func (s *Store) InsertSnapshot(ctx context.Context, tx *sql.Tx, id string, raw []byte, invalid bool) (string, error) {
	sid := uuid.NewString()
	_, e := tx.ExecContext(ctx, `INSERT INTO snapshots(id,stack_id,schema_version,raw_bytes,sha256,is_invalid) VALUES($1,$2,3,$3,$4,$5)`, sid, id, raw, core.Digest(raw), invalid)
	return sid, e
}
func (s *Store) SetHead(ctx context.Context, tx *sql.Tx, id, snapshot string) error {
	_, e := tx.ExecContext(ctx, `INSERT INTO stack_heads(stack_id,snapshot_id) VALUES($1,$2) ON CONFLICT(stack_id) DO UPDATE SET snapshot_id=$2,journal_update_id=NULL,journal_upto=0,generation=stack_heads.generation+1`, id, snapshot)
	return e
}
func (s *Store) Head(ctx context.Context, tx *sql.Tx, id string) ([]byte, bool, error) {
	var raw, hash []byte
	var invalid bool
	var journal sql.NullString
	var upto int64
	e := tx.QueryRowContext(ctx, `SELECT p.raw_bytes,p.sha256,p.is_invalid,h.journal_update_id,h.journal_upto FROM stack_heads h JOIN snapshots p ON p.id=h.snapshot_id WHERE h.stack_id=$1`, id).Scan(&raw, &hash, &invalid, &journal, &upto)
	if e != nil {
		return nil, false, e
	}
	if !hmac.Equal(hash, core.Digest(raw)) {
		return nil, false, fmt.Errorf("snapshot integrity failure")
	}
	if journal.Valid {
		raw, e = s.Replay(ctx, tx, journal.String, raw, upto)
	}
	return raw, invalid, e
}
func (s *Store) CreateStack(ctx context.Context, tx *sql.Tx, a Actor, org, project string, req apitype.CreateStackRequest, rawState []byte) error {
	if e := pulumicompat.Names(project, req.StackName); e != nil {
		return Fail(400, e.Error())
	}
	if len(req.Teams) > 0 {
		return Fail(422, "teams are unsupported")
	}
	var oid string
	if e := tx.QueryRowContext(ctx, `SELECT id FROM organizations WHERE name=$1`, org).Scan(&oid); e != nil {
		return Translate(e)
	}
	if e := s.Permission(ctx, tx, a, oid, true, false); e != nil {
		return e
	}
	raw := pulumicompat.Empty()
	if req.State != nil {
		var e error
		raw = append([]byte(nil), rawState...)
		if e != nil {
			return e
		}
		if e = pulumicompat.Validate(raw); e != nil {
			return Fail(422, e.Error())
		}
	}
	id := uuid.NewString()
	tags, _ := json.Marshal(req.Tags)
	if req.Tags == nil {
		tags = []byte("{}")
	}
	_, e := tx.ExecContext(ctx, `INSERT INTO stacks(id,org_id,tags) VALUES($1,$2,$3)`, id, oid, tags)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO stack_names(org_id,project,name,stack_id,is_current) VALUES($1,$2,$3,$4,true)`, oid, project, req.StackName, id)
	if e != nil {
		return e
	}
	sid, e := s.InsertSnapshot(ctx, tx, id, raw, false)
	if e != nil {
		return e
	}
	if e = s.SetHead(ctx, tx, id, sid); e != nil {
		return e
	}
	wrapped, e := core.Seal(core.Purpose(s.Config.Master, "wrap"), core.Random(), wrapAAD(id, 1))
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO stack_keys(stack_id,version,kek_id,wrapped_key,active) VALUES($1,1,'file-v1',$2,true)`, id, wrapped)
	if e != nil {
		return e
	}
	return s.ValidateServiceSecrets(ctx, tx, Stack{ID: id, OrgID: oid, Org: org, Project: project, Name: req.StackName}, raw)
}
func (s *Store) ListStacks(ctx context.Context, tx *sql.Tx, a Actor, org, project, tag, value string) ([]map[string]any, error) {
	rows, e := tx.QueryContext(ctx, `SELECT s.id,o.name,n.project,n.name FROM stacks s JOIN stack_names n ON n.stack_id=s.id AND n.is_current JOIN organizations o ON o.id=s.org_id JOIN memberships m ON m.org_id=o.id WHERE m.principal_id=$1 AND s.deleted_at IS NULL AND ($2='' OR o.name=$2) AND ($3='' OR n.project=$3) AND ($4='' OR s.tags->>$4=$5) ORDER BY s.id`, a.PrincipalID, org, project, tag, value)
	if e != nil {
		return nil, e
	}
	type item struct{ id, org, project, name string }
	items := []item{}
	for rows.Next() {
		var i item
		if e = rows.Scan(&i.id, &i.org, &i.project, &i.name); e != nil {
			rows.Close()
			return nil, e
		}
		items = append(items, i)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	out := []map[string]any{}
	for _, i := range items {
		raw, _, e := s.Head(ctx, tx, i.id)
		if e != nil {
			return nil, e
		}
		out = append(out, map[string]any{"id": i.id, "orgName": i.org, "projectName": i.project, "stackName": i.name, "resourceCount": pulumicompat.ResourceCount(raw), "links": map[string]string{"self": s.Config.ConsoleURL + "/" + i.org + "/" + i.project + "/" + i.name}})
	}
	return out, nil
}
func (s *Store) DeleteStack(ctx context.Context, tx *sql.Tx, st Stack, force bool) error {
	if st.Active != "" {
		return Fail(409, "Update in progress")
	}
	raw, _, e := s.Head(ctx, tx, st.ID)
	if e != nil {
		return e
	}
	if !force && pulumicompat.ContainsResources(raw) {
		return Fail(400, "Bad Request: Stack still contains resources.")
	}
	_, e = tx.ExecContext(ctx, `DELETE FROM stack_names WHERE stack_id=$1`, st.ID)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `UPDATE stacks SET deleted_at=clock_timestamp() WHERE id=$1`, st.ID)
	return e
}
func (s *Store) Import(ctx context.Context, tx *sql.Tx, a Actor, st Stack, raw []byte, kind string) (string, error) {
	if st.Active != "" {
		return "", Fail(409, "Update in progress")
	}
	if e := pulumicompat.Validate(raw); e != nil {
		return "", Fail(422, e.Error())
	}
	if e := s.ValidateServiceSecrets(ctx, tx, st, raw); e != nil {
		return "", e
	}
	sid, e := s.InsertSnapshot(ctx, tx, st.ID, raw, false)
	if e != nil {
		return "", e
	}
	id := uuid.NewString()
	request := []byte(`{"config":{},"metadata":{}}`)
	_ = tx.QueryRowContext(ctx, `SELECT request FROM updates WHERE stack_id=$1 AND status IN('succeeded','failed','cancelled') AND NOT dry_run ORDER BY version DESC LIMIT 1`, st.ID).Scan(&request)
	_, e = tx.ExecContext(ctx, `INSERT INTO updates(id,stack_id,actor_token_id,kind,dry_run,status,mode,request,final_snapshot_id,version,started_at,ended_at,event_closed_at) VALUES($1,$2,$3,$4,false,'succeeded','none',$5,$6,$7,clock_timestamp(),clock_timestamp(),clock_timestamp())`, id, st.ID, a.TokenID, kind, request, sid, st.Version+1)
	if e != nil {
		return "", e
	}
	_, e = tx.ExecContext(ctx, `UPDATE stacks SET last_version=last_version+1 WHERE id=$1`, st.ID)
	if e != nil {
		return "", e
	}
	e = s.SetHead(ctx, tx, st.ID, sid)
	return id, e
}
func (s *Store) Rename(ctx context.Context, tx *sql.Tx, a Actor, st Stack, project, name string) error {
	if project == "" {
		project = st.Project
	}
	if name == "" {
		name = st.Name
	}
	if project == st.Project && name == st.Name {
		return Fail(409, "Name unchanged")
	}
	if e := pulumicompat.Names(project, name); e != nil {
		return Fail(400, e.Error())
	}
	raw, _, e := s.Head(ctx, tx, st.ID)
	if e != nil {
		return e
	}
	raw, e = pulumicompat.Rename(raw, st.Org, st.Project, st.Name, project, name, s.Config.PublicURL)
	if e != nil {
		return e
	}
	if _, e = s.Import(ctx, tx, a, st, raw, "rename"); e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `UPDATE stack_names SET is_current=false WHERE stack_id=$1`, st.ID)
	if e != nil {
		return e
	}
	var existing string
	e = tx.QueryRowContext(ctx, `SELECT stack_id FROM stack_names WHERE org_id=$1 AND project=$2 AND name=$3`, st.OrgID, project, name).Scan(&existing)
	if e == nil && existing != st.ID {
		return Fail(409, "Name reserved")
	}
	if e != nil && e != sql.ErrNoRows {
		return e
	}
	var assigned string
	e = tx.QueryRowContext(ctx, `INSERT INTO stack_names(org_id,project,name,stack_id,is_current) VALUES($1,$2,$3,$4,true) ON CONFLICT(org_id,project,name) DO UPDATE SET is_current=true WHERE stack_names.stack_id=EXCLUDED.stack_id RETURNING stack_id`, st.OrgID, project, name, st.ID).Scan(&assigned)
	if e == sql.ErrNoRows {
		return Fail(409, "Name reserved")
	}
	return e
}
func (s *Store) Crypt(ctx context.Context, tx *sql.Tx, st Stack, raw []byte, decrypt bool) ([]byte, error) {
	v := uint32(1)
	var e error
	if decrypt {
		v, e = core.CipherVersion(raw)
		if e != nil {
			return nil, Fail(400, "Invalid ciphertext")
		}
	}
	var wrapped []byte
	if decrypt {
		e = tx.QueryRowContext(ctx, `SELECT wrapped_key FROM stack_keys WHERE stack_id=$1 AND version=$2`, st.ID, v).Scan(&wrapped)
	} else {
		e = tx.QueryRowContext(ctx, `SELECT version,wrapped_key FROM stack_keys WHERE stack_id=$1 AND active`, st.ID).Scan(&v, &wrapped)
	}
	if e != nil {
		return nil, Fail(400, "Invalid ciphertext")
	}
	key, e := core.Open(core.Purpose(s.Config.Master, "wrap"), wrapped, wrapAAD(st.ID, int(v)))
	if e != nil {
		return nil, e
	}
	defer clear(key)
	if decrypt {
		out, e := core.Decrypt(key, st.ID, raw)
		if e != nil {
			return nil, Fail(400, "Invalid ciphertext")
		}
		return out, nil
	}
	return core.Encrypt(key, st.ID, v, raw)
}
func (s *Store) Tags(ctx context.Context, tx *sql.Tx, st Stack, tags map[apitype.StackTagName]string) error {
	if st.Active != "" {
		return Fail(409, "Update in progress")
	}
	raw, _ := json.Marshal(tags)
	if tags == nil {
		raw = []byte("{}")
	}
	_, e := tx.ExecContext(ctx, `UPDATE stacks SET tags=$2 WHERE id=$1`, st.ID, raw)
	return e
}

// ValidateServiceSecrets authenticates imported service ciphertexts against the stable stack ID.
func (s *Store) ValidateServiceSecrets(ctx context.Context, tx *sql.Tx, st Stack, raw []byte) error {
	var envelope map[string]any
	if e := pulumicompat.Decode(raw, &envelope); e != nil {
		return Fail(400, "Invalid deployment")
	}
	dep, ok := envelope["deployment"].(map[string]any)
	if !ok {
		return Fail(400, "Invalid deployment")
	}
	provider, ok := dep["secrets_providers"].(map[string]any)
	if !ok || provider["type"] != "service" {
		return nil
	}
	var walk func(any) error
	walk = func(value any) error {
		switch v := value.(type) {
		case map[string]any:
			if v["4dabf18193072939515e22adb298388d"] == "1b47061264138c4ac30d75fd1eb44270" {
				text, ok := v["ciphertext"].(string)
				if !ok {
					return Fail(422, "Service secret must contain ciphertext")
				}
				b, e := base64.StdEncoding.DecodeString(text)
				if e != nil {
					return Fail(422, "Invalid service ciphertext")
				}
				plain, e := s.Crypt(ctx, tx, st, b, true)
				clear(plain)
				if e != nil {
					return Fail(422, "Service ciphertext belongs to another stack or key")
				}
				return nil
			}
			for _, x := range v {
				if e := walk(x); e != nil {
					return e
				}
			}
		case []any:
			for _, x := range v {
				if e := walk(x); e != nil {
					return e
				}
			}
		}
		return nil
	}
	return walk(dep)
}
