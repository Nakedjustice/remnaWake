package payments

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/store"
)

// recExtender records every ExtendSubscription call so a test can assert
// both the payer/redeemer extension and the separate referrer-bonus extension.
type recExtender struct {
	userIDs []int64
	expires []time.Time
}

func (r *recExtender) ExtendSubscription(_ context.Context, userID int64, e time.Time) error {
	r.userIDs = append(r.userIDs, userID)
	r.expires = append(r.expires, e)
	return nil
}

func newReferralLinkService(t *testing.T, finder *fakeFinder) (*Service, *fakeBot, *recExtender, *fakeCreator, *store.Store) {
	t.Helper()
	st, err := store.New(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	bot := &fakeBot{}
	ext := &recExtender{}
	creator := &fakeCreator{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := New(st, bot, ext, creator, &fakeUpdater{ext: ext}, finder, &fakeRegistrar{}, newFakeSquadLister(), []int64{1000}, "₽", false, logger)
	svc.now = func() time.Time { return time.Date(2026, 6, 20, 0, 0, 0, 0, time.UTC) }
	return svc, bot, ext, creator, st
}

func TestStartReferralRecordsNewUser(t *testing.T) {
	ctx := context.Background()
	finder := &fakeFinder{byTG: map[int64][]Subscriber{
		300: {{RemnawaveID: 30, Username: "referrer", TelegramID: 300, ExpireAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)}},
	}}
	svc, bot, _, _, st := newReferralLinkService(t, finder)
	svc.InitReferral(true, 30, 5)

	svc.StartReferral(ctx, 555, "300")

	ref, err := st.GetReferral(ctx, 555)
	if err != nil || ref == nil {
		t.Fatalf("referral not recorded: %v %+v", err, ref)
	}
	if ref.ReferrerTelegramID != 300 || ref.Status != "pending" {
		t.Fatalf("bad referral: %+v", ref)
	}
	var noted bool
	for _, m := range bot.sent {
		if m.ChatID == 555 {
			noted = true
		}
	}
	if !noted {
		t.Fatalf("clicker not notified: %+v", bot.sent)
	}
}

func TestStartReferralSkips(t *testing.T) {
	ctx := context.Background()
	finder := &fakeFinder{byTG: map[int64][]Subscriber{
		300: {{TelegramID: 300}},
		777: {{TelegramID: 777}}, // already a subscriber
	}}
	svc, _, _, _, st := newReferralLinkService(t, finder)
	svc.InitReferral(true, 30, 5)

	svc.StartReferral(ctx, 300, "300") // self-referral
	if r, _ := st.GetReferral(ctx, 300); r != nil {
		t.Fatalf("self-referral should be ignored: %+v", r)
	}
	svc.StartReferral(ctx, 777, "300") // existing subscriber
	if r, _ := st.GetReferral(ctx, 777); r != nil {
		t.Fatalf("existing user should not be referred: %+v", r)
	}
	svc.StartReferral(ctx, 888, "999") // referrer has no profile
	if r, _ := st.GetReferral(ctx, 888); r != nil {
		t.Fatalf("junk referrer should be ignored: %+v", r)
	}
}

func TestStartReferralDisabledRecordsNothing(t *testing.T) {
	ctx := context.Background()
	finder := &fakeFinder{byTG: map[int64][]Subscriber{300: {{TelegramID: 300}}}}
	svc, _, _, _, st := newReferralLinkService(t, finder)
	// referral disabled

	svc.StartReferral(ctx, 555, "300")
	if r, _ := st.GetReferral(ctx, 555); r != nil {
		t.Fatalf("disabled program should record nothing: %+v", r)
	}
}

func TestReferralLinkCreditOnFirstPayment(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	finder := &fakeFinder{byTG: map[int64][]Subscriber{
		300: {{RemnawaveID: 30, Username: "referrer", TelegramID: 300, ExpireAt: base}},
		555: {{RemnawaveID: 55, Username: "newbie", TelegramID: 555, ExpireAt: base}},
	}}
	svc, bot, ext, _, st := newReferralLinkService(t, finder)
	svc.InitReferral(true, 30, 5)

	if _, err := st.CreateReferral(ctx, 555, 300, svc.now()); err != nil {
		t.Fatalf("seed referral: %v", err)
	}
	reqID, err := st.CreatePaymentRequest(ctx, store.PaymentRequest{
		RemnawaveID: 55, Username: "newbie", TelegramID: 555,
		Months: 1, Price: 100, ExpireAt: base,
	})
	if err != nil {
		t.Fatalf("create req: %v", err)
	}

	if _, _, err := svc.confirmPaymentRequest(ctx, reqID); err != nil {
		t.Fatalf("confirm: %v", err)
	}

	if len(ext.userIDs) != 2 {
		t.Fatalf("expected 2 extends (payer + referrer), got %v", ext.userIDs)
	}
	// The payer's extension folds in the invitee bonus: 1 month + 5 days.
	wantPayer := base.AddDate(0, 1, 0).AddDate(0, 0, 5)
	if ext.userIDs[0] != 55 || !ext.expires[0].Equal(wantPayer) {
		t.Fatalf("payer extend = %d @ %s, want 55 @ %s", ext.userIDs[0], ext.expires[0], wantPayer)
	}
	// The referrer earns 30 days on their own subscription.
	wantRef := base.AddDate(0, 0, 30)
	if ext.userIDs[1] != 30 || !ext.expires[1].Equal(wantRef) {
		t.Fatalf("referrer extend = %d @ %s, want 30 @ %s", ext.userIDs[1], ext.expires[1], wantRef)
	}
	if r, _ := st.GetReferral(ctx, 555); r == nil || r.Status != "credited" {
		t.Fatalf("referral not credited: %+v", r)
	}
	var refNote, payerNote bool
	for _, m := range bot.sent {
		if m.ChatID == 300 && strings.Contains(m.Text, "начислено") {
			refNote = true
		}
		if m.ChatID == 555 && strings.Contains(m.Text, "начислено") {
			payerNote = true
		}
	}
	if !refNote || !payerNote {
		t.Fatalf("missing bonus notifications: %+v", bot.sent)
	}

	// A second payment by the same user must not credit the referrer again.
	req2, _ := st.CreatePaymentRequest(ctx, store.PaymentRequest{
		RemnawaveID: 55, Username: "newbie", TelegramID: 555,
		Months: 1, Price: 100, ExpireAt: base,
	})
	if _, _, err := svc.confirmPaymentRequest(ctx, req2); err != nil {
		t.Fatalf("confirm2: %v", err)
	}
	if len(ext.userIDs) != 3 || ext.userIDs[2] != 55 {
		t.Fatalf("second payment should add exactly one payer extend, got %v", ext.userIDs)
	}
}

func TestGiftReferralCreditsBuyer(t *testing.T) {
	ctx := context.Background()
	buyerExpiry := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	finder := &fakeFinder{byTG: map[int64][]Subscriber{
		300: {{RemnawaveID: 30, Username: "buyer", TelegramID: 300, ExpireAt: buyerExpiry}},
		// redeemer 555 has no profile yet
	}}
	svc, bot, ext, creator, st := newReferralLinkService(t, finder)
	svc.InitReferral(true, 30, 5)

	code := "ABCDEFGH23456789"
	gid, err := st.CreateGiftCode(ctx, store.GiftCode{Code: code, BuyerTelegramID: 300, BuyerUsername: "buyer", Months: 1})
	if err != nil {
		t.Fatalf("create gift: %v", err)
	}
	if _, err := st.IssueGiftCode(ctx, gid, svc.now()); err != nil {
		t.Fatalf("issue gift: %v", err)
	}

	res, err := svc.RedeemGift(ctx, 555, code, 0, "newbie")
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}

	// The new subscription includes the invitee bonus: 1 month + 5 days.
	wantExpire := svc.now().AddDate(0, 1, 0).AddDate(0, 0, 5)
	if res.ExpireAt != wantExpire.Format("02.01.2006") {
		t.Fatalf("new sub expire = %s, want %s", res.ExpireAt, wantExpire.Format("02.01.2006"))
	}
	if len(creator.created) != 1 || creator.created[0] != "newbie" {
		t.Fatalf("new user not created: %v", creator.created)
	}
	// The gift buyer is credited 30 days — the only extension here.
	if len(ext.userIDs) != 1 || ext.userIDs[0] != 30 {
		t.Fatalf("buyer not credited: %v", ext.userIDs)
	}
	if !ext.expires[0].Equal(buyerExpiry.AddDate(0, 0, 30)) {
		t.Fatalf("buyer expire = %s, want %s", ext.expires[0], buyerExpiry.AddDate(0, 0, 30))
	}
	var buyerNote, redeemerNote bool
	for _, m := range bot.sent {
		if m.ChatID == 300 && strings.Contains(m.Text, "начислено") {
			buyerNote = true
		}
		if m.ChatID == 555 && strings.Contains(m.Text, "начислено") {
			redeemerNote = true
		}
	}
	if !buyerNote || !redeemerNote {
		t.Fatalf("missing gift bonus notifications: %+v", bot.sent)
	}
	if g, _ := st.GetGiftCode(ctx, gid); g == nil || g.Status != "redeemed" {
		t.Fatalf("gift not redeemed: %+v", g)
	}
}

