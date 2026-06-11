package payments

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Nakedjustice/remnaWake/internal/i18n"
)

// WebAdminGift is one issued (revocable) gift code as shown in the mini app
// admin panel.
type WebAdminGift struct {
	ID        int64  `json:"id"`
	Code      string `json:"code"`
	Months    int    `json:"months"`
	Buyer     string `json:"buyer"`
	CreatedAt string `json:"created_at"` // DD.MM.YYYY
}

// WebAdminRequest is one pending payment request as shown in the mini app
// admin panel.
type WebAdminRequest struct {
	ID         int64  `json:"id"`
	Username   string `json:"username"`
	Months     int    `json:"months"`
	Price      int    `json:"price"`
	PriceLabel string `json:"price_label,omitempty"`
	ExpireAt   string `json:"expire_at,omitempty"` // DD.MM.YYYY
	CreatedAt  string `json:"created_at"`          // DD.MM.YYYY
}

// WebAdminGiftRequest is one pending gift purchase as shown in the mini app
// admin panel.
type WebAdminGiftRequest struct {
	ID         int64  `json:"id"`
	Code       string `json:"code"`
	Months     int    `json:"months"`
	Price      int    `json:"price"`
	PriceLabel string `json:"price_label,omitempty"`
	Buyer      string `json:"buyer"`
	CreatedAt  string `json:"created_at"` // DD.MM.YYYY
}

// WebAdminInviteRequest is one pending invite request as shown in the mini app
// admin panel.
type WebAdminInviteRequest struct {
	ID          int64  `json:"id"`
	Inviter     string `json:"inviter"`
	NewUsername string `json:"new_username"`
	Months      int    `json:"months"`
	Price       int    `json:"price"`
	PriceLabel  string `json:"price_label,omitempty"`
	CreatedAt   string `json:"created_at"` // DD.MM.YYYY
}

// WebAdminPanel is the full /api/admin payload for the mini app.
type WebAdminPanel struct {
	Tariffs        []WebTariff             `json:"tariffs"`
	Requisites     string                  `json:"requisites"`
	Gifts          []WebAdminGift          `json:"gifts"`
	Requests       []WebAdminRequest       `json:"requests"`
	GiftRequests   []WebAdminGiftRequest   `json:"gift_requests"`
	InviteRequests []WebAdminInviteRequest `json:"invite_requests"`
}

// adminGuard rejects calls from non-admin Telegram IDs. The ID comes from
// validated initData, so this is the real authorization check — the is_admin
// flag in /api/me is presentational only.
func (s *Service) adminGuard(telegramID int64) error {
	if !s.isEnabled() || !s.isAdmin(telegramID) {
		return ErrNotAdmin
	}
	return nil
}

