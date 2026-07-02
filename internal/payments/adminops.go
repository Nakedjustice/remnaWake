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
	// ErrPlanUnknown: the referenced tariff preset does not exist (and is not
	// the built-in standard plan).
	ErrPlanUnknown = errors.New("plan unknown")
	// ErrConfirmedNotMarked: the panel extension succeeded but the request could
	// not be marked confirmed in the database; confirming again would extend the
	// subscription a second time.
	ErrConfirmedNotMarked = errors.New("extension applied but request not marked confirmed")
	// ErrPanelCreateFailed: the panel rejected the user creation; the invite
	// request stays pending and can be approved again.
	ErrPanelCreateFailed = errors.New("create user in panel failed")
	// ErrPanelUnavailable: a panel API call needed by the operation (e.g.
	// listing internal squads) failed; retrying later may succeed.
	ErrPanelUnavailable = errors.New("panel api unavailable")
)

const (
	RenewStatusPlanChangePreview = "plan_change_preview"

	PlanChangeUpgradeRecalculation = "upgrade_recalculation"
	PlanChangeDowngradeImmediate   = "downgrade_immediate"
)

type PlanChangePreview struct {
	Kind            string `json:"kind"`
	CurrentPlan     string `json:"current_plan"`
	TargetPlan      string `json:"target_plan"`
	CurrentExpireAt string `json:"current_expire_at"`
	NewExpireAt     string `json:"new_expire_at"`
}

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

	plan, planErr := s.getPlan(ctx, req.Plan)
	newExpireAt, _ := s.renewalPlanChange(ctx, req, planErr == nil)

	// Referral link payout: if this payer was referred and not yet credited,
	// fold the invitee bonus into this same extension so it lands in one panel
	// call; the referrer is credited after the request is marked confirmed.
	refEnabled, inviterDays, inviteeDays := s.referralConfig()
	var pendingReferral *store.Referral
	if refEnabled && req.TelegramID != 0 {
		if ref, err := s.store.GetReferral(ctx, req.TelegramID); err != nil {
			s.logger.Error("referral: lookup on confirm failed", "err", err.Error())
		} else if ref != nil && ref.Status == "pending" {
			pendingReferral = ref
			if inviteeDays > 0 {
				newExpireAt = newExpireAt.AddDate(0, 0, inviteeDays)
			}
		}
	}

	// The purchase stamps the plan's panel settings (squad, limits, tag) in the
	// same PATCH as the expiry, so a failure leaves the request pending and
	// retryable as one unit. A plan deleted between purchase and confirm falls
	// back to extending the expiry only — never silently altering limits.
	patch := UserPatch{ExpireAt: &newExpireAt}
	if planErr != nil {
		s.logger.Warn("confirm: plan lookup failed, extending expiry only", "plan", req.Plan, "err", planErr.Error())
	} else {
		patch = s.planUserPatch(ctx, plan, newExpireAt)
	}

	if s.dryRun {
		s.logger.Info("dry-run: would extend and stamp plan", "uuid", req.UUID, "months", req.Months, "plan", req.Plan, "new_expire", newExpireAt.Format("2006-01-02"))
	} else if err := s.userUpdater.UpdateUser(ctx, req.UUID, patch); err != nil {
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
	if pendingReferral != nil {
		s.settleReferral(ctx, pendingReferral, inviterDays, inviteeDays)
	}
	return req, newExpireAt, nil
}

func (s *Service) renewalExpireAt(ctx context.Context, req *store.PaymentRequest, targetPlanKnown bool) time.Time {
	newExpireAt, _ := s.renewalPlanChange(ctx, req, targetPlanKnown)
	return newExpireAt
}

func (s *Service) renewalPlanChangePreview(ctx context.Context, req *store.PaymentRequest, targetPlanKnown bool) *PlanChangePreview {
	_, preview := s.renewalPlanChange(ctx, req, targetPlanKnown)
	return preview
}

func (s *Service) renewalPlanChange(ctx context.Context, req *store.PaymentRequest, targetPlanKnown bool) (time.Time, *PlanChangePreview) {
	now := s.now()
	base := req.ExpireAt
	if base.Before(now) {
		base = now
	}
	newExpireAt := base.AddDate(0, req.Months, 0)
	if !targetPlanKnown || !base.After(now) || req.Months <= 0 || req.Price <= 0 {
		return newExpireAt, nil
	}

	prev, err := s.store.LatestConfirmedPaymentRequestByUUID(ctx, req.UUID)
	if err != nil {
		s.logger.Warn("confirm: latest plan lookup failed, using full remaining time", "uuid", req.UUID, "err", err.Error())
		return newExpireAt, nil
	}
	if prev == nil || prev.Months <= 0 || prev.Price <= 0 || samePlan(prev.Plan, req.Plan) {
		return newExpireAt, nil
	}

	currentRate := monthlyRate(prev)
	targetRate := monthlyRate(req)
	if currentRate <= 0 || targetRate <= 0 || targetRate == currentRate {
		return newExpireAt, nil
	}
	kind := PlanChangeDowngradeImmediate
	if targetRate > currentRate {
		kind = PlanChangeUpgradeRecalculation
		remaining := base.Sub(now)
		if remaining <= 0 {
			return newExpireAt, nil
		}
		convertedRemaining := time.Duration(float64(remaining) * currentRate / targetRate)
		newExpireAt = now.Add(convertedRemaining).AddDate(0, req.Months, 0)
	}

	return newExpireAt, &PlanChangePreview{
		Kind:            kind,
		CurrentPlan:     s.planDisplayName(ctx, prev.Plan),
		TargetPlan:      s.planDisplayName(ctx, req.Plan),
		CurrentExpireAt: req.ExpireAt.Format("02.01.2006"),
		NewExpireAt:     newExpireAt.Format("02.01.2006"),
	}
}

func (s *Service) planDisplayName(ctx context.Context, code string) string {
	code = normalizePlan(code)
	plan, err := s.getPlan(ctx, code)
	if err == nil && plan.Name != "" {
		return plan.Name
	}
	return code
}

func samePlan(a, b string) bool {
	return normalizePlan(a) == normalizePlan(b)
}

func normalizePlan(plan string) string {
	if plan == "" {
		return store.PlanStandard
	}
	return plan
}

func monthlyRate(req *store.PaymentRequest) float64 {
	return float64(req.Price) / float64(req.Months)
}

// rejectPaymentRequest marks a pending payment request rejected, clears the
// confirm buttons in every admin's chat and notifies the requesting user.
func (s *Service) rejectPaymentRequest(ctx context.Context, reqID int64) (*store.PaymentRequest, error) {
	req, err := s.store.GetPaymentRequest(ctx, reqID)
	if err != nil {
		return nil, fmt.Errorf("get payment request: %w", err)
	}
	if req == nil {
		return nil, ErrRequestNotFound
	}
	if req.Status != "pending" {
		return nil, ErrRequestResolved
	}

	ok, err := s.store.RejectPaymentRequest(ctx, reqID, s.now())
	if err != nil {
		return nil, fmt.Errorf("reject payment request: %w", err)
	}
	if !ok {
		return nil, ErrRequestResolved
	}
	s.clearPayButtons(ctx, reqID)
	if req.TelegramID != 0 {
		_ = s.bot.SendPlain(ctx, req.TelegramID, fmt.Sprintf(
			i18n.T("❌ Заявка на продление «%s» на %d мес. отклонена администратором."),
			req.Username, req.Months))
	}
	return req, nil
}
