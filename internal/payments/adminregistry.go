package payments

import (
	"context"
	"fmt"
)

// This file is the single door for every admin operation, shared by the bot
// admin menu and the Mini App HTTP API. An operation is registered once here;
// both transports address it by name, so the authorization check, validation
// rules and side effects cannot drift apart (and cannot be bypassed: the
// admin guard lives in RunAdminOp, not in the callers).
//
// Op names are stable identifiers — bot callbacks, HTTP routes and tests all
// refer to them; rename only together with every transport.

// adminOpEntry is one registered admin operation with a type-erased payload.
type adminOpEntry struct {
	run func(ctx context.Context, s *Service, actorID int64, in any) (any, error)
}

// reg wraps a typed op body into the erased registry form. A nil payload
// becomes the zero In; a non-nil payload of the wrong type is a programming
// error at the transport boundary and reports ErrBadInput.
func reg[In any](run func(ctx context.Context, s *Service, actorID int64, in In) (any, error)) adminOpEntry {
	return adminOpEntry{run: func(ctx context.Context, s *Service, actorID int64, in any) (any, error) {
		var typed In
		if in != nil {
			t, ok := in.(In)
			if !ok {
				return nil, fmt.Errorf("admin op payload %T does not match the registered input: %w", in, ErrBadInput)
			}
			typed = t
		}
		return run(ctx, s, actorID, typed)
	}}
}

// regNoIn registers an op that takes no input.
func regNoIn(run func(ctx context.Context, s *Service, actorID int64) (any, error)) adminOpEntry {
	return adminOpEntry{run: func(ctx context.Context, s *Service, actorID int64, _ any) (any, error) {
		return run(ctx, s, actorID)
	}}
}

// RunAdminOp runs the named admin operation for actorID after verifying the
// actor is a configured admin. The payload must be the op's registered input
// type (nil for no-input ops). The result is the op's response DTO, or nil
// for ops that only acknowledge.
func (s *Service) RunAdminOp(ctx context.Context, actorID int64, name string, in any) (any, error) {
	if err := s.adminGuard(actorID); err != nil {
		return nil, err
	}
	entry, ok := adminOps[name]
	if !ok {
		return nil, fmt.Errorf("unknown admin op %q: %w", name, ErrBadInput)
	}
	return entry.run(ctx, s, actorID, in)
}

// --- op payloads ---

// AdminIDInput carries the {"id": N} body of the single-id actions
// (confirm/reject/delete/revoke/mark-paid/close).
type AdminIDInput struct {
	ID int64 `json:"id"`
}

// AdminTariffInput adds or updates one tariff cell.
type AdminTariffInput struct {
	Plan   string `json:"plan"` // "" = standard
	Months int    `json:"months"`
	Price  int    `json:"price"`
}

// AdminTariffDeleteInput removes one tariff cell.
type AdminTariffDeleteInput struct {
	Plan   string `json:"plan"` // "" = standard
	Months int    `json:"months"`
}

// AdminTrafficExtInput adds or updates one traffic-extension option.
type AdminTrafficExtInput struct {
	TrafficGB int `json:"traffic_gb"`
	Price     int `json:"price"`
}

// AdminTrafficExtDeleteInput removes one traffic-extension option.
type AdminTrafficExtDeleteInput struct {
	TrafficGB int `json:"traffic_gb"`
}

// AdminPlanDeleteInput removes a custom tariff preset.
type AdminPlanDeleteInput struct {
	Code string `json:"code"`
}

// AdminTextInput carries a single free-text value (payment requisites).
type AdminTextInput struct {
	Text string `json:"text"`
}

// AdminToggleInput carries a single on/off flag.
type AdminToggleInput struct {
	Enabled bool `json:"enabled"`
}

// AdminProviderInput enables or disables one payment provider.
type AdminProviderInput struct {
	Provider string `json:"provider"`
	Enabled  bool   `json:"enabled"`
}

// AdminUUIDInput carries one squad UUID.
type AdminUUIDInput struct {
	UUID string `json:"uuid"`
}