// AdminPanelData assembles the admin panel view: tariffs, requisites, issued
// gift codes and pending payment requests.
func (s *Service) AdminPanelData(ctx context.Context, telegramID int64) (*WebAdminPanel, error) {
	if err := s.adminGuard(telegramID); err != nil {
		return nil, err
	}
	out := &WebAdminPanel{}

	tariffs, err := s.store.ListTariffs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tariffs: %w", err)
	}
	for _, t := range tariffs {
		out.Tariffs = append(out.Tariffs, WebTariff{Months: t.Months, Price: t.Price, PriceLabel: s.priceLabel(t.Price)})
	}

	s.mu.Lock()
	out.Requisites = s.requisites
	s.mu.Unlock()

	gifts, err := s.store.ListGiftCodesByStatus(ctx, "issued")
	if err != nil {
		return nil, fmt.Errorf("list gift codes: %w", err)
	}
	for i := range gifts {
		g := &gifts[i]
		out.Gifts = append(out.Gifts, WebAdminGift{
			ID:        g.ID,
			Code:      g.Code,
			Months:    g.Months,
			Buyer:     g.BuyerUsername,
			CreatedAt: g.CreatedAt.Format("02.01.2006"),
		})
	}

	requests, err := s.store.ListPaymentRequestsByStatus(ctx, "pending")
	if err != nil {
		return nil, fmt.Errorf("list payment requests: %w", err)
	}
	for i := range requests {
		r := &requests[i]
		wr := WebAdminRequest{
			ID:        r.ID,
			Username:  r.Username,
			Months:    r.Months,
			Price:     r.Price,
			CreatedAt: r.CreatedAt.Format("02.01.2006"),
		}
		if r.Price > 0 {
			wr.PriceLabel = s.priceLabel(r.Price)
		}
		if !r.ExpireAt.IsZero() {
			wr.ExpireAt = r.ExpireAt.Format("02.01.2006")
		}
		out.Requests = append(out.Requests, wr)
	}

	pendingGifts, err := s.store.ListGiftCodesByStatus(ctx, "pending")
	if err != nil {
		return nil, fmt.Errorf("list pending gift codes: %w", err)
	}
	for i := range pendingGifts {
		g := &pendingGifts[i]
		gr := WebAdminGiftRequest{
			ID:        g.ID,
			Code:      g.Code,
			Months:    g.Months,
			Price:     g.Price,
			Buyer:     g.BuyerUsername,
			CreatedAt: g.CreatedAt.Format("02.01.2006"),
		}
		if g.Price > 0 {
			gr.PriceLabel = s.priceLabel(g.Price)
		}
		out.GiftRequests = append(out.GiftRequests, gr)
	}

	invites, err := s.store.ListInviteRequestsByStatus(ctx, "pending")
	if err != nil {
		return nil, fmt.Errorf("list invite requests: %w", err)
	}
	for i := range invites {
		r := &invites[i]
		ir := WebAdminInviteRequest{
			ID:          r.ID,
			Inviter:     r.InviterUsername,
			NewUsername: r.NewUsername,
			Months:      r.Months,
			Price:       r.Price,
			CreatedAt:   r.CreatedAt.Format("02.01.2006"),
		}
		if r.Price > 0 {
			ir.PriceLabel = s.priceLabel(r.Price)
		}
		out.InviteRequests = append(out.InviteRequests, ir)
	}

	return out, nil
}

// AdminSetTariff adds or updates a tariff from the mini app admin panel.
func (s *Service) AdminSetTariff(ctx context.Context, telegramID int64, months, price int) error {
	if err := s.adminGuard(telegramID); err != nil {
		return err
	}
	if months < 1 || price < 1 {
		return ErrBadInput
	}
	if err := s.store.UpsertTariff(ctx, months, price); err != nil {
		return fmt.Errorf("upsert tariff: %w", err)
	}
	return nil
}

// AdminDeleteTariff removes a tariff from the mini app admin panel.
func (s *Service) AdminDeleteTariff(ctx context.Context, telegramID int64, months int) error {
	if err := s.adminGuard(telegramID); err != nil {
		return err
	}
	deleted, err := s.store.DeleteTariff(ctx, months)
	if err != nil {
		return fmt.Errorf("delete tariff: %w", err)
	}
	if !deleted {
		return ErrTariffUnknown
	}
	return nil
}

// AdminSetRequisites replaces the payment requisites text from the mini app
// admin panel, persisting it and refreshing the in-memory cache (same as the
// chat /setrequisites flow).
func (s *Service) AdminSetRequisites(ctx context.Context, telegramID int64, text string) error {
	if err := s.adminGuard(telegramID); err != nil {
		return err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return ErrBadInput
	}
	if err := s.store.UpsertSetting(ctx, requisitesKey, text); err != nil {
		return fmt.Errorf("save requisites: %w", err)
	}
	s.mu.Lock()
	s.requisites = text
	s.mu.Unlock()
	return nil
}

// WebBroadcastResult reports broadcast delivery counts to the mini app.
type WebBroadcastResult struct {
	Sent   int `json:"sent"`
	Failed int `json:"failed"`
}

// AdminBroadcast sends text to every panel user with a linked Telegram ID
// from the mini app admin panel. Runs synchronously: the HTTP caller waits
// and gets delivery counts back.
func (s *Service) AdminBroadcast(ctx context.Context, telegramID int64, text string) (*WebBroadcastResult, error) {
	if err := s.adminGuard(telegramID); err != nil {
		return nil, err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, ErrBadInput
	}
	sent, failed, err := s.broadcastMessage(ctx, text)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	return &WebBroadcastResult{Sent: sent, Failed: failed}, nil
}

