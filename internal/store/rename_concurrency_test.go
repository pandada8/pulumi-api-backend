package store

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

// Run against an isolated stack in the integration harness. The waiting lookup
// must not reuse the name mapping from its pre-lock statement snapshot.
func TestRenameWaitingLookup(t *testing.T) {
	dsn, id := os.Getenv("BACKEND_DATABASE_URL"), os.Getenv("TEST_RENAME_STACK_ID")
	if id == "" {
		t.Skip("integration database required")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var org, project, name string
	if err = db.QueryRowContext(ctx, `SELECT o.name,n.project,n.name FROM stacks s JOIN organizations o ON o.id=s.org_id JOIN stack_names n ON n.stack_id=s.id WHERE s.id=$1 AND n.is_current`, id).Scan(&org, &project, &name); err != nil {
		t.Fatal(err)
	}
	writer, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback()
	if _, err = writer.ExecContext(ctx, `SELECT id FROM stacks WHERE id=$1 FOR UPDATE`, id); err != nil {
		t.Fatal(err)
	}
	reader, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Rollback()
	var pid int
	reader.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&pid)
	result := make(chan error, 1)
	go func() {
		st, e := (&Store{}).Stack(ctx, reader, org, project, name, true)
		if e == nil && st.Current {
			e = Fail(500, "waiting lookup accepted renamed path")
		}
		result <- e
	}()
	for {
		var blocked bool
		if err = db.QueryRowContext(ctx, `SELECT COALESCE(wait_event_type='Lock',false) FROM pg_stat_activity WHERE pid=$1`, pid).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("lookup never blocked")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if _, err = writer.ExecContext(ctx, `UPDATE stack_names SET is_current=false WHERE stack_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err = writer.ExecContext(ctx, `INSERT INTO stack_names(org_id,project,name,stack_id,is_current) SELECT org_id,$2,$3,id,true FROM stacks WHERE id=$1`, id, project, name+"renamed"); err != nil {
		t.Fatal(err)
	}
	if err = writer.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = <-result; err != nil {
		t.Fatal(err)
	}
}
