package payments

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Nakedjustice/remnaWake/internal/i18n"
	"github.com/Nakedjustice/remnaWake/internal/store"
)

func linkedFinder(tgID int64, username string) *fakeFinder {
	return &fakeFinder{byTG: map[int64][]Subscriber{
		tgID: {{RemnawaveID: 7, UUID: "u-7", Username: username, TelegramID: tgID}},
	}}
}

// adminMsgWith returns the first message sent to the admin chat whose keyboard
// contains a callback with the given prefix.
func adminMsgWith(bot *fakeBot, prefix string) *sentMsg {
	for i := range bot.sent {
		m := &bot.sent[i]
		if m.ChatID != 1000 || m.Keyboard == nil {
			continue
		}
		for _, row := range m.Keyboard.InlineKeyboard {
			for _, b := range row {
				if strings.HasPrefix(b.CallbackData, prefix) {
					return m
				}
			}
		}
	}
	return nil
}

func TestCreateGiftRequestHappyPath(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	svc.finder = linkedFinder(42, "alice")
	_ = st.UpsertTariff(ctx, 3, 450)

	if err := svc.CreateGiftRequest(ctx, 42, 3); err != nil {
		t.Fatalf("CreateGiftRequest: %v", err)
	}

	counts, _ := st.CountGiftCodesByBuyer(ctx, 42)
	if counts["pending"] != 1 {
		t.Fatalf("pending gift count = %d, want 1; counts=%v", counts["pending"], counts)
	}
	gifts, _ := st.ListGiftCodesByBuyer(ctx, 42)
	if len(gifts) != 1 || gifts[0].Months != 3 || gifts[0].Price != 450 || gifts[0].BuyerUsername != "alice" {
		t.Fatalf("unexpected gift row: %+v", gifts)
	}

	if m := adminMsgWith(bot, "gc_ok:"); m == nil {
		t.Fatalf("admin not notified with confirm button: %+v", bot.sent)
	} else if !strings.Contains(m.Text, "alice") || !strings.Contains(m.Text, "3 мес.") {
		t.Fatalf("unexpected admin text: %q", m.Text)
	}

	var buyerConfirmed bool
	for _, m := range bot.sent {
		if m.ChatID == 42 && strings.Contains(m.Text, "Заявка на подарочную подписку") {
			buyerConfirmed = true
		}
	}
	if !buyerConfirmed {
		t.Fatalf("buyer chat confirmation missing: %+v", bot.sent)
	}
}

func TestCreateGiftRequestErrors(t *testing.T) {
	svc, _, _, st := newTestService(t)
	ctx := context.Background()
	svc.finder = linkedFinder(42, "alice")
	_ = st.UpsertTariff(ctx, 3, 450)

	if err := svc.CreateGiftRequest(ctx, 42, 0); !errors.Is(err, ErrBadInput) {
		t.Fatalf("months=0: err=%v, want ErrBadInput", err)
	}
	if err := svc.CreateGiftRequest(ctx, 42, 6); !errors.Is(err, ErrTariffUnknown) {
		t.Fatalf("unknown tariff: err=%v, want ErrTariffUnknown", err)
	}
	if err := svc.CreateGiftRequest(ctx, 99, 3); !errors.Is(err, ErrNotLinked) {
		t.Fatalf("not linked: err=%v, want ErrNotLinked", err)
	}
}