// AdminRevokeGiftCode revokes an issued gift code from the mini app admin
// panel and notifies the buyer, mirroring the chat revoke flow.
func (s *Service) AdminRevokeGiftCode(ctx context.Context, telegramID, giftID int64) error {
	if err := s.adminGuard(telegramID); err != nil {
		return err
	}
	g, err := s.store.GetGiftCode(ctx, giftID)
	if err != nil {
		return fmt.Errorf("get gift code: %w", err)
	}
	if g == nil {
		return ErrRequestNotFound
	}
	ok, err := s.store.RevokeGiftCode(ctx, giftID, s.now())
	if err != nil {
		return fmt.Errorf("revoke gift code: %w", err)
	}
	if !ok {
		return ErrRequestResolved
	}
	if g.BuyerTelegramID != 0 {
		_ = s.bot.SendPlain(ctx, g.BuyerTelegramID,
			fmt.Sprintf(i18n.T("🚫 Ваш подарочный код %s отозван администратором."), g.Code))
	}
	return nil
}

// AdminConfirmRequest confirms a pending payment request from the mini app
// admin panel: extends the subscription and notifies the paying user in chat
// (a webapp confirmation otherwise leaves no visible trace for them).
func (s *Service) AdminConfirmRequest(ctx context.Context, telegramID, reqID int64) error {
	if err := s.adminGuard(telegramID); err != nil {
		return err
	}
	req, newExpireAt, err := s.confirmPaymentRequest(ctx, reqID)
	if errors.Is(err, ErrConfirmedNotMarked) {
		// The extension itself succeeded; warn the admin in chat, because the
		// webapp can only show a generic error for this state.
		_ = s.bot.SendPlain(ctx, telegramID, fmt.Sprintf(
			i18n.T("⚠️ Подписка для %s продлена до %s, но заявку №%d не удалось отметить подтверждённой в базе. Не подтверждайте её повторно — это продлит подписку ещё раз."),
			req.Username, newExpireAt.Format("02.01.2006"), reqID))
	}
	if err != nil {
		return err
	}
	if req.TelegramID != 0 {
		_ = s.bot.SendPlain(ctx, req.TelegramID, fmt.Sprintf(
			i18n.T("✅ Подписка «%s» продлена на %d мес. до %s."),
			req.Username, req.Months, newExpireAt.Format("02.01.2006")))
	}
	return nil
}

// AdminRejectRequest rejects a pending payment request from the mini app
// admin panel; the requesting user is notified by the shared helper.
func (s *Service) AdminRejectRequest(ctx context.Context, telegramID, reqID int64) error {
	if err := s.adminGuard(telegramID); err != nil {
		return err
	}
	return s.rejectPaymentRequest(ctx, reqID)
}

// AdminConfirmGiftRequest confirms payment of a pending gift purchase from the
// mini app admin panel; the buyer gets the redemption link from the shared
// helper.
func (s *Service) AdminConfirmGiftRequest(ctx context.Context, telegramID, giftID int64) error {
	if err := s.adminGuard(telegramID); err != nil {
		return err
	}
	_, err := s.issueGiftRequest(ctx, giftID)
	return err
}

// AdminRejectGiftRequest rejects a pending gift purchase from the mini app
// admin panel; the buyer is notified by the shared helper.
func (s *Service) AdminRejectGiftRequest(ctx context.Context, telegramID, giftID int64) error {
	if err := s.adminGuard(telegramID); err != nil {
		return err
	}
	_, err := s.rejectGiftRequest(ctx, giftID)
	return err
}

// AdminApproveInviteRequest approves a pending invite request from the mini
// app admin panel: creates the user in the panel and notifies the inviter via
// the shared helper.
func (s *Service) AdminApproveInviteRequest(ctx context.Context, telegramID, reqID int64) error {
	if err := s.adminGuard(telegramID); err != nil {
		return err
	}
	_, _, _, err := s.approveInviteRequest(ctx, reqID)
	return err
}

// AdminRejectInviteRequest rejects a pending invite request from the mini app
// admin panel; the inviter is notified by the shared helper.
func (s *Service) AdminRejectInviteRequest(ctx context.Context, telegramID, reqID int64) error {
	if err := s.adminGuard(telegramID); err != nil {
		return err
	}
	_, err := s.rejectInviteRequest(ctx, reqID)
	return err
}