// AdminStrategyInput carries one traffic-reset strategy.
type AdminStrategyInput struct {
	Strategy string `json:"strategy"`
}

// AdminTrialInput is the JSON form of TrialConfig the admin panel posts.
type AdminTrialInput struct {
	Enabled         bool   `json:"enabled"`
	Days            int    `json:"days"`
	TrafficLimitGB  int    `json:"traffic_limit_gb"`
	HwidDeviceLimit int    `json:"hwid_device_limit"`
	SquadUUID       string `json:"squad_uuid"`
	RequireApproval bool   `json:"require_approval"`
}

func (in AdminTrialInput) config() TrialConfig {
	return TrialConfig{
		Enabled: in.Enabled, Days: in.Days, TrafficLimitGB: in.TrafficLimitGB,
		HwidDeviceLimit: in.HwidDeviceLimit, SquadUUID: in.SquadUUID,
		RequireApproval: in.RequireApproval,
	}
}

// AdminReferralInput configures the referral bonus.
type AdminReferralInput struct {
	Enabled     bool `json:"enabled"`
	InviterDays int  `json:"inviter_days"`
	InviteeDays int  `json:"invitee_days"`
}

// AdminBackupScheduleInput configures the scheduled database backup.
type AdminBackupScheduleInput struct {
	Enabled      bool `json:"enabled"`
	IntervalDays int  `json:"interval_days"`
}

// AdminIntervalInput carries one duration string (update check interval).
type AdminIntervalInput struct {
	Interval string `json:"interval"`
}

// AdminPatternInput carries one registration-guard pattern.
type AdminPatternInput struct {
	Pattern string `json:"pattern"`
}

// AdminUnblockInput removes one registration block by Telegram ID.
type AdminUnblockInput struct {
	TelegramID int64 `json:"telegram_id"`
}

// AdminFindUserInput resolves a user card by username or subscription link.
type AdminFindUserInput struct {
	Query string `json:"query"`
}

// AdminCohortInput selects the users.list cohort ("" = every user).
type AdminCohortInput struct {
	Cohort string `json:"-"` // query param, not a JSON body field
}

// AdminAnalyticsInput selects the operator-analytics window.
type AdminAnalyticsInput struct {
	Days int `json:"-"` // query param, not a JSON body field
}

// AdminBroadcastInput starts a broadcast to all linked users.
type AdminBroadcastInput struct {
	Message string `json:"message"`
}

// AdminFxRateInput stores a manual FX rate for one currency.
type AdminFxRateInput struct {
	Currency string  `json:"currency"`
	Rate     float64 `json:"rate"`
}

// AdminCurrencyInput carries one ISO currency code.
type AdminCurrencyInput struct {
	Currency string `json:"currency"`
}

// AdminProxyNotifInput mutes or unmutes alerts for one proxy server.
type AdminProxyNotifInput struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	SubName string `json:"sub_name"`
	Muted   bool   `json:"muted"`
}

// AdminSupportUserInput addresses one user's support conversation.
type AdminSupportUserInput struct {
	UserID int64 `json:"user_id"`
}

// AdminSupportSendInput delivers an admin reply to a user's thread.
type AdminSupportSendInput struct {
	UserID int64  `json:"user_id"`
	Text   string `json:"text"`
}

// AdminTicketInput addresses one support ticket.
type AdminTicketInput struct {
	TicketID int64 `json:"ticket_id"`
}

// AdminTicketReplyInput appends an admin reply to one ticket.
type AdminTicketReplyInput struct {
	TicketID int64  `json:"ticket_id"`
	Text     string `json:"text"`
}