func TestCreateGiftRequestDisabled(t *testing.T) {
	st, _ := store.New(filepath.Join(t.TempDir(), "x.db"))
	defer st.Close()
	svc := New(st, &fakeBot{}, &fakeExtender{}, &fakeCreator{}, &fakeFinder{}, &fakeRegistrar{},
		newFakeSquadLister(), []int64{}, "₽", false, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := svc.CreateGiftRequest(context.Background(), 42, 1); !errors.Is(err, ErrPaymentsDisabled) {
		t.Fatalf("err=%v, want ErrPaymentsDisabled", err)
	}
}

func TestCreateGiftRequestNoTariffsFallback(t *testing.T) {
	svc, _, _, st := newTestService(t)
	ctx := context.Background()
	svc.finder = linkedFinder(42, "alice")

	if err := svc.CreateGiftRequest(ctx, 42, 1); err != nil {
		t.Fatalf("1-month fallback: %v", err)
	}
	gifts, _ := st.ListGiftCodesByBuyer(ctx, 42)
	if len(gifts) != 1 || gifts[0].Price != 0 {
		t.Fatalf("unexpected gift row: %+v", gifts)
	}
}

func TestCreateInviteRequestHappyPath(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	svc.finder = linkedFinder(42, "alice")
	_ = st.UpsertTariff(ctx, 1, 150)

	if err := svc.CreateInviteRequest(ctx, 42, "newbie_01"); err != nil {
		t.Fatalf("CreateInviteRequest: %v", err)
	}

	reqs, _ := st.ListInviteRequestsByInviter(ctx, 42)
	if len(reqs) != 1 || reqs[0].NewUsername != "newbie_01" || reqs[0].Price != 150 || reqs[0].Status != "pending" {
		t.Fatalf("unexpected invite row: %+v", reqs)
	}

	if m := adminMsgWith(bot, "inv_ok:"); m == nil {
		t.Fatalf("admin not notified with approve button: %+v", bot.sent)
	} else if !strings.Contains(m.Text, "newbie_01") {
		t.Fatalf("unexpected admin text: %q", m.Text)
	}

	var inviterConfirmed bool
	for _, m := range bot.sent {
		if m.ChatID == 42 && strings.Contains(m.Text, "newbie_01") {
			inviterConfirmed = true
		}
	}
	if !inviterConfirmed {
		t.Fatalf("inviter chat confirmation missing: %+v", bot.sent)
	}
}

func TestCreateInviteRequestErrors(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	ctx := context.Background()
	svc.finder = linkedFinder(42, "alice")

	for _, bad := range []string{"", "ab", "with space", strings.Repeat("x", 33)} {
		if err := svc.CreateInviteRequest(ctx, 42, bad); !errors.Is(err, ErrBadInput) {
			t.Errorf("username %q: err=%v, want ErrBadInput", bad, err)
		}
	}
	if err := svc.CreateInviteRequest(ctx, 99, "newbie_01"); !errors.Is(err, ErrNotLinked) {
		t.Fatalf("not linked: err=%v, want ErrNotLinked", err)
	}
}

func TestCabinetDataExposesLang(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	ctx := context.Background()
	svc.finder = linkedFinder(42, "alice")

	i18n.SetLang(i18n.EN)
	t.Cleanup(func() { i18n.SetLang(i18n.RU) })

	data, err := svc.CabinetData(ctx, 42)
	if err != nil {
		t.Fatalf("CabinetData: %v", err)
	}
	if data.Lang != "en" {
		t.Fatalf("Lang = %q, want en", data.Lang)
	}
}

func TestCabinetDataIssuedGiftHasLink(t *testing.T) {
	svc, _, _, st := newTestService(t)
	ctx := context.Background()
	svc.finder = linkedFinder(42, "alice")
	svc.SetBotUsername("testbot")

	id, _ := st.CreateGiftCode(ctx, store.GiftCode{
		Code: "ABCDEFGHJKMNPQRS", BuyerTelegramID: 42, BuyerUsername: "alice", Months: 3,
	})
	if ok, err := st.IssueGiftCode(ctx, id, svc.now()); err != nil || !ok {
		t.Fatalf("issue: ok=%v err=%v", ok, err)
	}

	data, err := svc.CabinetData(ctx, 42)
	if err != nil {
		t.Fatalf("CabinetData: %v", err)
	}
	if len(data.Gifts) != 1 {
		t.Fatalf("gifts = %+v", data.Gifts)
	}
	want := "https://t.me/testbot?start=gift_ABCDEFGHJKMNPQRS"
	if data.Gifts[0].Link != want {
		t.Fatalf("link = %q, want %q", data.Gifts[0].Link, want)
	}

	// Pending gifts carry no link.
	_, _ = st.CreateGiftCode(ctx, store.GiftCode{
		Code: "ZZZZZZZZZZZZZZZZ", BuyerTelegramID: 42, BuyerUsername: "alice", Months: 1,
	})
	data, _ = svc.CabinetData(ctx, 42)
	for _, g := range data.Gifts {
		if g.Status == "pending" && g.Link != "" {
			t.Fatalf("pending gift has link: %+v", g)
		}
	}
}
