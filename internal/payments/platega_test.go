package payments

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/store"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

// newServiceOverStore builds a fresh Service backed by an existing store, used
// to verify that persisted settings survive a "restart".
func newServiceOverStore(t *testing.T, st *store.Store) *Service {
	t.Helper()
	return New(st, &fakeBot{}, &fakeExtender{}, &fakeCreator{}, &fakeFinder{}, &fakeRegistrar{},
		newFakeSquadLister(), []int64{1000}, "₽", false, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// fakePlatega is a stand-in for *platega.Client. It records the last created
// transaction and lets a test fix the status returned by GetTransaction.
type fakePlatega struct {
	createCalls int
	lastAmount  float64
	lastPayload string
	nextID      string
	nextURL     string
	createErr   error

	statuses map[string]string // txn id -> status
	getErr   error
}

func (f *fakePlatega) CreateTransaction(_ context.Context, _ int, amount float64, _, _, _, payload string) (string, string, error) {
	f.createCalls++
	if f.createErr != nil {
		return "", "", f.createErr
	}
	f.lastAmount = amount
	f.lastPayload = payload
	id := f.nextID
	if id == "" {
		id = "tx-test"
	}
	url := f.nextURL
	if url == "" {
		url = "https://pay.example/x"
	}
	return id, url, nil
}

func (f *fakePlatega) GetTransaction(_ context.Context, id string) (string, error) {
	if f.getErr != nil {
		return "", f.getErr
	}
	return f.statuses[id], nil
}

func TestEnabledProvidersDefaultsToP2P(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	got := svc.enabledProviders()
	if len(got) != 1 || got[0] != ProviderP2P {
		t.Fatalf("enabledProviders = %v, want [p2p]", got)
	}
}

func TestEnableProviderRequiresConfiguredGateway(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	ctx := context.Background()

	// Enabling platega without a configured gateway is rejected.
	if err := svc.setProviderEnabled(ctx, ProviderPlatega, true); !errors.Is(err, ErrBadInput) {
		t.Fatalf("setProviderEnabled(platega) unconfigured = %v, want ErrBadInput", err)
	}
	if got := svc.enabledProviders(); len(got) != 1 || got[0] != ProviderP2P {
		t.Fatalf("enabledProviders = %v, want [p2p]", got)
	}

	// Once configured, the toggle takes effect and platega becomes available.
	svc.SetPlatega(&fakePlatega{}, 2, "RUB", "https://t.me")
	if err := svc.setProviderEnabled(ctx, ProviderPlatega, true); err != nil {
		t.Fatalf("setProviderEnabled(platega): %v", err)
	}
	if !contains(svc.enabledProviders(), ProviderPlatega) {
		t.Fatalf("enabledProviders = %v, want to include platega", svc.enabledProviders())
	}

	// The setting persists across restarts (a fresh Service over the same store).
	svc2 := newServiceOverStore(t, svc.store)
	if !svc2.isProviderEnabled(ProviderPlatega) {
		t.Fatalf("reloaded isProviderEnabled(platega) = false, want true")
	}
	// But without a gateway wired in, platega is filtered out of the effective set.
	if contains(svc2.enabledProviders(), ProviderPlatega) {
		t.Fatalf("reloaded enabledProviders = %v, want platega filtered out", svc2.enabledProviders())
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func TestStartPlategaPaymentCreatesRequestAndReturnsURL(t *testing.T) {
	svc, _, _, st := newTestService(t)
	ctx := context.Background()
	gw := &fakePlatega{nextID: "tx-1", nextURL: "https://pay.example/tx-1"}
	svc.SetPlatega(gw, 2, "RUB", "https://t.me")

	u := &store.NotifiedUser{RemnawaveID: 7, UUID: "u-7", Username: "bob", TelegramID: 555,
		ExpireAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)}
	_, url, err := svc.startPlategaPayment(ctx, u, 2, 300)
	if err != nil {
		t.Fatalf("startPlategaPayment: %v", err)
	}
	if url != "https://pay.example/tx-1" {
		t.Fatalf("redirect url = %q", url)
	}
	if gw.lastAmount != 300 {
		t.Fatalf("amount = %v, want 300", gw.lastAmount)
	}

	// A pending platega request was stored with the transaction id attached.
	got, err := st.GetPaymentRequestByProviderTxn(ctx, "tx-1")
	if err != nil || got == nil {
		t.Fatalf("lookup by txn: %v %+v", err, got)
	}
	if got.Provider != ProviderPlatega || got.Status != "pending" || got.Months != 2 {
		t.Fatalf("unexpected request: %+v", got)
	}
	// The payload encodes the request id so the webhook can correlate it.
	if !strings.Contains(gw.lastPayload, plategaPayloadPrefix) {
		t.Fatalf("payload %q missing prefix %q", gw.lastPayload, plategaPayloadPrefix)
	}
}

func TestPlategaWebhookConfirmsAndIsIdempotent(t *testing.T) {
	svc, bot, ext, st := newTestService(t)
	ctx := context.Background()
	gw := &fakePlatega{nextID: "tx-9", nextURL: "https://pay/x", statuses: map[string]string{}}
	svc.SetPlatega(gw, 2, "RUB", "https://t.me")

	u := &store.NotifiedUser{RemnawaveID: 7, UUID: "u-7", Username: "bob", TelegramID: 555,
		ExpireAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)}
	if _, _, err := svc.startPlategaPayment(ctx, u, 1, 150); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Not yet paid: webhook is a no-op, request stays pending.
	gw.statuses["tx-9"] = "PENDING"
	if err := svc.HandlePlategaWebhook(ctx, []byte(`{"transactionId":"tx-9"}`)); err != nil {
		t.Fatalf("webhook pending: %v", err)
	}
	if ext.calls != 0 {
		t.Fatalf("extend called %d times before CONFIRMED", ext.calls)
	}

	// Paid: webhook extends + confirms + notifies the user.
	gw.statuses["tx-9"] = "CONFIRMED"
	if err := svc.HandlePlategaWebhook(ctx, []byte(`{"id":"tx-9"}`)); err != nil {
		t.Fatalf("webhook confirmed: %v", err)
	}
	if ext.calls != 1 {
		t.Fatalf("extend calls = %d, want 1", ext.calls)
	}
	got, _ := st.GetPaymentRequestByProviderTxn(ctx, "tx-9")
	if got.Status != "confirmed" {
		t.Fatalf("status = %q, want confirmed", got.Status)
	}
	if len(bot.sent) == 0 {
		t.Fatal("expected a confirmation message to the user")
	}

	// Second webhook for the same txn must not extend again (dedup).
	if err := svc.HandlePlategaWebhook(ctx, []byte(`{"id":"tx-9"}`)); err != nil {
		t.Fatalf("webhook repeat: %v", err)
	}
	if ext.calls != 1 {
		t.Fatalf("extend calls after repeat = %d, want 1", ext.calls)
	}
}

func TestPlategaCheckButtonConfirms(t *testing.T) {
	svc, _, ext, st := newTestService(t)
	ctx := context.Background()
	gw := &fakePlatega{nextID: "tx-5", statuses: map[string]string{"tx-5": "CONFIRMED"}}
	svc.SetPlatega(gw, 2, "RUB", "https://t.me")

	u := &store.NotifiedUser{RemnawaveID: 7, UUID: "u-7", Username: "bob", TelegramID: 555,
		ExpireAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)}
	if _, _, err := svc.startPlategaPayment(ctx, u, 1, 150); err != nil {
		t.Fatalf("start: %v", err)
	}

	reqByTxn, _ := st.GetPaymentRequestByProviderTxn(ctx, "tx-5")
	cb := &tg.CallbackQuery{ID: "cb1", From: tg.User{ID: 555},
		Message: &tg.Message{MessageID: 1, Chat: tg.Chat{ID: 555}},
		Data:    "plcheck:" + strconv.FormatInt(reqByTxn.ID, 10)}
	if !svc.HandleCallback(ctx, cb) {
		t.Fatal("plcheck callback should be handled")
	}
	if ext.calls != 1 {
		t.Fatalf("extend calls = %d, want 1", ext.calls)
	}
}
