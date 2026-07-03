package payments

import (
	"context"
	"testing"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/store"
)

func TestConfirmTrafficExtensionAppliesTemporaryLimitAndResetRestoresBase(t *testing.T) {
	svc, _, _, st := newTestService(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	reqID, err := st.CreateTrafficExtensionPaymentRequest(ctx, store.PaymentRequest{
		RemnawaveID: 1, UUID: "u-1", Username: "alice", TelegramID: 777,
		Price: 300, ExpireAt: now.AddDate(0, 1, 0), TrafficGB: 50,
		BaseTrafficLimitBytes: 100 * bytesPerGB, ExtraTrafficBytes: 50 * bytesPerGB,
	}, now)
	if err != nil {
		t.Fatalf("create traffic request: %v", err)
	}
	req, expiresAt, err := svc.confirmPaymentRequest(ctx, reqID)
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if req.Kind != store.PaymentKindTrafficExtension || !expiresAt.Equal(now.AddDate(0, 0, trafficExtensionDays)) {
		t.Fatalf("confirmed request = %+v expires=%v", req, expiresAt)
	}
	upd := svc.userUpdater.(*fakeUpdater)
	if len(upd.calls) != 1 || upd.calls[0].TrafficLimitBytes == nil || *upd.calls[0].TrafficLimitBytes != 150*bytesPerGB {
		t.Fatalf("apply patch = %+v", upd.calls)
	}

	svc.now = func() time.Time { return now.AddDate(0, 0, trafficExtensionDays+1) }
	svc.RunTrafficExtensionResets(ctx)
	if len(upd.calls) != 2 || upd.calls[1].TrafficLimitBytes == nil || *upd.calls[1].TrafficLimitBytes != 100*bytesPerGB {
		t.Fatalf("reset patch = %+v", upd.calls)
	}
	got, _ := st.GetPaymentRequest(ctx, reqID)
	if got.ExtensionRestoredAt == nil {
		t.Fatalf("extension not marked restored: %+v", got)
	}
}
