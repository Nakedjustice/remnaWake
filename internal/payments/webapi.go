package payments

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Nakedjustice/remnaWake/internal/i18n"
	"github.com/Nakedjustice/remnaWake/internal/store"
)

// Errors returned by the mini app API methods, mapped to HTTP statuses by the
// webapp handler.
var (
	ErrNotLinked        = errors.New("telegram id is not linked to any profile")
	ErrProfileUnknown   = errors.New("profile does not belong to this user")
	ErrTariffUnknown    = errors.New("tariff not found")
	ErrPaymentsDisabled = errors.New("payment flow is disabled (no admin configured)")
	// ErrScreenshotRequired: the renew request was not created because the
	// admin requires a payment screenshot; the user was asked to attach it in
	// the bot chat.
	ErrScreenshotRequired = errors.New("payment screenshot required")
)

// WebProfile is one linked subscription as shown in the mini app.
type WebProfile struct {
	RemnawaveID     int64  `json:"remnawave_id"`
	Username        string `json:"username"`
	Status          string `json:"status"`
	StatusLabel     string `json:"status_label"`
	ExpireAt        string `json:"expire_at,omitempty"` // DD.MM.YYYY
	DaysLeft        int    `json:"days_left"`
	SubscriptionURL string `json:"subscription_url,omitempty"`
}

// WebTariff is one renewal option as shown in the mini app.
type WebTariff struct {
	Months     int    `json:"months"`
	Price      int    `json:"price"`
	PriceLabel string `json:"price_label"`
}

// WebGift is one bought gift code as shown in the mini app.
type WebGift struct {
	Code        string `json:"code"`
	Months      int    `json:"months"`
	Status      string `json:"status"`
	StatusLabel string `json:"status_label"`
	CreatedAt   string `json:"created_at"`     // DD.MM.YYYY
	Link        string `json:"link,omitempty"` // t.me redemption deep link, issued gifts only
}

// WebInvites is the invite-requests summary shown in the mini app.
type WebInvites struct {
	Total   int `json:"total"`
	Pending int `json:"pending"`
}

// WebCabinet is the full /api/me payload for the mini app.
type WebCabinet struct {
	Linked     bool         `json:"linked"`
	IsAdmin    bool         `json:"is_admin,omitempty"`
	Profiles   []WebProfile `json:"profiles,omitempty"`
	Tariffs    []WebTariff  `json:"tariffs,omitempty"`
	Requisites string       `json:"requisites,omitempty"`
	Gifts      []WebGift    `json:"gifts,omitempty"`
	Invites    *WebInvites  `json:"invites,omitempty"`
}

