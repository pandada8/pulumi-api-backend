package store

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/pandada8/pulumid/internal/core"
	"github.com/pandada8/pulumid/internal/pulumicompat"
	"github.com/pulumi/pulumi/pkg/v3/backend"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
)

// replay validates references before calling upstream code, which assumes trusted engine inputs.
func replay(raw []byte, entries []apitype.JournalEntry) (out []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("journal replayer panic: %v", r)
		}
	}()
	var u apitype.UntypedDeployment
	if err = json.Unmarshal(raw, &u); err != nil {
		return
	}
	var base apitype.DeploymentV3
	if err = json.Unmarshal(u.Deployment, &base); err != nil {
		return
	}
	r := backend.NewJournalReplayer(&base)
	validator := newValidator(len(base.Resources))
	for _, entry := range entries {
		if err = validator.add(entry); err != nil {
			return nil, err
		}
		if err = r.Add(entry); err != nil {
			return nil, err
		}
	}
	dep, err := r.GenerateDeployment()
	if err != nil {
		return nil, err
	}
	if dep.Version != 3 || len(dep.Features) > 0 {
		return nil, Fail(422, "Journal produced unsupported features")
	}
	b, err := json.Marshal(dep.Deployment)
	if err != nil {
		return nil, err
	}
	out, err = json.Marshal(apitype.UntypedDeployment{Version: 3, Deployment: b})
	if err == nil {
		err = pulumicompat.Validate(out)
	}
	return out, err
}
func (s *Store) journal(ctx context.Context, tx *sql.Tx, id string, upto int64) ([]apitype.JournalEntry, error) {
	rows, e := tx.QueryContext(ctx, `SELECT payload,digest FROM journal_entries WHERE update_id=$1 AND ingest_order<=$2 ORDER BY ingest_order`, id, upto)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []apitype.JournalEntry{}
	for rows.Next() {
		var b, h []byte
		if e = rows.Scan(&b, &h); e != nil {
			return nil, e
		}
		if !hmac.Equal(h, core.Digest(b)) {
			return nil, fmt.Errorf("journal integrity failure")
		}
		var entry apitype.JournalEntry
		if e = json.Unmarshal(b, &entry); e != nil {
			return nil, e
		}
		out = append(out, entry)
	}
	return out, rows.Err()
}
func (s *Store) Replay(ctx context.Context, tx *sql.Tx, id string, raw []byte, upto int64) ([]byte, error) {
	entries, e := s.journal(ctx, tx, id, upto)
	if e != nil {
		return nil, e
	}
	return replay(raw, entries)
}
func (s *Store) AppendJournal(ctx context.Context, tx *sql.Tx, st Stack, u Update, token string, entries []json.RawMessage) error {
	if e := s.Lease(ctx, tx, st, u, token); e != nil {
		return e
	}
	if u.Mode != "journal" {
		return Fail(409, "Journal was not negotiated")
	}
	if len(entries) > 1000 {
		return Fail(413, "Too many journal entries")
	}
	var e error
	type item struct {
		b, h  []byte
		entry apitype.JournalEntry
	}
	newItems := []item{}
	seen := map[int64][]byte{}
	for _, b := range entries {
		if e = pulumicompat.ValidateJournal(b); e != nil {
			return Fail(422, e.Error())
		}
		var generic any
		if e = pulumicompat.Decode(b, &generic); e != nil {
			return Fail(400, "Invalid JSON")
		}
		canonical, _ := json.Marshal(generic)
		var entry apitype.JournalEntry
		if e = json.Unmarshal(canonical, &entry); e != nil {
			return Fail(400, "Invalid journal")
		}
		hash := core.Digest(canonical)
		if old, ok := seen[entry.SequenceID]; ok {
			if !hmac.Equal(old, hash) {
				return Fail(409, "Conflicting journal sequence")
			}
			continue
		}
		seen[entry.SequenceID] = hash
		var old []byte
		e = tx.QueryRowContext(ctx, `SELECT digest FROM journal_entries WHERE update_id=$1 AND sequence_id=$2`, u.ID, entry.SequenceID).Scan(&old)
		if e == nil {
			if !hmac.Equal(old, hash) {
				return Fail(409, "Conflicting journal sequence")
			}
			continue
		}
		if e != sql.ErrNoRows {
			return e
		}
		newItems = append(newItems, item{canonical, hash, entry})
	}
	if len(newItems) == 0 {
		return nil
	}
	cacheAny, _ := s.validators.LoadOrStore(u.ID, newValidator(0))
	cache := cacheAny.(*journalValidator)
	cache.mu.Lock()
	s.afterFinish(tx, func(committed bool) {
		if !committed {
			cache.count = -1
		}
		cache.mu.Unlock()
	})
	if cache.count != u.JournalCount {
		var raw []byte
		if e = tx.QueryRowContext(ctx, `SELECT raw_bytes FROM snapshots WHERE id=$1`, u.Base).Scan(&raw); e != nil {
			return e
		}
		var envelope apitype.UntypedDeployment
		var base apitype.DeploymentV3
		if e = json.Unmarshal(raw, &envelope); e != nil {
			return e
		}
		if e = json.Unmarshal(envelope.Deployment, &base); e != nil {
			return e
		}
		v := newValidator(len(base.Resources))
		existing, e := s.journal(ctx, tx, u.ID, u.JournalCount)
		if e != nil {
			return e
		}
		for _, entry := range existing {
			if e = v.add(entry); e != nil {
				return e
			}
		}
		cache.baseLen = v.baseLen
		cache.produced = v.produced
		cache.begun = v.begun
		cache.terminal = v.terminal
		cache.count = u.JournalCount
	}
	for _, i := range newItems {
		if e = cache.add(i.entry); e != nil {
			return e
		}
	}
	count := u.JournalCount
	for _, i := range newItems {
		count++
		_, e = tx.ExecContext(ctx, `INSERT INTO journal_entries(update_id,sequence_id,ingest_order,operation_id,payload,digest) VALUES($1,$2,$3,$4,$5,$6)`, u.ID, i.entry.SequenceID, count, i.entry.OperationID, i.b, i.h)
		if e != nil {
			return e
		}
	}
	_, e = tx.ExecContext(ctx, `UPDATE updates SET journal_count=$2 WHERE id=$1`, u.ID, count)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `UPDATE stack_heads SET snapshot_id=$2,journal_update_id=$3,journal_upto=$4,generation=generation+1 WHERE stack_id=$1`, st.ID, u.Base, u.ID, count)
	if e == nil {
		cache.count = count
	}
	return e
}
