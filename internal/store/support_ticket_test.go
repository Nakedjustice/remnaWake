package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestSupportTicketLifecycle(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	const userID = int64(555)

	// No tickets yet.
	if tk, err := st.LatestOpenTicketForUser(ctx, userID); err != nil || tk != nil {
		t.Fatalf("latest open on empty: got %+v err %v", tk, err)
	}
	if list, err := st.ListSupportTicketsForUser(ctx, userID); err != nil || len(list) != 0 {
		t.Fatalf("user list on empty: got %v err %v", list, err)
	}

	id, err := st.CreateSupportTicket(ctx, userID, "alice", "billing question")
	if err != nil || id == 0 {
		t.Fatalf("create ticket: id=%d err=%v", id, err)
	}

	// A fresh ticket without messages is still listed and open.
	list, err := st.ListSupportTicketsForUser(ctx, userID)
	if err != nil || len(list) != 1 {
		t.Fatalf("user list: got %v err %v", list, err)
	}
	if list[0].ID != id || list[0].Status != "open" || list[0].Subject != "billing question" {
		t.Fatalf("fresh ticket: %+v", list[0])
	}

	tk, err := st.GetSupportTicket(ctx, id)
	if err != nil || tk == nil || tk.UserTelegramID != userID || tk.UserUsername != "alice" {
		t.Fatalf("get ticket: %+v err %v", tk, err)
	}
	if tk.ClosedAt != nil || tk.ClosedBy != "" {
		t.Fatalf("fresh ticket close fields: %+v", tk)
	}
	if missing, err := st.GetSupportTicket(ctx, id+999); err != nil || missing != nil {
		t.Fatalf("get missing ticket: %+v err %v", missing, err)
	}

	open, err := st.LatestOpenTicketForUser(ctx, userID)
	if err != nil || open == nil || open.ID != id {
		t.Fatalf("latest open: %+v err %v", open, err)
	}

	// Messages attach to the ticket.
	if _, err := st.AddSupportMessage(ctx, SupportMessage{TicketID: id, UserTelegramID: userID, UserUsername: "alice", Text: "hello"}); err != nil {
		t.Fatalf("add msg: %v", err)
	}
	if _, err := st.AddSupportMessage(ctx, SupportMessage{TicketID: id, UserTelegramID: userID, FromAdmin: true, AuthorTelegramID: 1, Text: "hi!"}); err != nil {
		t.Fatalf("add admin msg: %v", err)
	}
	msgs, err := st.ListSupportMessagesByTicket(ctx, id)
	if err != nil || len(msgs) != 2 {
		t.Fatalf("list by ticket: got %d err %v", len(msgs), err)
	}
	if msgs[0].Text != "hello" || !msgs[1].FromAdmin || msgs[1].TicketID != id {
		t.Fatalf("message order/fields: %+v", msgs)
	}

	// The user list reflects the last message and the unread admin reply.
	list, err = st.ListSupportTicketsForUser(ctx, userID)
	if err != nil || len(list) != 1 {
		t.Fatalf("user list after msgs: got %v err %v", list, err)
	}
	if list[0].LastText != "hi!" || !list[0].LastFromAdmin || list[0].Unread != 1 {
		t.Fatalf("user list summary: %+v", list[0])
	}

	// Close: one-way, exactly once.
	closedAt := time.Now()
	closed, err := st.CloseSupportTicket(ctx, id, "user", closedAt)
	if err != nil || !closed {
		t.Fatalf("close: closed=%v err=%v", closed, err)
	}
	if closed, err := st.CloseSupportTicket(ctx, id, "admin", closedAt); err != nil || closed {
		t.Fatalf("double close: closed=%v err=%v", closed, err)
	}
	tk, err = st.GetSupportTicket(ctx, id)
	if err != nil || tk == nil || tk.Status != "closed" || tk.ClosedBy != "user" || tk.ClosedAt == nil {
		t.Fatalf("closed ticket: %+v err %v", tk, err)
	}
	if open, err := st.LatestOpenTicketForUser(ctx, userID); err != nil || open != nil {
		t.Fatalf("latest open after close: %+v err %v", open, err)
	}

	// History survives the close.
	if msgs, err := st.ListSupportMessagesByTicket(ctx, id); err != nil || len(msgs) != 2 {
		t.Fatalf("history after close: got %d err %v", len(msgs), err)
	}
}