// CabinetData assembles the personal-cabinet view for the mini app: linked
// profiles, tariffs, payment requisites and gift/invite summaries.
func (s *Service) CabinetData(ctx context.Context, telegramID int64) (*WebCabinet, error) {
	subs, err := s.finder.FindByTelegramID(ctx, telegramID)
	if err != nil {
		return nil, fmt.Errorf("find by telegram id: %w", err)
	}

	// Presentational only: every /api/admin call re-checks isAdmin server-side.
	out := &WebCabinet{Linked: len(subs) > 0, IsAdmin: s.isEnabled() && s.isAdmin(telegramID)}
	now := s.now()
	for i := range subs {
		sub := &subs[i]
		p := WebProfile{
			RemnawaveID:     sub.RemnawaveID,
			Username:        sub.Username,
			Status:          sub.Status,
			StatusLabel:     subStatusLabel(sub.Status),
			SubscriptionURL: sub.SubscriptionURL,
		}
		if !sub.ExpireAt.IsZero() {
			p.ExpireAt = sub.ExpireAt.Format("02.01.2006")
			if sub.ExpireAt.After(now) {
				p.DaysLeft = daysLeft(now, sub.ExpireAt)
			}
		}
		out.Profiles = append(out.Profiles, p)

		// Refresh the snapshot so a renew started from the mini app works even
		// if this user was never notified (same as the chat cabinet).
		if err := s.store.UpsertNotifiedUser(ctx, store.NotifiedUser{
			RemnawaveID: sub.RemnawaveID,
			UUID:        sub.UUID,
			Username:    sub.Username,
			TelegramID:  telegramID,
			ExpireAt:    sub.ExpireAt,
		}); err != nil {
			s.logger.Error("webapp: remember user failed", "err", err.Error(), "user_id", sub.RemnawaveID)
		}
	}

	tariffs, err := s.store.ListTariffs(ctx)
	if err != nil {
		s.logger.Error("webapp: list tariffs failed", "err", err.Error())
	}
	for _, t := range tariffs {
		out.Tariffs = append(out.Tariffs, WebTariff{Months: t.Months, Price: t.Price, PriceLabel: s.priceLabel(t.Price)})
	}

	s.mu.Lock()
	out.Requisites = s.requisites
	s.mu.Unlock()

	gifts, err := s.store.ListGiftCodesByBuyer(ctx, telegramID)
	if err != nil {
		s.logger.Error("webapp: list gifts failed", "err", err.Error())
	}
	for i := range gifts {
		g := &gifts[i]
		wg := WebGift{
			Code:        g.Code,
			Months:      g.Months,
			Status:      g.Status,
			StatusLabel: giftStatusLabel(g),
			CreatedAt:   g.CreatedAt.Format("02.01.2006"),
		}
		if g.Status == "issued" {
			wg.Link = s.giftDeepLink(g.Code)
		}
		out.Gifts = append(out.Gifts, wg)
	}

	invites, err := s.store.ListInviteRequestsByInviter(ctx, telegramID)
	if err != nil {
		s.logger.Error("webapp: list invites failed", "err", err.Error())
	}
	if len(invites) > 0 {
		inv := &WebInvites{Total: len(invites)}
		for _, r := range invites {
			if r.Status == "pending" {
				inv.Pending++
			}
		}
		out.Invites = inv
	}

	return out, nil
}

// CreateRenewRequest creates a pending payment request from the mini app:
// verifies the profile belongs to telegramID, resolves the tariff price and
// notifies the admins. It also confirms in the user's chat so the request has
// a visible trace outside the mini app.
func (s *Service) CreateRenewRequest(ctx context.Context, telegramID, remnawaveID int64, months int) error {
	if !s.isEnabled() {
		return ErrPaymentsDisabled
	}
	subs, err := s.finder.FindByTelegramID(ctx, telegramID)
	if err != nil {
		return fmt.Errorf("find by telegram id: %w", err)
	}
	if len(subs) == 0 {
		return ErrNotLinked
	}
	var sub *Subscriber
	for i := range subs {
		if subs[i].RemnawaveID == remnawaveID {
			sub = &subs[i]
			break
		}
	}
	if sub == nil {
		return ErrProfileUnknown
	}

	price := 0
	tariffs, err := s.store.ListTariffs(ctx)
	if err != nil {
		return fmt.Errorf("list tariffs: %w", err)
	}
	if len(tariffs) > 0 {
		tariff, err := s.store.GetTariff(ctx, months)
		if err != nil {
			return fmt.Errorf("get tariff: %w", err)
		}
		if tariff == nil {
			return ErrTariffUnknown
		}
		price = tariff.Price
	} else if months < 1 {
		months = 1
	}

	u := &store.NotifiedUser{
		RemnawaveID: sub.RemnawaveID,
		UUID:        sub.UUID,
		Username:    sub.Username,
		TelegramID:  telegramID,
		ExpireAt:    sub.ExpireAt,
	}
	if err := s.store.UpsertNotifiedUser(ctx, *u); err != nil {
		s.logger.Error("webapp: remember user failed", "err", err.Error(), "user_id", sub.RemnawaveID)
	}

	// With the screenshot requirement on, the request is deferred: the mini app
	// cannot upload photos to the bot, so the user finishes in the bot chat.
	if s.getRequireScreenshot() {
		s.startPayPhotoFlow(ctx, telegramID, sub.RemnawaveID, months, price)
		return ErrScreenshotRequired
	}

	if _, err := s.createPaymentRequest(ctx, u, months, price, nil); err != nil {
		return err
	}

	_ = s.bot.SendPlain(ctx, telegramID, fmt.Sprintf(
		i18n.T("✅ Заявка на продление «%s» на %d мес. отправлена администратору. После подтверждения оплаты подписка будет продлена."),
		sub.Username, months))
	return nil
}

