package store

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"
)

// A locked snapshot table must not block listing summarized heads. This guards
// against reintroducing full-state reads for every row on the console homepage.
func TestListingDoesNotReadSnapshots(t *testing.T) {
	id := os.Getenv("TEST_RENAME_STACK_ID")
	if id == "" {
		t.Skip("integration database required")
	}
	db, err := sql.Open("postgres", os.Getenv("BACKEND_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var principal string
	if err = db.QueryRowContext(ctx, `SELECT m.principal_id FROM stacks s JOIN memberships m ON m.org_id=s.org_id WHERE s.id=$1 LIMIT 1`, id).Scan(&principal); err != nil {
		t.Fatal(err)
	}
	lock, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback()
	if _, err = lock.ExecContext(ctx, `LOCK TABLE snapshots IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	items, err := (&Store{DB: db}).ListStacks(ctx, tx, Actor{PrincipalID: principal}, "", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range items {
		if item["id"] == id {
			found = true
		}
	}
	if !found {
		t.Fatal("fixture stack missing from list")
	}
}
