package store

import (
	"context"
	"testing"
)

func TestSupportConversationLifecycle(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	const userID = int64(555)

	// Empty conversation: no messages, no unread, nothing to close.
	if msgs, err := st.ListSupportMessages(ctx, userID); err != nil || len(msgs) != 0 {
		t.Fatalf("empty list: got %v err %v", msgs, err)
	}
	if n, err := st.CountUnreadForUser(ctx, userID); err != nil || n != 0 {
		t.Fatalf("empty unread: got %d err %v", n, err)
	}

	// User sends two messages.
	if _, err := st.AddSupportMessage(ctx, SupportMessage{UserTelegramID: userID, UserUsername: "alice", Text: "hello"}); err != nil {
		t.Fatalf("add user msg: %v", err)
	}
	if _, err := st.AddSupportMessage(ctx, SupportMessage{UserTelegramID: userID, UserUsername: "alice", Text: "anyone there?"}); err != nil {
		t.Fatalf("add user msg: %v", err)
	}

	// Both appear in the admin conversation list with unread = 2.
	convs, err := st.ListSupportConversations(ctx)
	if err != nil || len(convs) != 1 {
		t.Fatalf("list conversations: got %v err %v", convs, err)
	}
	if convs[0].UserTelegramID != userID || convs[0].UserUsername != "alice" {
		t.Fatalf("conversation identity: %+v", convs[0])
	}
	if convs[0].Unread != 2 || convs[0].LastText != "anyone there?" {
		t.Fatalf("conversation summary: %+v", convs[0])
	}

	// Admin reads, then replies.
	if err := st.MarkSupportReadByAdmin(ctx, userID); err != nil {
		t.Fatalf("mark read by admin: %v", err)
	}
	if _, err := st.AddSupportMessage(ctx, SupportMessage{UserTelegramID: userID, FromAdmin: true, AuthorTelegramID: 1, Text: "hi, how can I help?"}); err != nil {
		t.Fatalf("add admin msg: %v", err)
	}

	// Admin list now shows no unread (user messages read; admin reply doesn't count).
	convs, err = st.ListSupportConversations(ctx)
	if err != nil || len(convs) != 1 || convs[0].Unread != 0 {
		t.Fatalf("after admin reply: %+v err %v", convs, err)
	}

	// The user has one unread admin reply until they read it.
	if n, err := st.CountUnreadForUser(ctx, userID); err != nil || n != 1 {
		t.Fatalf("user unread: got %d err %v", n, err)
	}
	if err := st.MarkSupportReadByUser(ctx, userID); err != nil {
		t.Fatalf("mark read by user: %v", err)
	}
	if n, err := st.CountUnreadForUser(ctx, userID); err != nil || n != 0 {
		t.Fatalf("user unread after read: got %d err %v", n, err)
	}

	// Full thread is preserved in order.
	msgs, err := st.ListSupportMessages(ctx, userID)
	if err != nil || len(msgs) != 3 {
		t.Fatalf("list messages: got %d err %v", len(msgs), err)
	}
	if msgs[0].Text != "hello" || msgs[2].FromAdmin != true {
		t.Fatalf("message order/flags: %+v", msgs)
	}

	// Closing deletes the thread.
	closed, err := st.CloseSupportConversation(ctx, userID)
	if err != nil || !closed {
		t.Fatalf("close: closed=%v err=%v", closed, err)
	}
	if msgs, err := st.ListSupportMessages(ctx, userID); err != nil || len(msgs) != 0 {
		t.Fatalf("after close: got %v err %v", msgs, err)
	}
	if closed, err := st.CloseSupportConversation(ctx, userID); err != nil || closed {
		t.Fatalf("close empty: closed=%v err=%v", closed, err)
	}
}
