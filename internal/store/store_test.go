package store_test

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"acl/internal/receipt"
	"acl/internal/store"
)

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func makeReceipt(intent, status string) *receipt.Receipt {
	return &receipt.Receipt{
		ACLVersion: "0.1.0",
		Intent:     intent,
		Status:     status,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
		Agents: []receipt.AgentTrace{
			{
				Name:   "TestAgent",
				Status: status,
				Steps: []receipt.StepTrace{
					{Name: "step1", DurationMS: 42},
				},
			},
		},
	}
}

func TestPutAndGet(t *testing.T) {
	st := openTestStore(t)
	r := makeReceipt("test intent", "success")

	id, err := st.Put(r)
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if id <= 0 {
		t.Errorf("expected positive ID, got %d", id)
	}

	got, err := st.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Intent != r.Intent {
		t.Errorf("intent: got %q, want %q", got.Intent, r.Intent)
	}
	if got.Status != r.Status {
		t.Errorf("status: got %q, want %q", got.Status, r.Status)
	}
	if got.ACLVersion != r.ACLVersion {
		t.Errorf("acl_version: got %q, want %q", got.ACLVersion, r.ACLVersion)
	}
}

func TestPutMultipleIncrements(t *testing.T) {
	st := openTestStore(t)

	id1, _ := st.Put(makeReceipt("first", "success"))
	id2, _ := st.Put(makeReceipt("second", "failed"))

	if id2 <= id1 {
		t.Errorf("expected id2 > id1, got id1=%d id2=%d", id1, id2)
	}
}

func TestList(t *testing.T) {
	st := openTestStore(t)

	for i := 0; i < 5; i++ {
		if _, err := st.Put(makeReceipt(fmt.Sprintf("intent %d", i), "success")); err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
	}

	rows, err := st.List(3)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 3 {
		t.Errorf("expected 3 rows, got %d", len(rows))
	}
	// Newest first (highest ID first).
	if rows[0].ID <= rows[len(rows)-1].ID {
		t.Errorf("expected descending IDs: first=%d last=%d", rows[0].ID, rows[len(rows)-1].ID)
	}
}

func TestListDefault(t *testing.T) {
	st := openTestStore(t)
	// n<=0 should default to 20
	rows, err := st.List(0)
	if err != nil {
		t.Fatalf("List(0): %v", err)
	}
	if rows == nil {
		rows = []store.RunSummary{} // nil is fine for empty
	}
	// Just check it doesn't error on empty store.
	_ = rows
}

func TestCount(t *testing.T) {
	st := openTestStore(t)

	n, err := st.Count()
	if err != nil {
		t.Fatalf("Count (empty): %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0, got %d", n)
	}

	st.Put(makeReceipt("a", "success"))
	st.Put(makeReceipt("b", "failed"))

	n, err = st.Count()
	if err != nil {
		t.Fatalf("Count (2): %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2, got %d", n)
	}
}

func TestGetNotFound(t *testing.T) {
	st := openTestStore(t)
	_, err := st.Get(99999)
	if err == nil {
		t.Error("expected error for missing run ID")
	}
}

func TestListSummaryFields(t *testing.T) {
	st := openTestStore(t)
	st.Put(makeReceipt("list check", "success"))

	rows, err := st.List(1)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("expected 1 row")
	}
	row := rows[0]
	if row.ID <= 0 {
		t.Errorf("ID: %d", row.ID)
	}
	if row.Intent != "list check" {
		t.Errorf("Intent: %q", row.Intent)
	}
	if row.Status != "success" {
		t.Errorf("Status: %q", row.Status)
	}
	if row.Timestamp == "" {
		t.Error("Timestamp empty")
	}
	if row.DurationMS < 0 {
		t.Errorf("DurationMS: %d", row.DurationMS)
	}
}

func TestPurge(t *testing.T) {
	st := openTestStore(t)

	// Old receipt: 30 days ago.
	old := makeReceipt("old run", "success")
	old.Timestamp = time.Now().UTC().AddDate(0, 0, -30).Format(time.RFC3339)
	if _, err := st.Put(old); err != nil {
		t.Fatalf("Put old: %v", err)
	}

	// Recent receipt: now.
	if _, err := st.Put(makeReceipt("new run", "success")); err != nil {
		t.Fatalf("Put new: %v", err)
	}

	// Purge anything older than 7 days — should remove the old record.
	deleted, err := st.Purge(7)
	if err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if deleted != 1 {
		t.Errorf("expected 1 deleted, got %d", deleted)
	}

	n, _ := st.Count()
	if n != 1 {
		t.Errorf("expected 1 remaining, got %d", n)
	}
}

func TestPurgeNothingOld(t *testing.T) {
	st := openTestStore(t)
	st.Put(makeReceipt("recent", "success"))

	deleted, err := st.Purge(1) // purge older than 1 day
	if err != nil {
		t.Fatalf("Purge: %v", err)
	}
	if deleted != 0 {
		t.Errorf("expected 0 deleted, got %d", deleted)
	}
}
