package payments

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/store"
)

// adminTG is the admin ID configured by newTestService; userTG is a regular user.
const (
	adminTG int64 = 1000
	userTG  int64 = 555
)

func TestWebAdminRejectsNonAdmin(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	ctx := context.Background()

	calls := map[string]error{}
	_, err := svc.AdminPanelData(ctx, userTG)
	calls["AdminPanelData"] = err
	calls["AdminSetTariff"] = svc.AdminSetTariff(ctx, userTG, 3, 300)
	calls["AdminDeleteTariff"] = svc.AdminDeleteTariff(ctx, userTG, 3)
	calls["AdminSetRequisites"] = svc.AdminSetRequisites(ctx, userTG, "x")
	calls["AdminRevokeGiftCode"] = svc.AdminRevokeGiftCode(ctx, userTG, 1)
	calls["AdminConfirmRequest"] = svc.AdminConfirmRequest(ctx, userTG, 1)
	calls["AdminRejectRequest"] = svc.AdminRejectRequest(ctx, userTG, 1)
	calls["AdminConfirmGiftRequest"] = svc.AdminConfirmGiftRequest(ctx, userTG, 1)
	calls["AdminRejectGiftRequest"] = svc.AdminRejectGiftRequest(ctx, userTG, 1)
	calls["AdminApproveInviteRequest"] = svc.AdminApproveInviteRequest(ctx, userTG, 1)
	calls["AdminRejectInviteRequest"] = svc.AdminRejectInviteRequest(ctx, userTG, 1)
	for name, err := range calls {
		if !errors.Is(err, ErrNotAdmin) {
			t.Errorf("%s: err = %v, want ErrNotAdmin", name, err)
		}
	}
}

func TestCabinetDataSetsIsAdmin(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	ctx := context.Background()

	data, err := svc.CabinetData(ctx, adminTG)
	if err != nil || !data.IsAdmin {
		t.Fatalf("admin cabinet: is_admin=%v err=%v", data.IsAdmin, err)
	}
	data, err = svc.CabinetData(ctx, userTG)
	if err != nil || data.IsAdmin {
		t.Fatalf("user cabinet: is_admin=%v err=%v", data.IsAdmin, err)
	}
}

func TestAdminPanelData(t *testing.T) {
	svc, _, _, st := newTestService(t)
	ctx := context.Background()

	if err := svc.AdminSetTariff(ctx, adminTG, 3, 450); err != nil {
		t.Fatalf("set tariff: %v", err)
	}
	if err := svc.AdminSetRequisites(ctx, adminTG, "card 1234"); err != nil {
		t.Fatalf("set requisites: %v", err)
	}

	giftID, err := st.CreateGiftCode(ctx, store.GiftCode{
		Code: "GIFT1", BuyerTelegramID: userTG, BuyerUsername: "alice", Months: 3, Price: 450,
	})
	if err != nil {
		t.Fatalf("create gift: %v", err)
	}
	if ok, err := st.IssueGiftCode(ctx, giftID, svc.now()); err != nil || !ok {
		t.Fatalf("issue gift: %v %v", ok, err)
	}

	exp := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	reqID, err := st.CreatePaymentRequest(ctx, store.PaymentRequest{
		RemnawaveID: 42, UUID: "uuid-42", Username: "alice", TelegramID: userTG,
		Months: 3, Price: 450, ExpireAt: exp, Status: "pending",
	})
	if err != nil {
		t.Fatalf("create request: %v", err)
	}

	pendingGiftID, err := st.CreateGiftCode(ctx, store.GiftCode{
		Code: "GIFT2", BuyerTelegramID: userTG, BuyerUsername: "alice", Months: 1, Price: 150,
	})
	if err != nil {
		t.Fatalf("create pending gift: %v", err)
	}

	invID, err := st.CreateInviteRequest(ctx, store.InviteRequest{
		InviterTelegramID: userTG, InviterUsername: "alice", NewUsername: "bob", Months: 1, Price: 150,
	})
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}

	panel, err := svc.AdminPanelData(ctx, adminTG)
	if err != nil {
		t.Fatalf("panel: %v", err)
	}
	if len(panel.Tariffs) != 1 || panel.Tariffs[0].Months != 3 || panel.Tariffs[0].Price != 450 {
		t.Fatalf("tariffs: %+v", panel.Tariffs)
	}
	if panel.Requisites != "card 1234" {
		t.Fatalf("requisites: %q", panel.Requisites)
	}
	if len(panel.GiftBuyers) != 1 || panel.GiftBuyers[0].Buyer != "alice" ||
		panel.GiftBuyers[0].BuyerTelegramID != userTG || len(panel.GiftBuyers[0].Used) != 0 ||
		len(panel.GiftBuyers[0].NotUsed) != 1 || panel.GiftBuyers[0].NotUsed[0].ID != giftID ||
		panel.GiftBuyers[0].NotUsed[0].Code != "GIFT1" {
		t.Fatalf("gift buyers: %+v", panel.GiftBuyers)
	}
	if len(panel.Requests) != 1 || panel.Requests[0].ID != reqID || panel.Requests[0].Username != "alice" ||
		panel.Requests[0].Months != 3 || panel.Requests[0].ExpireAt != "01.07.2026" {
		t.Fatalf("requests: %+v", panel.Requests)
	}
	if len(panel.GiftRequests) != 1 || panel.GiftRequests[0].ID != pendingGiftID ||
		panel.GiftRequests[0].Code != "GIFT2" || panel.GiftRequests[0].Buyer != "alice" ||
		panel.GiftRequests[0].Months != 1 || panel.GiftRequests[0].PriceLabel == "" {
		t.Fatalf("gift requests: %+v", panel.GiftRequests)
	}
	if len(panel.InviteRequests) != 1 || panel.InviteRequests[0].ID != invID ||
		panel.InviteRequests[0].Inviter != "alice" || panel.InviteRequests[0].NewUsername != "bob" ||
		panel.InviteRequests[0].Months != 1 || panel.InviteRequests[0].PriceLabel == "" {
		t.Fatalf("invite requests: %+v", panel.InviteRequests)
	}
}