// giftDeepLink returns the t.me redemption link for a gift code, or "" when
// the bot username is unknown (the chat flow then falls back to manual /start
// instructions; the mini app simply hides the link).
func (s *Service) giftDeepLink(code string) string {
	bu := s.getBotUsername()
	if bu == "" {
		return ""
	}
	return fmt.Sprintf("https://t.me/%s?start=%s%s", bu, giftDeepLinkPrefix, code)
}

// CreateGiftRequest creates a pending gift-code purchase from the mini app,
// mirroring the chat /gift flow: subscribers only, tariff-priced, and the
// admins get the same confirm/reject buttons (gc_ok/gc_rej) as in chat. The
// buyer also gets a chat confirmation so the request has a visible trace
// outside the mini app.
func (s *Service) CreateGiftRequest(ctx context.Context, telegramID int64, months int) error {
	if !s.isEnabled() {
		return ErrPaymentsDisabled
	}
	if months < 1 {
		return ErrBadInput
	}
	subs, err := s.finder.FindByTelegramID(ctx, telegramID)
	if err != nil {
		return fmt.Errorf("find by telegram id: %w", err)
	}
	if len(subs) == 0 {
		return ErrNotLinked
	}
	price, err := s.giftPriceFor(ctx, months)
	if err != nil {
		return err
	}
	giftID, code, err := s.createGiftCodeRow(ctx, subs[0].Username, telegramID, months, price)
	if err != nil {
		return fmt.Errorf("create gift code: %w", err)
	}
	s.notifyAdminsGiftRequest(ctx, giftID, code, subs[0].Username, telegramID, months, price)
	_ = s.bot.SendPlain(ctx, telegramID, fmt.Sprintf(
		i18n.T("✅ Заявка на подарочную подписку (%d мес.) отправлена администратору. Ожидайте подтверждения оплаты."), months))
	return nil
}

// CreateInviteRequest creates a pending invite from the mini app, mirroring
// the chat /invite flow: subscribers only, the new user is priced at the
// 1-month tariff, and the admins get the same approve/reject buttons
// (inv_ok/inv_rej) as in chat.
func (s *Service) CreateInviteRequest(ctx context.Context, telegramID int64, username string) error {
	if !s.isEnabled() || s.creator == nil {
		return ErrPaymentsDisabled
	}
	username = strings.TrimSpace(username)
	if !isValidUsername(username) {
		return ErrBadInput
	}
	subs, err := s.finder.FindByTelegramID(ctx, telegramID)
	if err != nil {
		return fmt.Errorf("find by telegram id: %w", err)
	}
	if len(subs) == 0 {
		return ErrNotLinked
	}
	price := 0
	tariff, err := s.store.GetTariff(ctx, 1)
	if err != nil {
		return fmt.Errorf("get tariff: %w", err)
	}
	if tariff != nil {
		price = tariff.Price
	}
	reqID, err := s.store.CreateInviteRequest(ctx, store.InviteRequest{
		InviterTelegramID: telegramID,
		InviterUsername:   subs[0].Username,
		NewUsername:       username,
		Months:            1,
		Price:             price,
		Status:            "pending",
	})
	if err != nil {
		return fmt.Errorf("create invite request: %w", err)
	}
	s.notifyAdminsInviteRequest(ctx, reqID, subs[0].Username, telegramID, username, price)
	_ = s.bot.SendPlain(ctx, telegramID, fmt.Sprintf(
		i18n.T("✅ Заявка на приглашение пользователя «%s» отправлена администратору."), username))
	return nil
}
