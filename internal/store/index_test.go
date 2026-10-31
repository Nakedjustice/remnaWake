package store

import (
	"path/filepath"
	"strings"
	"testing"
)

// queryPlan returns the EXPLAIN QUERY PLAN details for a query, joined so a
// test can assert on how SQLite reaches the rows.
func queryPlan(t *testing.T, st *Store, query string) string {
	t.Helper()
	rows, err := st.db.Query("EXPLAIN QUERY PLAN " + query)
	if err != nil {
		t.Fatalf("explain %q: %v", query, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		out = append(out, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("plan rows: %v", err)
	}
	return strings.Join(out, " | ")
}

// TestCabinetLookupsAreIndexed pins the two lookups that run on every single
// cabinet open (chat and mini app alike) to an index. Without them both were
// full table scans that grow with every gift and invite ever created.
func TestCabinetLookupsAreIndexed(t *testing.T) {
	st := newTestStore(t)
	for _, tc := range []struct{ name, query, table string }{
		{"gift codes by buyer", `SELECT id FROM gift_codes WHERE buyer_telegram_id = 1 ORDER BY id DESC`, "gift_codes"},
		{"invite requests by inviter", `SELECT id FROM invite_requests WHERE inviter_telegram_id = 1`, "invite_requests"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := queryPlan(t, st, tc.query)
			if !strings.Contains(plan, "USING INDEX") && !strings.Contains(plan, "USING COVERING INDEX") {
				t.Fatalf("plan uses no index: %s", plan)
			}
			if strings.Contains(plan, "SCAN "+tc.table) {
				t.Fatalf("plan still scans %s: %s", tc.table, plan)
			}
		})
	}
}

// TestReopenAddsMissingIndexes covers the upgrade path: an existing database
// created before these indexes existed must gain them when the new binary
// opens it, without a migration step.
func TestReopenAddsMissingIndexes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	st, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, idx := range []string{"idx_gift_codes_buyer", "idx_invite_requests_inviter"} {
		if _, err := st.db.Exec("DROP INDEX IF EXISTS " + idx); err != nil {
			t.Fatalf("drop %s: %v", idx, err)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened, err := New(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	for _, idx := range []string{"idx_gift_codes_buyer", "idx_invite_requests_inviter"} {
		var name string
		err := reopened.db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, idx).Scan(&name)
		if err != nil {
			t.Fatalf("index %s missing after reopen: %v", idx, err)
		}
	}
}