func TestAdminConfirmGiftRequest(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()

	giftID, _ := st.CreateGiftCode(ctx, store.GiftCode{
		Code: "GIFT1", BuyerTelegramID: userTG, BuyerUsername: "alice", Months: 3, Price: 450,
	})

	if err := svc.AdminConfirmGiftRequest(ctx, adminTG, giftID); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	g, _ := st.GetGiftCode(ctx, giftID)
	if g.Status != "issued" {
		t.Fatalf("status: %s", g.Status)
	}
	// Buyer gets the redemption code in chat.
	var notified bool
	for _, m := range bot.sent {
		if m.ChatID == userTG && strings.Contains(m.Text, "GIFT1") {
			notified = true
		}
	}
	if !notified {
		t.Fatalf("buyer not notified: %+v", bot.sent)
	}
	if err := svc.AdminConfirmGiftRequest(ctx, adminTG, giftID); !errors.Is(err, ErrRequestResolved) {
		t.Fatalf("second confirm: err = %v, want ErrRequestResolved", err)
	}
	if err := svc.AdminConfirmGiftRequest(ctx, adminTG, 9999); !errors.Is(err, ErrRequestNotFound) {
		t.Fatalf("missing: err = %v, want ErrRequestNotFound", err)
	}
}

func TestAdminRejectGiftRequest(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()

	giftID, _ := st.CreateGiftCode(ctx, store.GiftCode{
		Code: "GIFT1", BuyerTelegramID: userTG, BuyerUsername: "alice", Months: 3, Price: 450,
	})

	if err := svc.AdminRejectGiftRequest(ctx, adminTG, giftID); err != nil {
		t.Fatalf("reject: %v", err)
	}
	g, _ := st.GetGiftCode(ctx, giftID)
	if g.Status != "rejected" {
		t.Fatalf("status: %s", g.Status)
	}
	var notified bool
	for _, m := range bot.sent {
		if m.ChatID == userTG && strings.Contains(m.Text, "отклонена") {
			notified = true
		}
	}
	if !notified {
		t.Fatalf("buyer not notified: %+v", bot.sent)
	}
	if err := svc.AdminConfirmGiftRequest(ctx, adminTG, giftID); !errors.Is(err, ErrRequestResolved) {
		t.Fatalf("confirm after reject: err = %v, want ErrRequestResolved", err)
	}
}

