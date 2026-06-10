package payments

import (
	"context"
	"errors"
	"fmt"

	"github.com/Nakedjustice/remnaWake/internal/store"
)

// Errors returned by the mini app API methods, mapped to HTTP statuses by the
// webapp handler.
var (
	ErrNotLinked        = errors.New("telegram id is not linked to any profile")
	ErrProfileUnknown   = errors.New("profile does not belong to this user")
	ErrTariffUnknown    = errors.New("tariff not found")
	ErrPaymentsDisabled = errors.New("payment flow is disabled (no admin configured)")
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
	CreatedAt   string `json:"created_at"` // DD.MM.YYYY
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
		out.Gifts = append(out.Gifts, WebGift{
			Code:        g.Code,
			Months:      g.Months,
			Status:      g.Status,
			StatusLabel: giftStatusLabel(g),
			CreatedAt:   g.CreatedAt.Format("02.01.2006"),
		})
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

	if _, err := s.createPaymentRequest(ctx, u, months, price); err != nil {
		return err
	}

	_ = s.bot.SendPlain(ctx, telegramID, fmt.Sprintf(
		"✅ Заявка на продление «%s» на %d мес. отправлена администратору. После подтверждения оплаты подписка будет продлена.",
		sub.Username, months))
	return nil
}
