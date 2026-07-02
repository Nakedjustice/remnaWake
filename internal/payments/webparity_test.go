package payments

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/store"
)

func TestRegisterProfileByUsernameAndLink(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query string
	}{
		{name: "username", query: "alice"},
		{name: "subscription link", query: "https://vpn.example/sub/short-42"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, _, _ := newTestService(t)
			finder := &fakeFinder{
				byName:  map[string]*Subscriber{"alice": {UUID: "uuid-42", Username: "alice"}},
				byShort: map[string]*Subscriber{"short-42": {UUID: "uuid-42", Username: "alice"}},
			}
			registrar := &fakeRegistrar{}
			svc.finder, svc.registrar = finder, registrar

			got, err := svc.RegisterProfile(context.Background(), 777, tc.query)
			if err != nil {
				t.Fatalf("RegisterProfile: %v", err)
			}
			if got.Username != "alice" || registrar.uuid != "uuid-42" || registrar.telegramID != 777 {
				t.Fatalf("unexpected result=%+v registrar=%+v", got, registrar)
			}
		})
	}
}

func TestRegisterProfileIsIdempotentAndConcealsForeignLink(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	svc.finder = &fakeFinder{byName: map[string]*Subscriber{
		"mine":    {UUID: "mine", Username: "mine", TelegramID: 777},
		"foreign": {UUID: "foreign", Username: "foreign", TelegramID: 888},
	}}
	registrar := &fakeRegistrar{}
	svc.registrar = registrar

	if _, err := svc.RegisterProfile(context.Background(), 777, "mine"); err != nil {
		t.Fatalf("idempotent register: %v", err)
	}
	if registrar.calls != 0 {
		t.Fatal("idempotent registration must not call panel")
	}
	if _, err := svc.RegisterProfile(context.Background(), 777, "foreign"); !errors.Is(err, ErrProfileLinkedElsewhere) {
		t.Fatalf("foreign error = %v", err)
	}
}