func TestAdminApproveInviteRequest(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()

	invID, _ := st.CreateInviteRequest(ctx, store.InviteRequest{
		InviterTelegramID: userTG, InviterUsername: "alice", NewUsername: "bob", Months: 1, Price: 150,
	})

	if err := svc.AdminApproveInviteRequest(ctx, adminTG, invID); err != nil {
		t.Fatalf("approve: %v", err)
	}
	req, _ := st.GetInviteRequest(ctx, invID)
	if req.Status != "approved" {
		t.Fatalf("status: %s", req.Status)
	}
	var notified bool
	for _, m := range bot.sent {
		if m.ChatID == userTG && strings.Contains(m.Text, "bob") && strings.Contains(m.Text, "одобрена") {
			notified = true
		}
	}
	if !notified {
		t.Fatalf("inviter not notified: %+v", bot.sent)
	}
	if err := svc.AdminApproveInviteRequest(ctx, adminTG, invID); !errors.Is(err, ErrRequestResolved) {
		t.Fatalf("second approve: err = %v, want ErrRequestResolved", err)
	}
	if err := svc.AdminApproveInviteRequest(ctx, adminTG, 9999); !errors.Is(err, ErrRequestNotFound) {
		t.Fatalf("missing: err = %v, want ErrRequestNotFound", err)
	}
}

func TestAdminRejectInviteRequest(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()

	invID, _ := st.CreateInviteRequest(ctx, store.InviteRequest{
		InviterTelegramID: userTG, InviterUsername: "alice", NewUsername: "bob", Months: 1, Price: 150,
	})

	if err := svc.AdminRejectInviteRequest(ctx, adminTG, invID); err != nil {
		t.Fatalf("reject: %v", err)
	}
	req, _ := st.GetInviteRequest(ctx, invID)
	if req.Status != "rejected" {
		t.Fatalf("status: %s", req.Status)
	}
	var notified bool
	for _, m := range bot.sent {
		if m.ChatID == userTG && strings.Contains(m.Text, "отклонена") {
			notified = true
		}
	}
	if !notified {
		t.Fatalf("inviter not notified: %+v", bot.sent)
	}
	if err := svc.AdminApproveInviteRequest(ctx, adminTG, invID); !errors.Is(err, ErrRequestResolved) {
		t.Fatalf("approve after reject: err = %v, want ErrRequestResolved", err)
	}
}

func TestAdminSetTariffValidatesInput(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	ctx := context.Background()
	for _, tc := range [][2]int{{0, 100}, {-1, 100}, {3, 0}, {3, -5}} {
		if err := svc.AdminSetTariff(ctx, adminTG, tc[0], tc[1]); !errors.Is(err, ErrBadInput) {
			t.Errorf("months=%d price=%d: err = %v, want ErrBadInput", tc[0], tc[1], err)
		}
	}
}

func TestAdminDeleteTariff(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	ctx := context.Background()

	if err := svc.AdminSetTariff(ctx, adminTG, 3, 450); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := svc.AdminDeleteTariff(ctx, adminTG, 3); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := svc.AdminDeleteTariff(ctx, adminTG, 3); !errors.Is(err, ErrTariffUnknown) {
		t.Fatalf("delete missing: err = %v, want ErrTariffUnknown", err)
	}
}

func TestAdminSetRequisitesUpdatesStoreAndMemory(t *testing.T) {
	svc, _, _, st := newTestService(t)
	ctx := context.Background()

	if err := svc.AdminSetRequisites(ctx, adminTG, "  card 9999  "); err != nil {
		t.Fatalf("set: %v", err)
	}
	svc.mu.Lock()
	mem := svc.requisites
	svc.mu.Unlock()
	if mem != "card 9999" {
		t.Fatalf("memory: %q", mem)
	}
	value, found, err := st.GetSetting(ctx, requisitesKey)
	if err != nil || !found || value != "card 9999" {
		t.Fatalf("persisted: %q found=%v err=%v", value, found, err)
	}
	if err := svc.AdminSetRequisites(ctx, adminTG, "   "); !errors.Is(err, ErrBadInput) {
		t.Fatalf("blank: err = %v, want ErrBadInput", err)
	}
}