// adminOps is the registry every admin transport resolves through.
var adminOps = map[string]adminOpEntry{
	// Read models.
	"panel.get":         regNoIn(func(ctx context.Context, s *Service, _ int64) (any, error) { return s.adminPanelData(ctx) }),
	"action_center.get": regNoIn(func(ctx context.Context, s *Service, _ int64) (any, error) { return s.actionCenterData(ctx) }),
	"stats.get":         regNoIn(func(ctx context.Context, s *Service, _ int64) (any, error) { return s.adminStatsData(ctx) }),
	"analytics.get": reg(func(ctx context.Context, s *Service, _ int64, in *AdminAnalyticsInput) (any, error) {
		days := 0
		if in != nil {
			days = in.Days
		}
		return s.adminOperatorAnalytics(ctx, days)
	}),
	"proxy_health.get": regNoIn(func(ctx context.Context, s *Service, _ int64) (any, error) { return s.proxyHealth(ctx) }),
	"payments.report": reg(func(ctx context.Context, s *Service, _ int64, in *WebPaymentFilter) (any, error) {
		var f WebPaymentFilter
		if in != nil {
			f = *in
		}
		return s.adminPaymentReport(ctx, f)
	}),
	"squads.list": regNoIn(func(ctx context.Context, s *Service, _ int64) (any, error) { return s.adminListSquads(ctx) }),
	"users.list": reg(func(ctx context.Context, s *Service, _ int64, in *AdminCohortInput) (any, error) {
		cohort := ""
		if in != nil {
			cohort = in.Cohort
		}
		return s.adminListUsers(ctx, cohort)
	}),
	"user.find": reg(func(ctx context.Context, s *Service, _ int64, in *AdminFindUserInput) (any, error) {
		return s.adminFindUser(ctx, in.Query)
	}),
	"servers.list":  regNoIn(func(ctx context.Context, s *Service, _ int64) (any, error) { return s.adminListInfraServers(ctx) }),
	"fx.list":       regNoIn(func(ctx context.Context, s *Service, _ int64) (any, error) { return s.adminListFxRates(ctx) }),
	"updates.check": regNoIn(func(ctx context.Context, s *Service, _ int64) (any, error) { return s.adminCheckUpdates(ctx) }),
	"support.conversations": regNoIn(func(ctx context.Context, s *Service, _ int64) (any, error) {
		return s.supportConversations(ctx)
	}),
	"support.thread": reg(func(ctx context.Context, s *Service, _ int64, in *AdminSupportUserInput) (any, error) {
		return s.supportThreadAdmin(ctx, in.UserID)
	}),
	"support.tickets": regNoIn(func(ctx context.Context, s *Service, _ int64) (any, error) { return s.supportTicketsAdmin(ctx) }),
	"support.ticket.thread": reg(func(ctx context.Context, s *Service, _ int64, in *AdminTicketInput) (any, error) {
		return s.supportTicketThreadAdmin(ctx, in.TicketID)
	}),

	// Settings and configuration.
	"backup.send": regNoIn(func(ctx context.Context, s *Service, actorID int64) (any, error) {
		return nil, s.sendBackupToAdmin(ctx, actorID)
	}),
	"backup.schedule.set": reg(func(ctx context.Context, s *Service, _ int64, in *AdminBackupScheduleInput) (any, error) {
		return nil, s.adminSetBackupSchedule(ctx, in.Enabled, in.IntervalDays)
	}),
	"updates.interval.set": reg(func(ctx context.Context, s *Service, _ int64, in *AdminIntervalInput) (any, error) {
		return nil, s.adminSetUpdateInterval(ctx, in.Interval)
	}),
	"tariff.set": reg(func(ctx context.Context, s *Service, _ int64, in *AdminTariffInput) (any, error) {
		return nil, s.adminSetTariff(ctx, in.Plan, in.Months, in.Price)
	}),
	"tariff.delete": reg(func(ctx context.Context, s *Service, _ int64, in *AdminTariffDeleteInput) (any, error) {
		return nil, s.adminDeleteTariff(ctx, in.Plan, in.Months)
	}),
	"traffic_ext.set": reg(func(ctx context.Context, s *Service, _ int64, in *AdminTrafficExtInput) (any, error) {
		return nil, s.adminSetTrafficExtensionOption(ctx, in.TrafficGB, in.Price)
	}),
	"traffic_ext.delete": reg(func(ctx context.Context, s *Service, _ int64, in *AdminTrafficExtDeleteInput) (any, error) {
		return nil, s.adminDeleteTrafficExtensionOption(ctx, in.TrafficGB)
	}),
	"plan.set": reg(func(ctx context.Context, s *Service, _ int64, in *WebAdminPlan) (any, error) {
		return nil, s.adminSetPlan(ctx, *in)
	}),
	"plan.delete": reg(func(ctx context.Context, s *Service, _ int64, in *AdminPlanDeleteInput) (any, error) {
		return nil, s.adminDeletePlan(ctx, in.Code)
	}),
	"requisites.set": reg(func(ctx context.Context, s *Service, _ int64, in *AdminTextInput) (any, error) {
		return nil, s.adminSetRequisites(ctx, in.Text)
	}),
	"screenshot.set": reg(func(ctx context.Context, s *Service, _ int64, in *AdminToggleInput) (any, error) {
		return nil, s.setRequireScreenshot(ctx, in.Enabled)
	}),
	"provider.set": reg(func(ctx context.Context, s *Service, _ int64, in *AdminProviderInput) (any, error) {
		return nil, s.setProviderEnabled(ctx, in.Provider, in.Enabled)
	}),
	"squad.default.set": reg(func(ctx context.Context, s *Service, _ int64, in *AdminUUIDInput) (any, error) {
		_, err := s.setDefaultSquad(ctx, in.UUID)
		return nil, err
	}),
	"traffic_reset.set": reg(func(ctx context.Context, s *Service, _ int64, in *AdminStrategyInput) (any, error) {
		return nil, s.setDefaultTrafficReset(ctx, in.Strategy)
	}),
	"trial.set": reg(func(ctx context.Context, s *Service, _ int64, in *AdminTrialInput) (any, error) {
		return nil, s.setTrialConfig(ctx, in.config())
	}),
	"referral.set": reg(func(ctx context.Context, s *Service, _ int64, in *AdminReferralInput) (any, error) {
		return nil, s.adminSetReferral(ctx, in.Enabled, in.InviterDays, in.InviteeDays)
	}),
	"guard.pattern.add": reg(func(ctx context.Context, s *Service, actorID int64, in *AdminPatternInput) (any, error) {
		return nil, s.adminAddRegistrationGuardPattern(ctx, actorID, in.Pattern)
	}),
	"guard.pattern.delete": reg(func(ctx context.Context, s *Service, _ int64, in *AdminPatternInput) (any, error) {
		return nil, s.adminDeleteRegistrationGuardPattern(ctx, in.Pattern)
	}),
	"guard.unblock": reg(func(ctx context.Context, s *Service, _ int64, in *AdminUnblockInput) (any, error) {
		return nil, s.adminUnblockRegistration(ctx, in.TelegramID)
	}),
	"proxy_notification.set": reg(func(ctx context.Context, s *Service, _ int64, in *AdminProxyNotifInput) (any, error) {
		return nil, s.adminSetProxyNotification(ctx, in.Name, in.Address, in.SubName, in.Muted)
	}),

	// User management and broadcast.
	"user.update": reg(func(ctx context.Context, s *Service, _ int64, in *WebUserUpdate) (any, error) {
		return nil, s.adminUpdateUser(ctx, *in)
	}),
	"broadcast.start": reg(func(ctx context.Context, s *Service, actorID int64, in *AdminBroadcastInput) (any, error) {
		return s.adminBroadcast(ctx, actorID, in.Message)
	}),

	// Request lifecycle (confirm / reject / delete) and gift revoke.
	"request.confirm": reg(func(ctx context.Context, s *Service, actorID int64, in *AdminIDInput) (any, error) {
		return nil, s.adminConfirmRequest(ctx, actorID, in.ID)
	}),
	"request.reject": reg(func(ctx context.Context, s *Service, _ int64, in *AdminIDInput) (any, error) {
		return nil, s.adminRejectRequest(ctx, in.ID)
	}),
	"request.delete": reg(func(ctx context.Context, s *Service, _ int64, in *AdminIDInput) (any, error) {
		return nil, s.adminDeletePaymentRequest(ctx, in.ID)
	}),
	"gift.revoke": reg(func(ctx context.Context, s *Service, _ int64, in *AdminIDInput) (any, error) {
		return nil, s.adminRevokeGiftCode(ctx, in.ID)
	}),
	"gift.delete": reg(func(ctx context.Context, s *Service, _ int64, in *AdminIDInput) (any, error) {
		return nil, s.adminDeleteGiftCode(ctx, in.ID)
	}),
	"gift_request.confirm": reg(func(ctx context.Context, s *Service, _ int64, in *AdminIDInput) (any, error) {
		return nil, s.adminConfirmGiftRequest(ctx, in.ID)
	}),
	"gift_request.reject": reg(func(ctx context.Context, s *Service, _ int64, in *AdminIDInput) (any, error) {
		return nil, s.adminRejectGiftRequest(ctx, in.ID)
	}),
	"invite.delete": reg(func(ctx context.Context, s *Service, _ int64, in *AdminIDInput) (any, error) {
		return nil, s.adminDeleteInviteRequest(ctx, in.ID)
	}),
	"invite_request.approve": reg(func(ctx context.Context, s *Service, _ int64, in *AdminIDInput) (any, error) {
		return nil, s.adminApproveInviteRequest(ctx, in.ID)
	}),
	"invite_request.reject": reg(func(ctx context.Context, s *Service, _ int64, in *AdminIDInput) (any, error) {
		return nil, s.adminRejectInviteRequest(ctx, in.ID)
	}),
	"trial_request.approve": reg(func(ctx context.Context, s *Service, _ int64, in *AdminIDInput) (any, error) {
		return nil, s.adminApproveTrialRequest(ctx, in.ID)
	}),
	"trial_request.reject": reg(func(ctx context.Context, s *Service, _ int64, in *AdminIDInput) (any, error) {
		return nil, s.adminRejectTrialRequest(ctx, in.ID)
	}),

	// Infrastructure servers and FX.
	"server.save": reg(func(ctx context.Context, s *Service, _ int64, in *WebInfraServerInput) (any, error) {
		return nil, s.adminSaveInfraServer(ctx, *in)
	}),
	"server.delete": reg(func(ctx context.Context, s *Service, _ int64, in *AdminIDInput) (any, error) {
		return nil, s.adminDeleteInfraServer(ctx, in.ID)
	}),
	"server.paid": reg(func(ctx context.Context, s *Service, _ int64, in *AdminIDInput) (any, error) {
		return nil, s.markInfraServerPaid(ctx, in.ID)
	}),
	"fx.manual.set": reg(func(ctx context.Context, s *Service, _ int64, in *AdminFxRateInput) (any, error) {
		return nil, s.adminSetManualFxRate(ctx, in.Currency, in.Rate)
	}),
	"fx.base.set": reg(func(ctx context.Context, s *Service, _ int64, in *AdminCurrencyInput) (any, error) {
		return nil, s.adminSetBaseCurrency(ctx, in.Currency)
	}),
	"fx.refresh": regNoIn(func(ctx context.Context, s *Service, _ int64) (any, error) {
		return nil, s.RefreshInfraFxRates(ctx)
	}),

	// Support (admin side).
	"support.send": reg(func(ctx context.Context, s *Service, actorID int64, in *AdminSupportSendInput) (any, error) {
		return nil, s.supportSendAdmin(ctx, actorID, in.UserID, in.Text)
	}),
	"support.close": reg(func(ctx context.Context, s *Service, _ int64, in *AdminIDInput) (any, error) {
		return nil, s.supportCloseAdmin(ctx, in.ID)
	}),
	"support.ticket.reply": reg(func(ctx context.Context, s *Service, actorID int64, in *AdminTicketReplyInput) (any, error) {
		return nil, s.supportReplyTicketAdmin(ctx, actorID, in.TicketID, in.Text)
	}),
	"support.ticket.close": reg(func(ctx context.Context, s *Service, _ int64, in *AdminTicketInput) (any, error) {
		return nil, s.supportCloseTicketAdmin(ctx, in.TicketID)
	}),
}
