package payments

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/store"
)

// ownerGateAnswer is the single answer every profile-scoped callback gives when
// the presser does not own the target, mirroring the mini app's anti-probing
// choice in CheckPlategaPayment.
const ownerGateAnswer = "Не удалось найти данные. Дождитесь следующего уведомления."

// seedOwnerGateFixtures gives remnawave profile 42 to telegram 777 plus the
// tariffs and preset the payment callbacks need to get past their own parsing.
func seedOwnerGateFixtures(t *testing.T, st *store.Store) {
	t.Helper()
	ctx := context.Background()
	rememberAlice(t, st) // remnawave 42 -> telegram 777
	if err := st.UpsertTariff(ctx, store.PlanStandard, 3, 450); err != nil {
		t.Fatalf("seed standard tariff: %v", err)
	}
	if err := st.UpsertPlan(ctx, store.Plan{Code: "messenger", Name: "Мессенджер"}); err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	if err := st.UpsertTariff(ctx, "messenger", 3, 250); err != nil {
		t.Fatalf("seed preset tariff: %v", err)
	}
}

func TestForeignPresserCannotActOnAnotherProfile(t *testing.T) {
	for _, data := range []string{
		"pay:42",
		"pln:42:messenger",
		"pick:42:3",
		"pick:42:3:messenger",
		"payvia:42:3:p2p",
		"chg:42:3:standard",
		"chgv:42:3:p2p:standard",
		"cab:pay:42",
	} {
		t.Run(data, func(t *testing.T) {
			svc, bot, _, st := newTestService(t)
			ctx := context.Background()
			seedOwnerGateFixtures(t, st)

			if !svc.HandleCallback(ctx, cbq(888, data)) {
				t.Fatalf("%s should be handled", data)
			}
			if reqs, _ := st.ListPaymentRequestsByStatus(ctx, "pending"); len(reqs) != 0 {
				t.Fatalf("foreign presser created payment requests: %+v", reqs)
			}
			if len(bot.edits) != 0 {
				t.Fatalf("foreign presser changed a keyboard: %+v", bot.edits)
			}
			if len(bot.sent) != 0 {
				t.Fatalf("foreign presser triggered messages: %+v", bot.sent)
			}
			if len(bot.answers) != 1 || bot.answers[0] != ownerGateAnswer {
				t.Fatalf("answers = %q, want [%q]", bot.answers, ownerGateAnswer)
			}
		})
	}
}

// TestForeignAndUnknownProfileShareOneAnswer is the bot-side mirror of
// TestCheckPlategaPaymentConcealsForeignRequest: a foreign profile must be
// indistinguishable from one that does not exist.
func TestForeignAndUnknownProfileShareOneAnswer(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	seedOwnerGateFixtures(t, st)

	if !svc.HandleCallback(ctx, cbq(888, "pick:42:3")) {
		t.Fatal("foreign pick should be handled")
	}
	if !svc.HandleCallback(ctx, cbq(888, "pick:4242:3")) {
		t.Fatal("unknown pick should be handled")
	}
	if len(bot.answers) != 2 {
		t.Fatalf("answers = %q, want two", bot.answers)
	}
	if bot.answers[0] != bot.answers[1] {
		t.Fatalf("foreign answer %q differs from unknown answer %q", bot.answers[0], bot.answers[1])
	}
}