func TestGiftReferralExistingUserNoCredit(t *testing.T) {
	ctx := context.Background()
	finder := &fakeFinder{byTG: map[int64][]Subscriber{
		300: {{RemnawaveID: 30, TelegramID: 300, ExpireAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)}},
		555: {{RemnawaveID: 9, Username: "old", TelegramID: 555, ExpireAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)}},
	}}
	svc, bot, ext, _, st := newReferralLinkService(t, finder)
	svc.InitReferral(true, 30, 5)

	code := "ABCDEFGH23456789"
	gid, _ := st.CreateGiftCode(ctx, store.GiftCode{Code: code, BuyerTelegramID: 300, Months: 1})
	_, _ = st.IssueGiftCode(ctx, gid, svc.now())

	if _, err := svc.RedeemGift(ctx, 555, code, 9, "old"); err != nil {
		t.Fatalf("redeem: %v", err)
	}
	for _, u := range ext.userIDs {
		if u == 30 {
			t.Fatalf("buyer must not be credited for an existing-subscriber redemption: %v", ext.userIDs)
		}
	}
	for _, m := range bot.sent {
		if m.ChatID == 300 && strings.Contains(m.Text, "начислено") {
			t.Fatalf("buyer must not get a referral bonus DM: %q", m.Text)
		}
	}
}

