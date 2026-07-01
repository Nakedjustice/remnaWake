package store

import (
	"context"
	"testing"
	"time"
)

func TestDeleteInviteRequest(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	id, err := st.CreateInviteRequest(ctx, InviteRequest{
		InviterTelegramID: 111, InviterUsername: "inviter", NewUsername: "newbie",
		Months: 1, Price: 300, Status: "pending",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Unconditional: an approved (resolved) request must be deletable too.
	if _, err := st.ResolveInviteRequest(ctx, id, "approved", time.Now()); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	ok, err := st.DeleteInviteRequest(ctx, id)
	if err != nil || !ok {
		t.Fatalf("delete: ok=%v err=%v", ok, err)
	}
	if got, _ := st.GetInviteRequest(ctx, id); got != nil {
		t.Fatalf("invite still present: %+v", got)
	}
	// Deleting again (or a never-existing id) reports no row removed.
	if ok, err := st.DeleteInviteRequest(ctx, id); err != nil || ok {
		t.Fatalf("second delete: ok=%v err=%v, want ok=false", ok, err)
	}
}
