package payments

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/i18n"
	"github.com/Nakedjustice/remnaWake/internal/store"
)

// Errors returned by admin operations, mapped to HTTP statuses by the webapp
// handler and to callback answers by the bot.
var (
	ErrNotAdmin        = errors.New("not an admin")
	ErrBadInput        = errors.New("invalid input")
	ErrRequestNotFound = errors.New("request not found")
	ErrRequestResolved = errors.New("request already resolved")
	// ErrConfirmedNotMarked: the panel extension succeeded but the request could
	// not be marked confirmed in the database; confirming again would extend the
	// subscription a second time.
	ErrConfirmedNotMarked = errors.New("extension applied but request not marked confirmed")
	// ErrPanelCreateFailed: the panel rejected the user creation; the invite
	// request stays pending and can be approved again.
	ErrPanelCreateFailed = errors.New("create user in panel failed")
)

// confirmPaymentRequest extends the subscription for a pending payment
// request, marks it confirmed and clears the confirm buttons in every admin's
// chat. Shared by the bot callback and the mini app admin API; it sends no
// chat messages itself so each transport keeps its own notifications.
func (s *Service) confirmPaymentRequest(ctx context.Context, reqID int64) (*store.PaymentRequest, time.Time, error) {
	req, err := s.store.GetPaymentRequest(ctx, reqID)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("get payment request: %w", err)
	}
	if req == nil {
		return nil, time.Time{}, ErrRequestNotFound
	}
	if req.Status != "pending" {
		return req, time.Time{}, ErrRequestResolved
	}

	base := req.ExpireAt
	if now := s.now(); base.Before(now) {
		base = now
	}
	newExpireAt := base.AddDate(0, req.Months, 0)

	if s.dryRun {
		s.logger.Info("dry-run: would extend", "uuid", req.UUID, "months", req.Months, "new_expire", newExpireAt.Format("2006-01-02"))
	} else if err := s.extender.ExtendSubscriptionByUUID(ctx, req.UUID, newExpireAt); err != nil {
		s.logger.Error("extend subscription failed", "uuid", req.UUID, "err", err.Error())
		return req, time.Time{}, fmt.Errorf("extend subscription: %w", err)
	}

	if _, err := s.store.ConfirmPaymentRequest(ctx, reqID, s.now()); err != nil {
		s.logger.Error("mark confirmed failed", "uuid", req.UUID, "req_id", reqID, "err", err.Error())
		// The subscription IS extended; clear the buttons anyway so a second tap
		// cannot extend it again, and report the inconsistency to the caller.
		s.clearPayButtons(ctx, reqID)
		return req, newExpireAt, fmt.Errorf("%w: %v", ErrConfirmedNotMarked, err)
	}
	s.clearPayButtons(ctx, reqID)
	return req, newExpireAt, nil
}

// rejectPaymentRequest marks a pending payment request rejected, clears the
// confirm buttons in every admin's chat and notifies the requesting user.
func (s *Service) rejectPaymentRequest(ctx context.Context, reqID int64) error {
	req, err := s.store.GetPaymentRequest(ctx, reqID)
	if err != nil {
		return fmt.Errorf("get payment request: %w", err)
	}
	if req == nil {
		return ErrRequestNotFound
	}
	if req.Status != "pending" {
		return ErrRequestResolved
	}

	ok, err := s.store.RejectPaymentRequest(ctx, reqID, s.now())
	if err != nil {
		return fmt.Errorf("reject payment request: %w", err)
	}
	if !ok {
		return ErrRequestResolved
	}
	s.clearPayButtons(ctx, reqID)
	if req.TelegramID != 0 {
		_ = s.bot.SendPlain(ctx, req.TelegramID, fmt.Sprintf(
			i18n.T("❌ Заявка на продление «%s» на %d мес. отклонена администратором."),
			req.Username, req.Months))
	}
	return nil
}