func TestGiftReferralDisabledNoCredit(t *testing.T) {
	ctx := context.Background()
	finder := &fakeFinder{byTG: map[int64][]Subscriber{
		300: {{RemnawaveID: 30, TelegramID: 300, ExpireAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)}},
	}}
	svc, _, ext, _, st := newReferralLinkService(t, finder)
	// referral disabled

	code := "ABCDEFGH23456789"
	gid, _ := st.CreateGiftCode(ctx, store.GiftCode{Code: code, BuyerTelegramID: 300, Months: 1})
	_, _ = st.IssueGiftCode(ctx, gid, svc.now())

	res, err := svc.RedeemGift(ctx, 555, code, 0, "newbie")
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	wantPlain := svc.now().AddDate(0, 1, 0)
	if res.ExpireAt != wantPlain.Format("02.01.2006") {
		t.Fatalf("expire = %s, want plain %s (no invitee bonus)", res.ExpireAt, wantPlain.Format("02.01.2006"))
	}
	if len(ext.userIDs) != 0 {
		t.Fatalf("no extends expected when referral disabled, got %v", ext.userIDs)
	}
}

func TestCabinetDataReferral(t *testing.T) {
	ctx := context.Background()
	finder := &fakeFinder{byTG: map[int64][]Subscriber{
		300: {{RemnawaveID: 1, Username: "me", TelegramID: 300, ExpireAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)}},
	}}
	svc, _, _, _, st := newReferralLinkService(t, finder)
	svc.SetBotUsername("mybot")
	svc.InitReferral(true, 30, 5)

	_, _ = st.CreateReferral(ctx, 901, 300, svc.now())
	_, _ = st.CreateReferral(ctx, 902, 300, svc.now())
	_, _ = st.CreditReferral(ctx, 901, svc.now())

	cab, err := svc.CabinetData(ctx, 300)
	if err != nil {
		t.Fatalf("cabinet: %v", err)
	}
	if cab.Referral == nil {
		t.Fatalf("referral block missing")
	}
	if cab.Referral.Link != "https://t.me/mybot?start=ref_300" {
		t.Fatalf("link = %q", cab.Referral.Link)
	}
	if cab.Referral.InviterDays != 30 || cab.Referral.InviteeDays != 5 {
		t.Fatalf("days = %+v", cab.Referral)
	}
	if cab.Referral.Invited != 2 || cab.Referral.Credited != 1 {
		t.Fatalf("stats = %+v", cab.Referral)
	}

	if err := svc.setReferralEnabled(ctx, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	cab2, err := svc.CabinetData(ctx, 300)
	if err != nil {
		t.Fatalf("cabinet2: %v", err)
	}
	if cab2.Referral != nil {
		t.Fatalf("referral block should be absent when disabled")
	}
}