func TestAddSupportMessageToOpenTicketRejectsClosed(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	const userID = int64(7)
	id, err := st.CreateSupportTicket(ctx, userID, "bob", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	msgID, ok, err := st.AddSupportMessageToOpenTicket(ctx, SupportMessage{TicketID: id, UserTelegramID: userID, Text: "first"})
	if err != nil || !ok || msgID == 0 {
		t.Fatalf("insert into open: id=%d ok=%v err=%v", msgID, ok, err)
	}

	if _, err := st.CloseSupportTicket(ctx, id, "user", time.Now()); err != nil {
		t.Fatalf("close: %v", err)
	}

	if _, ok, err := st.AddSupportMessageToOpenTicket(ctx, SupportMessage{TicketID: id, UserTelegramID: userID, Text: "too late"}); err != nil || ok {
		t.Fatalf("insert into closed: ok=%v err=%v", ok, err)
	}
	if msgs, err := st.ListSupportMessagesByTicket(ctx, id); err != nil || len(msgs) != 1 {
		t.Fatalf("closed ticket messages: got %d err %v", len(msgs), err)
	}

	// Unknown ticket behaves like a closed one.
	if _, ok, err := st.AddSupportMessageToOpenTicket(ctx, SupportMessage{TicketID: id + 999, UserTelegramID: userID, Text: "ghost"}); err != nil || ok {
		t.Fatalf("insert into missing: ok=%v err=%v", ok, err)
	}
}

func TestListSupportTicketsAdminUnreadAndOrdering(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	// Ticket A (user 1) gets the first message, ticket B (user 2) the latest;
	// ticket C is closed and must sort after every open ticket.
	a, _ := st.CreateSupportTicket(ctx, 1, "alice", "a")
	b, _ := st.CreateSupportTicket(ctx, 2, "bob", "b")
	c, _ := st.CreateSupportTicket(ctx, 3, "carol", "c")

	if _, err := st.AddSupportMessage(ctx, SupportMessage{TicketID: a, UserTelegramID: 1, Text: "from alice"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := st.AddSupportMessage(ctx, SupportMessage{TicketID: c, UserTelegramID: 3, Text: "from carol"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := st.AddSupportMessage(ctx, SupportMessage{TicketID: b, UserTelegramID: 2, Text: "from bob"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := st.CloseSupportTicket(ctx, c, "admin", time.Now()); err != nil {
		t.Fatalf("close: %v", err)
	}

	list, err := st.ListSupportTicketsAdmin(ctx)
	if err != nil || len(list) != 3 {
		t.Fatalf("admin list: got %d err %v", len(list), err)
	}
	if list[0].ID != b || list[1].ID != a || list[2].ID != c {
		t.Fatalf("admin ordering: %d %d %d (want %d %d %d)", list[0].ID, list[1].ID, list[2].ID, b, a, c)
	}
	if list[0].Unread != 1 || list[0].UserUsername != "bob" || list[0].UserTelegramID != 2 {
		t.Fatalf("admin summary: %+v", list[0])
	}
	if list[2].Status != "closed" {
		t.Fatalf("closed ticket status: %+v", list[2])
	}

	// Admin reads ticket B: its unread drops, A's stays.
	if err := st.MarkTicketReadByAdmin(ctx, b); err != nil {
		t.Fatalf("mark read: %v", err)
	}
	list, err = st.ListSupportTicketsAdmin(ctx)
	if err != nil {
		t.Fatalf("admin list: %v", err)
	}
	if list[0].Unread != 0 || list[1].Unread != 1 {
		t.Fatalf("unread after read: %+v", list[:2])
	}
}

func TestReadAdminSupportAttentionCountsOnlyUnreadOpenTickets(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	openID, _ := st.CreateSupportTicket(ctx, 1, "alice", "open")
	readID, _ := st.CreateSupportTicket(ctx, 2, "bob", "read")
	closedID, _ := st.CreateSupportTicket(ctx, 3, "carol", "closed")

	oldest := time.Now().UTC().Add(-25 * time.Hour).Truncate(time.Second)
	firstID, err := st.AddSupportMessage(ctx, SupportMessage{TicketID: openID, UserTelegramID: 1, Text: "first unread"})
	if err != nil {
		t.Fatalf("add first unread: %v", err)
	}
	if _, err := st.AddSupportMessage(ctx, SupportMessage{TicketID: openID, UserTelegramID: 1, Text: "second unread"}); err != nil {
		t.Fatalf("add second unread: %v", err)
	}
	if _, err := st.AddSupportMessage(ctx, SupportMessage{TicketID: openID, UserTelegramID: 1, FromAdmin: true, Text: "admin reply"}); err != nil {
		t.Fatalf("add admin reply: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE support_messages SET created_at = ? WHERE id = ?`, formatTime(oldest), firstID); err != nil {
		t.Fatalf("age first unread: %v", err)
	}

	if _, err := st.AddSupportMessage(ctx, SupportMessage{TicketID: readID, UserTelegramID: 2, Text: "already read"}); err != nil {
		t.Fatalf("add read message: %v", err)
	}
	if err := st.MarkTicketReadByAdmin(ctx, readID); err != nil {
		t.Fatalf("mark ticket read: %v", err)
	}

	if _, err := st.AddSupportMessage(ctx, SupportMessage{TicketID: closedID, UserTelegramID: 3, Text: "closed unread"}); err != nil {
		t.Fatalf("add closed message: %v", err)
	}
	if _, err := st.CloseSupportTicket(ctx, closedID, "admin", time.Now()); err != nil {
		t.Fatalf("close ticket: %v", err)
	}

	attention, err := st.ReadAdminSupportAttention(ctx)
	if err != nil {
		t.Fatalf("read support attention: %v", err)
	}
	if attention.Tickets != 1 || attention.UnreadMessages != 2 {
		t.Fatalf("attention counts = %+v, want 1 ticket and 2 messages", attention)
	}
	if attention.OldestUnreadAt.IsZero() || !attention.OldestUnreadAt.Equal(oldest) {
		t.Fatalf("oldest unread = %v, want %v", attention.OldestUnreadAt, oldest)
	}
}

func TestListSupportTicketsAdminCapsClosed(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	for i := range 55 {
		id, err := st.CreateSupportTicket(ctx, int64(100+i), fmt.Sprintf("u%d", i), "")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if _, err := st.CloseSupportTicket(ctx, id, "admin", time.Now()); err != nil {
			t.Fatalf("close: %v", err)
		}
	}
	open, err := st.CreateSupportTicket(ctx, 999, "openuser", "")
	if err != nil {
		t.Fatalf("create open: %v", err)
	}

	list, err := st.ListSupportTicketsAdmin(ctx)
	if err != nil {
		t.Fatalf("admin list: %v", err)
	}
	if list[0].ID != open {
		t.Fatalf("open ticket must lead: %+v", list[0])
	}
	closedSeen := 0
	for _, tk := range list {
		if tk.Status == "closed" {
			closedSeen++
		}
	}
	if closedSeen > 50 {
		t.Fatalf("closed tickets not capped: %d", closedSeen)
	}
}

func TestMarkTicketReadScopedToTicket(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	const userID = int64(42)
	a, _ := st.CreateSupportTicket(ctx, userID, "dave", "first")
	b, _ := st.CreateSupportTicket(ctx, userID, "dave", "second")

	for _, id := range []int64{a, b} {
		if _, err := st.AddSupportMessage(ctx, SupportMessage{TicketID: id, UserTelegramID: userID, FromAdmin: true, AuthorTelegramID: 1, Text: "reply"}); err != nil {
			t.Fatalf("add: %v", err)
		}
	}

	if err := st.MarkTicketReadByUser(ctx, a); err != nil {
		t.Fatalf("mark read: %v", err)
	}
	list, err := st.ListSupportTicketsForUser(ctx, userID)
	if err != nil || len(list) != 2 {
		t.Fatalf("user list: got %d err %v", len(list), err)
	}
	unreadByID := map[int64]int{list[0].ID: list[0].Unread, list[1].ID: list[1].Unread}
	if unreadByID[a] != 0 || unreadByID[b] != 1 {
		t.Fatalf("scoped read: %v", unreadByID)
	}
}

// TestSupportTicketMigrationAdoptsLegacyMessages builds a database with the
// pre-ticket support_messages shape, opens it through store.New and checks the
// orphan rows are adopted into one open legacy ticket per user, idempotently.
func TestSupportTicketMigrationAdoptsLegacyMessages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")

	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	if _, err := raw.Exec(`
		CREATE TABLE support_messages (
		  id                 INTEGER PRIMARY KEY AUTOINCREMENT,
		  user_telegram_id   INTEGER NOT NULL,
		  user_username      TEXT NOT NULL DEFAULT '',
		  from_admin         INTEGER NOT NULL,
		  author_telegram_id INTEGER NOT NULL DEFAULT 0,
		  text               TEXT NOT NULL,
		  created_at         TEXT NOT NULL,
		  read_by_user       INTEGER NOT NULL DEFAULT 0,
		  read_by_admin      INTEGER NOT NULL DEFAULT 0
		);
	`); err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	insert := func(user int64, username, text, createdAt string, fromAdmin int) {
		t.Helper()
		if _, err := raw.Exec(`
			INSERT INTO support_messages (user_telegram_id, user_username, from_admin, text, created_at)
			VALUES (?, ?, ?, ?, ?)`, user, username, fromAdmin, text, createdAt); err != nil {
			t.Fatalf("insert legacy row: %v", err)
		}
	}
	insert(10, "", "old hello", "2026-01-01T10:00:00Z", 0)
	insert(10, "alice", "still there?", "2026-01-02T10:00:00Z", 0)
	insert(20, "bob", "hi", "2026-02-01T09:00:00Z", 0)
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	st, err := New(path)
	if err != nil {
		t.Fatalf("New on legacy db: %v", err)
	}
	ctx := context.Background()

	for _, tc := range []struct {
		user      int64
		username  string
		createdAt string
		msgs      int
	}{
		{10, "alice", "2026-01-01T10:00:00Z", 2},
		{20, "bob", "2026-02-01T09:00:00Z", 1},
	} {
		open, err := st.LatestOpenTicketForUser(ctx, tc.user)
		if err != nil || open == nil {
			t.Fatalf("user %d adopted ticket: %+v err %v", tc.user, open, err)
		}
		if open.UserUsername != tc.username {
			t.Fatalf("user %d adopted username: %q", tc.user, open.UserUsername)
		}
		if got := open.CreatedAt.UTC().Format(time.RFC3339); got != tc.createdAt {
			t.Fatalf("user %d adopted created_at: %q want %q", tc.user, got, tc.createdAt)
		}
		msgs, err := st.ListSupportMessagesByTicket(ctx, open.ID)
		if err != nil || len(msgs) != tc.msgs {
			t.Fatalf("user %d adopted messages: got %d err %v", tc.user, len(msgs), err)
		}
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	// Re-running the migration must not duplicate tickets.
	st, err = New(path)
	if err != nil {
		t.Fatalf("New second time: %v", err)
	}
	defer st.Close()
	list, err := st.ListSupportTicketsAdmin(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("idempotence: got %d tickets err %v", len(list), err)
	}
}