// TestUnlinkedProfileCallbackIsDenied covers the snapshot with no Telegram
// link: there is no owner to compare against, so every presser is refused.
func TestUnlinkedProfileCallbackIsDenied(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	if err := st.UpsertNotifiedUser(ctx, store.NotifiedUser{
		RemnawaveID: 51, UUID: "uuid-51", Username: "orphan",
		ExpireAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("remember unlinked: %v", err)
	}
	if err := st.UpsertTariff(ctx, store.PlanStandard, 3, 450); err != nil {
		t.Fatalf("seed tariff: %v", err)
	}

	// 0 is the presser whose own id matches the missing link — it must be
	// refused just like anybody else.
	for _, from := range []int64{777, 0} {
		bot.answers = nil
		if !svc.HandleCallback(ctx, cbq(from, "pick:51:3")) {
			t.Fatalf("pick from %d should be handled", from)
		}
		if len(bot.answers) != 1 || bot.answers[0] != ownerGateAnswer {
			t.Fatalf("answers for presser %d = %q, want [%q]", from, bot.answers, ownerGateAnswer)
		}
	}
	if reqs, _ := st.ListPaymentRequestsByStatus(ctx, "pending"); len(reqs) != 0 {
		t.Fatalf("unlinked profile got payment requests: %+v", reqs)
	}
}

func TestPlategaCheckButtonConcealsForeignRequest(t *testing.T) {
	svc, bot, ext, st := newTestService(t)
	ctx := context.Background()
	gw := &fakePlatega{nextID: "tx-11", statuses: map[string]string{"tx-11": "CONFIRMED"}}
	svc.SetPlatega(gw, 2, "RUB", "https://t.me")

	u := &store.NotifiedUser{RemnawaveID: 7, UUID: "u-7", Username: "bob", TelegramID: 555,
		ExpireAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)}
	reqID, _, err := svc.startPlategaPayment(ctx, u, 1, 150, store.PlanStandard)
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	if !svc.HandleCallback(ctx, cbq(888, "plcheck:"+strconv.FormatInt(reqID, 10))) {
		t.Fatal("foreign plcheck should be handled")
	}
	if !svc.HandleCallback(ctx, cbq(888, "plcheck:"+strconv.FormatInt(reqID+999, 10))) {
		t.Fatal("missing plcheck should be handled")
	}
	if len(bot.answers) != 2 {
		t.Fatalf("answers = %q, want two", bot.answers)
	}
	if bot.answers[0] != bot.answers[1] {
		t.Fatalf("foreign answer %q differs from missing answer %q", bot.answers[0], bot.answers[1])
	}
	if ext.calls != 0 {
		t.Fatalf("foreign plcheck extended the subscription: calls=%d", ext.calls)
	}
	if got, _ := st.GetPaymentRequest(ctx, reqID); got.Status != "pending" {
		t.Fatalf("status = %q, want pending", got.Status)
	}
}

// TestPlategaCheckIgnoresNonPlategaRequest keeps the button's reachability
// identical to CheckPlategaPayment: a p2p request has no transaction to verify,
// and its id must not be confirmable (or probeable) through plcheck.
func TestPlategaCheckIgnoresNonPlategaRequest(t *testing.T) {
	svc, bot, ext, st := newTestService(t)
	ctx := context.Background()
	svc.SetPlatega(&fakePlatega{nextID: "tx-12"}, 2, "RUB", "https://t.me")

	reqID, err := st.CreatePaymentRequest(ctx, store.PaymentRequest{
		RemnawaveID: 42, UUID: "uuid-42", Username: "alice", TelegramID: 777,
		Months: 1, Price: 100, ExpireAt: time.Now(), Status: "pending", Provider: ProviderP2P,
	})
	if err != nil {
		t.Fatalf("create p2p request: %v", err)
	}

	if !svc.HandleCallback(ctx, cbq(777, "plcheck:"+strconv.FormatInt(reqID, 10))) {
		t.Fatal("p2p plcheck should be handled")
	}
	if !svc.HandleCallback(ctx, cbq(777, "plcheck:"+strconv.FormatInt(reqID+999, 10))) {
		t.Fatal("missing plcheck should be handled")
	}
	if len(bot.answers) != 2 || bot.answers[0] != bot.answers[1] {
		t.Fatalf("p2p answer must match the missing answer: %q", bot.answers)
	}
	if ext.calls != 0 {
		t.Fatalf("p2p plcheck extended the subscription: calls=%d", ext.calls)
	}
	if got, _ := st.GetPaymentRequest(ctx, reqID); got.Status != "pending" {
		t.Fatalf("status = %q, want pending", got.Status)
	}
}