func TestCheckPlategaPaymentConcealsForeignRequest(t *testing.T) {
	svc, _, _, st := newTestService(t)
	id, err := st.CreatePaymentRequest(context.Background(), store.PaymentRequest{
		RemnawaveID: 1, UUID: "u", Username: "alice", TelegramID: 888,
		Months: 1, Price: 100, ExpireAt: time.Now(), Provider: ProviderPlatega, ProviderTxnID: "tx",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CheckPlategaPayment(context.Background(), 777, id); !errors.Is(err, ErrPaymentRequestInaccessible) {
		t.Fatalf("foreign error = %v", err)
	}
	if _, err := svc.CheckPlategaPayment(context.Background(), 777, id+999); !errors.Is(err, ErrPaymentRequestInaccessible) {
		t.Fatalf("missing error = %v", err)
	}
}

func TestRedeemGiftForOwnedProfileAndRollback(t *testing.T) {
	svc, _, ext, st := newTestService(t)
	now := time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	svc.finder = &fakeFinder{byTG: map[int64][]Subscriber{777: {{RemnawaveID: 42, UUID: "uuid-42", Username: "alice", ExpireAt: now.AddDate(0, 1, 0)}}}}
	g := issueGift(t, st, 999, 3)
	ext.err = errors.New("panel down")
	if _, err := svc.RedeemGift(context.Background(), 777, g.Code, 42, ""); err == nil {
		t.Fatal("expected panel failure")
	}
	after, _ := st.GetGiftCode(context.Background(), g.ID)
	if after.Status != "issued" {
		t.Fatalf("gift status after rollback = %q", after.Status)
	}
	ext.err = nil
	got, err := svc.RedeemGift(context.Background(), 777, g.Code, 42, "")
	if err != nil {
		t.Fatalf("RedeemGift retry: %v", err)
	}
	if got.Username != "alice" || ext.calls != 2 {
		t.Fatalf("result=%+v calls=%d", got, ext.calls)
	}
	if _, err := svc.RedeemGift(context.Background(), 777, g.Code, 42, ""); !errors.Is(err, ErrGiftUsed) {
		t.Fatalf("second redemption = %v", err)
	}
}

func TestRedeemGiftCreatesFirstProfile(t *testing.T) {
	svc, _, _, st := newTestService(t)
	creator := &fakeCreator{}
	registrar := &fakeRegistrar{}
	svc.creator, svc.registrar = creator, registrar
	svc.finder = &fakeFinder{byTG: map[int64][]Subscriber{}, byName: map[string]*Subscriber{}}
	g := issueGift(t, st, 999, 1)
	got, err := svc.RedeemGift(context.Background(), 777, g.Code, 0, "new_user")
	if err != nil {
		t.Fatalf("RedeemGift: %v", err)
	}
	if got.Username != "new_user" || len(creator.created) != 1 || registrar.telegramID != 777 {
		t.Fatalf("result=%+v creator=%+v registrar=%+v", got, creator, registrar)
	}
}

func TestRedeemGiftConcurrentClaim(t *testing.T) {
	svc, _, ext, st := newTestService(t)
	svc.finder = &fakeFinder{byTG: map[int64][]Subscriber{777: {{RemnawaveID: 42, UUID: "u", Username: "alice", ExpireAt: time.Now()}}}}
	g := issueGift(t, st, 999, 1)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.RedeemGift(context.Background(), 777, g.Code, 42, "")
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	success, used := 0, 0
	for err := range errs {
		if err == nil {
			success++
		} else if errors.Is(err, ErrGiftUsed) {
			used++
		} else {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if success != 1 || used != 1 || ext.calls != 1 {
		t.Fatalf("success=%d used=%d extensions=%d", success, used, ext.calls)
	}
}

func TestUploadRenewReceiptUploadsOnceAndReusesFileID(t *testing.T) {
	svc, bot, _, st := newTestServiceTwoAdmins(t)
	ctx := context.Background()
	rememberAlice(t, st)
	svc.startPayPhotoFlow(ctx, 777, 42, 1, 150, store.PlanStandard)
	if err := svc.UploadRenewReceipt(ctx, 777, WebReceipt{Filename: "receipt.png", ContentType: "image/png", Data: []byte("png"), Note: "paid"}); err != nil {
		t.Fatalf("UploadRenewReceipt: %v", err)
	}
	if bot.photoUploads != 1 || len(bot.photos) != 2 {
		t.Fatalf("uploads=%d photos=%+v", bot.photoUploads, bot.photos)
	}
	req, _ := st.GetPaymentRequest(ctx, 1)
	if req == nil || req.ScreenshotFileID != "uploaded-photo-id" || req.ScreenshotIsDocument {
		t.Fatalf("request=%+v", req)
	}
}

func TestUploadRenewReceiptValidationExpiryAndCleanup(t *testing.T) {
	svc, bot, _, st := newTestServiceTwoAdmins(t)
	ctx := context.Background()
	rememberAlice(t, st)
	if err := svc.UploadRenewReceipt(ctx, 777, WebReceipt{Filename: "x.png", Data: []byte("x")}); !errors.Is(err, ErrReceiptSessionExpired) {
		t.Fatalf("no session = %v", err)
	}
	svc.startPayPhotoFlow(ctx, 777, 42, 1, 150, store.PlanStandard)
	if err := svc.UploadRenewReceipt(ctx, 777, WebReceipt{Filename: "x.exe", Data: []byte("x")}); !errors.Is(err, ErrReceiptType) {
		t.Fatalf("type = %v", err)
	}
	bot.sendErrs = map[int64]error{1000: errors.New("down"), 2000: errors.New("down")}
	if err := svc.UploadRenewReceipt(ctx, 777, WebReceipt{Filename: "x.pdf", ContentType: "application/pdf", Data: []byte("pdf")}); err == nil {
		t.Fatal("expected all-admin delivery failure")
	}
	if req, _ := st.GetPaymentRequest(ctx, 1); req != nil {
		t.Fatalf("stranded request=%+v", req)
	}
}

func TestUploadRenewReceiptSizeLimitsAndExpiredState(t *testing.T) {
	svc, _, _, st := newTestService(t)
	ctx := context.Background()
	rememberAlice(t, st)
	base := time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return base }
	svc.startPayPhotoFlow(ctx, 777, 42, 1, 150, store.PlanStandard)
	if err := svc.UploadRenewReceipt(ctx, 777, WebReceipt{Filename: "large.png", Data: make([]byte, maxReceiptPhotoSize+1)}); !errors.Is(err, ErrReceiptTooLarge) {
		t.Fatalf("photo size=%v", err)
	}
	if err := svc.UploadRenewReceipt(ctx, 777, WebReceipt{Filename: "large.pdf", Data: make([]byte, maxReceiptDocumentSize+1)}); !errors.Is(err, ErrReceiptTooLarge) {
		t.Fatalf("document size=%v", err)
	}
	svc.now = func() time.Time { return base.Add(payPhotoTTL + time.Second) }
	if err := svc.UploadRenewReceipt(ctx, 777, WebReceipt{Filename: "ok.png", Data: []byte("x")}); !errors.Is(err, ErrReceiptSessionExpired) {
		t.Fatalf("expired=%v", err)
	}
}

func TestCheckPlategaPaymentStatusesAndRepeat(t *testing.T) {
	svc, _, ext, st := newTestService(t)
	ctx := context.Background()
	gw := &fakePlatega{statuses: map[string]string{"tx": "PENDING"}}
	svc.SetPlatega(gw, 2, "RUB", "https://return")
	id, _ := st.CreatePaymentRequest(ctx, store.PaymentRequest{RemnawaveID: 1, UUID: "u", Username: "alice", TelegramID: 777, Months: 1, Price: 100, ExpireAt: time.Now(), Provider: ProviderPlatega, ProviderTxnID: "tx"})
	got, err := svc.CheckPlategaPayment(ctx, 777, id)
	if err != nil || got.Status != "pending" {
		t.Fatalf("pending=%+v err=%v", got, err)
	}
	gw.statuses["tx"] = "CONFIRMED"
	got, err = svc.CheckPlategaPayment(ctx, 777, id)
	if err != nil || got.Status != "confirmed" {
		t.Fatalf("confirmed=%+v err=%v", got, err)
	}
	if _, err := svc.CheckPlategaPayment(ctx, 777, id); err != nil {
		t.Fatalf("repeat: %v", err)
	}
	if ext.calls != 1 {
		t.Fatalf("extension calls=%d", ext.calls)
	}
}
