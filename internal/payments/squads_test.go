package payments

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Nakedjustice/remnaWake/internal/store"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

func TestResolveDefaultSquadPrefersSelectedSetting(t *testing.T) {
	svc, _, _, st := newTestService(t)
	ctx := context.Background()
	if err := st.UpsertSetting(ctx, defaultSquadUUIDKey, "picked-uuid"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	uuid, err := svc.resolveDefaultSquadUUID(ctx)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if uuid != "picked-uuid" {
		t.Fatalf("uuid = %q, want picked-uuid", uuid)
	}
	if svc.squads.(*fakeSquadLister).calls != 0 {
		t.Fatal("must not hit the panel when a squad is selected")
	}
}

func TestResolveDefaultSquadFallbackIsCaseInsensitiveAndCached(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	ctx := context.Background()
	lister := svc.squads.(*fakeSquadLister)
	lister.squads = []InternalSquad{
		{UUID: "sq-other", Name: "Premium"},
		{UUID: "sq-def", Name: "default-squad"},
	}

	for i := 0; i < 2; i++ {
		uuid, err := svc.resolveDefaultSquadUUID(ctx)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if uuid != "sq-def" {
			t.Fatalf("uuid = %q, want sq-def", uuid)
		}
	}
	if lister.calls != 1 {
		t.Fatalf("fallback lookup should be cached, panel calls = %d", lister.calls)
	}
}

func TestResolveDefaultSquadFailsWhenNothingMatches(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	svc.squads.(*fakeSquadLister).squads = []InternalSquad{{UUID: "sq-1", Name: "Premium"}}

	if _, err := svc.resolveDefaultSquadUUID(context.Background()); err == nil {
		t.Fatal("expected error when no Default-Squad exists and nothing is selected")
	}
}

func TestSetDefaultSquadPersistsAndClearsCache(t *testing.T) {
	svc, _, _, st := newTestService(t)
	ctx := context.Background()
	lister := svc.squads.(*fakeSquadLister)
	lister.squads = []InternalSquad{
		{UUID: "sq-def", Name: "Default-Squad"},
		{UUID: "sq-prem", Name: "Premium"},
	}
	// Warm the fallback cache first, then verify selection overrides it.
	if _, err := svc.resolveDefaultSquadUUID(ctx); err != nil {
		t.Fatalf("warm cache: %v", err)
	}

	sq, err := svc.setDefaultSquad(ctx, "sq-prem")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if sq.Name != "Premium" {
		t.Fatalf("squad = %+v, want Premium", sq)
	}
	if got, found, _ := st.GetSetting(ctx, defaultSquadUUIDKey); !found || got != "sq-prem" {
		t.Fatalf("uuid not persisted: %q", got)
	}
	if got, found, _ := st.GetSetting(ctx, defaultSquadNameKey); !found || got != "Premium" {
		t.Fatalf("name not persisted: %q", got)
	}
	uuid, err := svc.resolveDefaultSquadUUID(ctx)
	if err != nil || uuid != "sq-prem" {
		t.Fatalf("resolution after selection = %q (%v), want sq-prem", uuid, err)
	}
}

func TestSetDefaultSquadRejectsUnknownUUID(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	if _, err := svc.setDefaultSquad(context.Background(), "no-such"); !errors.Is(err, ErrBadInput) {
		t.Fatalf("err = %v, want ErrBadInput", err)
	}
}

func TestSetDefaultSquadPanelError(t *testing.T) {
	svc, _, _, _ := newTestService(t)
	svc.squads.(*fakeSquadLister).err = fmt.Errorf("boom")
	if _, err := svc.setDefaultSquad(context.Background(), "sq-1"); !errors.Is(err, ErrPanelUnavailable) {
		t.Fatalf("err = %v, want ErrPanelUnavailable", err)
	}
}

func TestRedeemCreateFailsLoudlyWhenSquadUnresolvable(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	creator := &fakeCreator{}
	svc.creator = creator
	svc.finder = &fakeFinder{byTG: map[int64][]Subscriber{}, byName: map[string]*Subscriber{}}
	svc.squads.(*fakeSquadLister).err = fmt.Errorf("panel down")
	g := issueGift(t, st, 555, 1)

	svc.StartGiftRedemption(ctx, 700, g.Code)
	if !svc.HandleText(ctx, msg(700, "newbie")) {
		t.Fatal("username should be consumed")
	}
	if len(creator.created) != 0 {
		t.Fatalf("user must not be created without a squad: %+v", creator.created)
	}
	if !strings.Contains(bot.sent[len(bot.sent)-1].Text, "Ошибка активации подарка") {
		t.Fatalf("expected visible error: %+v", bot.sent[len(bot.sent)-1])
	}
	// The code rolls back to issued so the redeemer can retry later.
	if got, _ := st.GetGiftCode(ctx, g.ID); got.Status != "issued" {
		t.Fatalf("code should be reissued: %+v", got)
	}
}

func TestInviteApproveFailsWhenSquadUnresolvable(t *testing.T) {
	svc, _, _, st := newTestService(t)
	ctx := context.Background()
	svc.squads.(*fakeSquadLister).squads = nil // no Default-Squad, nothing selected
	reqID, err := st.CreateInviteRequest(ctx, store.InviteRequest{
		InviterTelegramID: 555, InviterUsername: "inviter", NewUsername: "newbie", Months: 1,
	})
	if err != nil {
		t.Fatalf("seed invite: %v", err)
	}

	_, _, _, err = svc.approveInviteRequest(ctx, reqID)
	if !errors.Is(err, ErrPanelCreateFailed) {
		t.Fatalf("err = %v, want ErrPanelCreateFailed", err)
	}
	if req, _ := st.GetInviteRequest(ctx, reqID); req.Status != "pending" {
		t.Fatalf("request must stay pending for retry: %+v", req)
	}
}

func TestInviteApprovePassesSquadToCreate(t *testing.T) {
	svc, _, _, st := newTestService(t)
	ctx := context.Background()
	creator := &fakeCreator{}
	svc.creator = creator
	if err := st.UpsertSetting(ctx, defaultSquadUUIDKey, "picked-uuid"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	reqID, err := st.CreateInviteRequest(ctx, store.InviteRequest{
		InviterTelegramID: 555, InviterUsername: "inviter", NewUsername: "newbie", Months: 1,
	})
	if err != nil {
		t.Fatalf("seed invite: %v", err)
	}

	if _, _, _, err := svc.approveInviteRequest(ctx, reqID); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if len(creator.squads) != 1 || len(creator.squads[0]) != 1 || creator.squads[0][0] != "picked-uuid" {
		t.Fatalf("selected squad not passed to create: %+v", creator.squads)
	}
}

func TestInviteApproveRejectsLegacyInvalidUsername(t *testing.T) {
	svc, _, _, st := newTestService(t)
	ctx := context.Background()
	creator := &fakeCreator{}
	svc.creator = creator
	reqID, err := st.CreateInviteRequest(ctx, store.InviteRequest{
		InviterTelegramID: 555, InviterUsername: "inviter", NewUsername: "профиль", Months: 1,
	})
	if err != nil {
		t.Fatalf("seed legacy invite: %v", err)
	}

	if _, _, _, err := svc.approveInviteRequest(ctx, reqID); !errors.Is(err, ErrInvalidUsername) {
		t.Fatalf("approve invalid legacy invite err=%v, want ErrInvalidUsername", err)
	}
	if len(creator.created) != 0 {
		t.Fatalf("invalid legacy invite reached profile creator: %+v", creator.created)
	}
	if req, _ := st.GetInviteRequest(ctx, reqID); req == nil || req.Status != "rejected" {
		t.Fatalf("invalid legacy invite was not rejected: %+v", req)
	}
}

func TestAdmSquadCallbackShowsPicker(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	lister := svc.squads.(*fakeSquadLister)
	lister.squads = []InternalSquad{
		{UUID: "sq-def", Name: "Default-Squad"},
		{UUID: "sq-prem", Name: "Premium"},
	}
	if err := st.UpsertSetting(ctx, defaultSquadUUIDKey, "sq-prem"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	cb := &tg.CallbackQuery{ID: "cb1", From: tg.User{ID: 1000}, Data: "adm:squad"}
	if !svc.HandleCallback(ctx, cb) {
		t.Fatal("adm:squad should be handled")
	}
	m := bot.sent[0]
	if m.Keyboard == nil || len(m.Keyboard.InlineKeyboard) != 3 {
		t.Fatalf("expected 2 squad rows + back: %+v", m.Keyboard)
	}
	if m.Keyboard.InlineKeyboard[0][0].CallbackData != "adm:sq:sq-def" {
		t.Fatalf("squad button data = %q", m.Keyboard.InlineKeyboard[0][0].CallbackData)
	}
	if !strings.HasPrefix(m.Keyboard.InlineKeyboard[1][0].Text, "✅ ") {
		t.Fatalf("selected squad not marked: %+v", m.Keyboard.InlineKeyboard[1][0])
	}
}

func TestAdmSquadPickPersists(t *testing.T) {
	svc, bot, _, st := newTestService(t)
	ctx := context.Background()
	svc.squads.(*fakeSquadLister).squads = []InternalSquad{{UUID: "sq-prem", Name: "Premium"}}

	cb := &tg.CallbackQuery{ID: "cb1", From: tg.User{ID: 1000}, Data: "adm:sq:sq-prem"}
	if !svc.HandleCallback(ctx, cb) {
		t.Fatal("adm:sq should be handled")
	}
	if got, found, _ := st.GetSetting(ctx, defaultSquadUUIDKey); !found || got != "sq-prem" {
		t.Fatalf("not persisted: %q", got)
	}
	if !strings.Contains(bot.sent[len(bot.sent)-1].Text, "Premium") {
		t.Fatalf("expected confirmation with squad name: %+v", bot.sent)
	}
}

func TestAdminListSquadsMarksSelected(t *testing.T) {
	svc, _, _, st := newTestService(t)
	ctx := context.Background()
	lister := svc.squads.(*fakeSquadLister)
	lister.squads = []InternalSquad{
		{UUID: "sq-def", Name: "Default-Squad"},
		{UUID: "sq-prem", Name: "Premium"},
	}
	if err := st.UpsertSetting(ctx, defaultSquadUUIDKey, "sq-prem"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	squads, err := svc.AdminListSquads(ctx, 1000)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(squads) != 2 || squads[0].Selected || !squads[1].Selected {
		t.Fatalf("squads wrong: %+v", squads)
	}

	if _, err := svc.AdminListSquads(ctx, 9999); !errors.Is(err, ErrNotAdmin) {
		t.Fatalf("non-admin err = %v, want ErrNotAdmin", err)
	}
}

func TestAdminSetDefaultSquad(t *testing.T) {
	svc, _, _, st := newTestService(t)
	ctx := context.Background()
	svc.squads.(*fakeSquadLister).squads = []InternalSquad{{UUID: "sq-1", Name: "Default-Squad"}}

	if err := svc.AdminSetDefaultSquad(ctx, 9999, "sq-1"); !errors.Is(err, ErrNotAdmin) {
		t.Fatalf("non-admin err = %v, want ErrNotAdmin", err)
	}
	if err := svc.AdminSetDefaultSquad(ctx, 1000, "nope"); !errors.Is(err, ErrBadInput) {
		t.Fatalf("unknown uuid err = %v, want ErrBadInput", err)
	}
	if err := svc.AdminSetDefaultSquad(ctx, 1000, "sq-1"); err != nil {
		t.Fatalf("err: %v", err)
	}
	if got, found, _ := st.GetSetting(ctx, defaultSquadNameKey); !found || got != "Default-Squad" {
		t.Fatalf("name not persisted: %q", got)
	}
}

func TestAdminPanelDataIncludesDefaultSquadName(t *testing.T) {
	svc, _, _, st := newTestService(t)
	ctx := context.Background()
	if err := st.UpsertSetting(ctx, defaultSquadNameKey, "Premium"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	panel, err := svc.AdminPanelData(ctx, 1000)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if panel.DefaultSquadName != "Premium" {
		t.Fatalf("default_squad_name = %q, want Premium", panel.DefaultSquadName)
	}
}
