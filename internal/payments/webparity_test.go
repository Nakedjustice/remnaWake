package payments

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
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
				byName:  map[string]*Subscriber{"alice": {RemnawaveID: 42, Username: "alice"}},
				byShort: map[string]*Subscriber{"short-42": {RemnawaveID: 42, Username: "alice"}},
			}
			registrar := &fakeRegistrar{}
			svc.finder, svc.registrar = finder, registrar

			got, err := svc.RegisterProfile(context.Background(), 777, tc.query)
			if err != nil {
				t.Fatalf("RegisterProfile: %v", err)
			}
			if got.Username != "alice" || registrar.userID != 42 || registrar.telegramID != 777 {
				t.Fatalf("unexpected result=%+v registrar=%+v", got, registrar)
			}
		})
	}
}

func TestRegisterProfileIsIdempotentAndConcealsForeignLink(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	svc.finder = &fakeFinder{byName: map[string]*Subscriber{
		"mine":    {Username: "mine", TelegramID: 777},
		"foreign": {Username: "foreign", TelegramID: 888},
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
		RemnawaveID: 1, Username: "alice", TelegramID: 888,
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
	svc.finder = &fakeFinder{byTG: map[int64][]Subscriber{777: {{RemnawaveID: 42, Username: "alice", ExpireAt: now.AddDate(0, 1, 0)}}}}
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
	svc.finder = &fakeFinder{byTG: map[int64][]Subscriber{777: {{RemnawaveID: 42, Username: "alice", ExpireAt: time.Now()}}}}
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
	id, _ := st.CreatePaymentRequest(ctx, store.PaymentRequest{RemnawaveID: 1, Username: "alice", TelegramID: 777, Months: 1, Price: 100, ExpireAt: time.Now(), Provider: ProviderPlatega, ProviderTxnID: "tx"})
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

// Device self-management has to work identically with WEBAPP_URL unset: the
// dev:* callbacks and the Mini App methods must observe the same slots and
// mutate the panel the same way, so a user is never told to "open the app".
func TestDeviceParityBetweenBotAndMiniApp(t *testing.T) {
	ctx := context.Background()

	fromMiniApp, _, miniDevices := deviceService(t)
	if _, err := fromMiniApp.RevokeDevice(ctx, 777, 42, "hw-phone"); err != nil {
		t.Fatalf("mini app revoke: %v", err)
	}

	fromBot, _, botDevices := deviceService(t)
	if !fromBot.HandleCallback(ctx, cbq(777, "dev:list")) {
		t.Fatal("dev:list should be handled")
	}
	if !fromBot.HandleCallback(ctx, cbq(777, "dev:rev:0:ok")) {
		t.Fatal("dev:rev:ok should be handled")
	}

	if len(miniDevices.deleted) != 1 || len(botDevices.deleted) != 1 ||
		miniDevices.deleted[0] != botDevices.deleted[0] {
		t.Fatalf("panel calls differ: mini app=%v bot=%v", miniDevices.deleted, botDevices.deleted)
	}

	// Both surfaces then read the same overview back.
	miniView, err := fromMiniApp.DeviceOverview(ctx, 777)
	if err != nil {
		t.Fatalf("mini app overview: %v", err)
	}
	botView, err := fromBot.DeviceOverview(ctx, 777)
	if err != nil {
		t.Fatalf("bot overview: %v", err)
	}
	if !reflect.DeepEqual(miniView, botView) {
		t.Fatalf("overview differs:\nmini app=%+v\nbot=%+v", miniView, botView)
	}
	if miniView.Profiles[0].Used != 1 || miniView.Profiles[0].Full {
		t.Fatalf("freed slot not reflected: %+v", miniView.Profiles[0])
	}
}

// The mini app admin panel and the adm:backup:* callbacks must drive the same
// schedule: whichever surface an admin uses, the persisted state, the runtime
// cache and the reset-on-change rule have to come out identical.
func TestBackupScheduleParityBetweenBotAndMiniApp(t *testing.T) {
	ctx := context.Background()

	fromMiniApp, _, _, miniStore := newTestService(t)
	fromMiniApp.InitBackupConfig(false, 1)
	if err := fromMiniApp.AdminSetBackupSchedule(ctx, adminTG, true, 7); err != nil {
		t.Fatalf("mini app set schedule: %v", err)
	}

	fromBot, _, _, botStore := newTestService(t)
	fromBot.InitBackupConfig(false, 1)
	fromBot.handleBackupToggle(ctx, adminTG)
	fromBot.applyBackupInterval(ctx, adminTG, "7")

	miniEnabled, miniInterval := fromMiniApp.backupConfig()
	botEnabled, botInterval := fromBot.backupConfig()
	if miniEnabled != botEnabled || miniInterval != botInterval {
		t.Fatalf("runtime config differs: mini app=%v/%d bot=%v/%d",
			miniEnabled, miniInterval, botEnabled, botInterval)
	}
	if !miniEnabled || miniInterval != 7 {
		t.Fatalf("schedule = enabled:%v interval:%d, want true/7", miniEnabled, miniInterval)
	}
	for _, key := range []string{settingBackupEnabled, settingBackupIntervalDays} {
		miniValue, miniFound, err := miniStore.GetSetting(ctx, key)
		if err != nil || !miniFound {
			t.Fatalf("mini app %s: found=%v err=%v", key, miniFound, err)
		}
		botValue, botFound, err := botStore.GetSetting(ctx, key)
		if err != nil || !botFound {
			t.Fatalf("bot %s: found=%v err=%v", key, botFound, err)
		}
		if miniValue != botValue {
			t.Fatalf("%s persisted as %q from the mini app and %q from the bot", key, miniValue, botValue)
		}
	}

	// The panel payload reports the same schedule the bot card renders.
	panel, err := fromMiniApp.AdminPanelData(ctx, adminTG)
	if err != nil {
		t.Fatalf("panel: %v", err)
	}
	if !panel.BackupEnabled || panel.BackupIntervalDays != 7 || panel.BackupMaxIntervalDays != maxBackupIntervalDays {
		t.Fatalf("panel backup fields = %+v", panel)
	}

	// Out-of-range input is refused on both surfaces.
	if err := fromMiniApp.AdminSetBackupSchedule(ctx, adminTG, true, maxBackupIntervalDays+1); !errors.Is(err, ErrBadInput) {
		t.Fatalf("above ceiling: err = %v, want ErrBadInput", err)
	}
	if err := fromMiniApp.AdminSetBackupSchedule(ctx, adminTG, true, 0); !errors.Is(err, ErrBadInput) {
		t.Fatalf("zero interval: err = %v, want ErrBadInput", err)
	}
	if _, interval := fromMiniApp.backupConfig(); interval != 7 {
		t.Fatalf("rejected input changed the interval to %d", interval)
	}
}

// The multi-ticket support surface must be complete on both sides: the same
// conversation driven through the bot's sup:* / adm:sup:* buttons and through
// the Web* methods the mini app calls has to leave identical tickets behind.
func TestSupportTicketParityBetweenBotAndMiniApp(t *testing.T) {
	ctx := context.Background()
	const (
		opening = "Не работает VPN\nвторой день"
		reply   = "смотрим"
	)

	// snapshot is everything either surface can show about the conversation.
	snapshot := func(t *testing.T, svc *Service, ticketID int64) string {
		t.Helper()
		list, err := svc.SupportTicketsUser(ctx, supportUser)
		if err != nil {
			t.Fatalf("ticket list: %v", err)
		}
		thread, err := svc.SupportTicketThreadUser(ctx, supportUser, ticketID)
		if err != nil {
			t.Fatalf("ticket thread: %v", err)
		}
		stored, err := svc.store.GetSupportTicket(ctx, ticketID)
		if err != nil || stored == nil {
			t.Fatalf("stored ticket: %+v err %v", stored, err)
		}
		var b strings.Builder
		fmt.Fprintf(&b, "tickets=%d\n", len(list.Tickets))
		for _, tk := range list.Tickets {
			fmt.Fprintf(&b, "summary|%s|%s|%s|%s|%s|%d\n",
				tk.Subject, tk.Status, tk.StatusLabel, tk.State, tk.LastText, tk.Unread)
		}
		for _, m := range thread.Messages {
			fmt.Fprintf(&b, "message|%t|%s\n", m.FromAdmin, m.Text)
		}
		fmt.Fprintf(&b, "closed_by=%s state=%s", stored.ClosedBy, thread.Ticket.State)
		return b.String()
	}

	fromBot, _ := newSupportService(t)
	supportTap(t, fromBot, supportUser, "sup:new")
	if !supportType(fromBot, supportUser, opening) {
		t.Fatal("bot: the new-ticket text was not consumed")
	}
	botTicket, err := fromBot.store.LatestOpenTicketForUser(ctx, supportUser)
	if err != nil || botTicket == nil {
		t.Fatalf("bot ticket: %+v err %v", botTicket, err)
	}
	supportTicketTap(t, fromBot, adminTG, "adm:sup:re:", botTicket.ID)
	if !supportTypeAdmin(fromBot, adminTG, reply) {
		t.Fatal("bot: the admin reply was not consumed")
	}
	supportTicketTap(t, fromBot, supportUser, "sup:cl:", botTicket.ID)

	fromMiniApp, _ := newSupportService(t)
	webTicket, err := fromMiniApp.SupportCreateTicket(ctx, supportUser, "", opening)
	if err != nil {
		t.Fatalf("mini app create: %v", err)
	}
	if err := fromMiniApp.SupportReplyTicketAdmin(ctx, adminTG, webTicket, reply); err != nil {
		t.Fatalf("mini app reply: %v", err)
	}
	if err := fromMiniApp.SupportCloseTicketUser(ctx, supportUser, webTicket); err != nil {
		t.Fatalf("mini app close: %v", err)
	}

	if got, want := snapshot(t, fromBot, botTicket.ID), snapshot(t, fromMiniApp, webTicket); got != want {
		t.Fatalf("the two surfaces disagree:\nbot:\n%s\nmini app:\n%s", got, want)
	}

	// Both surfaces also refuse the same follow-up on a closed ticket.
	if err := fromMiniApp.SupportSendUserToTicket(ctx, supportUser, webTicket, "ещё"); !errors.Is(err, ErrTicketClosed) {
		t.Fatalf("mini app reply to a closed ticket: %v", err)
	}
	supportTicketTap(t, fromBot, supportUser, "sup:re:", botTicket.ID)
	if supportType(fromBot, supportUser, "ещё") {
		t.Fatal("bot: a closed ticket must not accept a reply")
	}
	if msgs, _ := fromBot.store.ListSupportMessagesByTicket(ctx, botTicket.ID); len(msgs) != 2 {
		t.Fatalf("bot ticket grew after the close: %+v", msgs)
	}
}