func TestAdminRevokeGiftCode(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()

	giftID, _ := st.CreateGiftCode(ctx, store.GiftCode{
		Code: "GIFT1", BuyerTelegramID: userTG, BuyerUsername: "alice", Months: 3, Price: 450,
	})
	if ok, err := st.IssueGiftCode(ctx, giftID, svc.now()); err != nil || !ok {
		t.Fatalf("issue: %v %v", ok, err)
	}

	if err := svc.AdminRevokeGiftCode(ctx, adminTG, giftID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	g, _ := st.GetGiftCode(ctx, giftID)
	if g.Status != "revoked" {
		t.Fatalf("status: %s", g.Status)
	}
	// Buyer is notified in chat.
	var notified bool
	for _, m := range bot.sent {
		if m.ChatID == userTG && strings.Contains(m.Text, "GIFT1") {
			notified = true
		}
	}
	if !notified {
		t.Fatalf("buyer not notified: %+v", bot.sent)
	}
	// Second revoke fails: already resolved.
	if err := svc.AdminRevokeGiftCode(ctx, adminTG, giftID); !errors.Is(err, ErrRequestResolved) {
		t.Fatalf("second revoke: err = %v, want ErrRequestResolved", err)
	}
}

func TestAdminConfirmRequest(t *testing.T) {
	svc, bot, ext, st := newTestService(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }

	exp := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	reqID, _ := st.CreatePaymentRequest(ctx, store.PaymentRequest{
		RemnawaveID: 42, UUID: "uuid-42", Username: "alice", TelegramID: userTG,
		Months: 3, Price: 450, ExpireAt: exp, Status: "pending",
	})

	if err := svc.AdminConfirmRequest(ctx, adminTG, reqID); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if ext.calls != 1 || ext.uuid != "uuid-42" || !ext.expire.Equal(exp.AddDate(0, 3, 0)) {
		t.Fatalf("extend: calls=%d uuid=%s expire=%v", ext.calls, ext.uuid, ext.expire)
	}
	req, _ := st.GetPaymentRequest(ctx, reqID)
	if req.Status != "confirmed" {
		t.Fatalf("status: %s", req.Status)
	}
	// The paying user gets a chat notification (webapp path only).
	var notified bool
	for _, m := range bot.sent {
		if m.ChatID == userTG && strings.Contains(m.Text, "продлена") {
			notified = true
		}
	}
	if !notified {
		t.Fatalf("user not notified: %+v", bot.sent)
	}
	// Second confirm: already resolved.
	if err := svc.AdminConfirmRequest(ctx, adminTG, reqID); !errors.Is(err, ErrRequestResolved) {
		t.Fatalf("second confirm: err = %v, want ErrRequestResolved", err)
	}
	if err := svc.AdminConfirmRequest(ctx, adminTG, 9999); !errors.Is(err, ErrRequestNotFound) {
		t.Fatalf("missing: err = %v, want ErrRequestNotFound", err)
	}
}

func TestAdminRejectRequest(t *testing.T) {
	svc, bot, ext, st := newTestService(t)
	ctx := context.Background()

	exp := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	reqID, _ := st.CreatePaymentRequest(ctx, store.PaymentRequest{
		RemnawaveID: 42, UUID: "uuid-42", Username: "alice", TelegramID: userTG,
		Months: 3, Price: 450, ExpireAt: exp, Status: "pending",
	})

	if err := svc.AdminRejectRequest(ctx, adminTG, reqID); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if ext.calls != 0 {
		t.Fatalf("reject must not extend: calls=%d", ext.calls)
	}
	req, _ := st.GetPaymentRequest(ctx, reqID)
	if req.Status != "rejected" {
		t.Fatalf("status: %s", req.Status)
	}
	var notified bool
	for _, m := range bot.sent {
		if m.ChatID == userTG && strings.Contains(m.Text, "отклонена") {
			notified = true
		}
	}
	if !notified {
		t.Fatalf("user not notified: %+v", bot.sent)
	}
	// Rejected request cannot be confirmed afterwards.
	if err := svc.AdminConfirmRequest(ctx, adminTG, reqID); !errors.Is(err, ErrRequestResolved) {
		t.Fatalf("confirm after reject: err = %v, want ErrRequestResolved", err)
	}
}
