package payments

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Nakedjustice/remnaWake/internal/i18n"
	"github.com/Nakedjustice/remnaWake/internal/store"
)

// Errors returned by the mini app API methods, mapped to HTTP statuses by the
// webapp handler.
var (
	ErrNotLinked          = errors.New("telegram id is not linked to any profile")
	ErrProfileUnknown     = errors.New("profile does not belong to this user")
	ErrTariffUnknown      = errors.New("tariff not found")
	ErrInfraServerUnknown = errors.New("infra server not found")
	ErrPaymentsDisabled   = errors.New("payment flow is disabled (no admin configured)")
	// ErrScreenshotRequired: the renew request was not created because the
	// admin requires a payment screenshot; the user was asked to attach it in
	// the bot chat.
	ErrScreenshotRequired         = errors.New("payment screenshot required")
	ErrInvalidProfileQuery        = errors.New("invalid profile query")
	ErrProfileLinkedElsewhere     = errors.New("profile linked to another telegram account")
	ErrGiftInvalid                = errors.New("gift code invalid")
	ErrGiftUsed                   = errors.New("gift code already used")
	ErrReceiptSessionExpired      = errors.New("receipt session expired")
	ErrReceiptType                = errors.New("invalid receipt type")
	ErrReceiptTooLarge            = errors.New("receipt too large")
	ErrPaymentRequestInaccessible = errors.New("payment request inaccessible")
	ErrProviderUnavailable        = errors.New("payment provider unavailable")
	// ErrTicketNotFound conceals foreign tickets: a ticket that exists but
	// belongs to another user is reported exactly like a missing one.
	ErrTicketNotFound = errors.New("support ticket not found")
	ErrTicketClosed   = errors.New("support ticket is closed")

	// Free-trial errors shared by the bot flow and the mini app claim endpoint.
	ErrTrialDisabled    = errors.New("free trial is disabled")
	ErrTrialNotEligible = errors.New("trial is for new users only")
	ErrTrialAlreadyUsed = errors.New("trial already used by this telegram id")
	ErrUsernameTaken    = errors.New("username already taken")
	// ErrTrialPendingApproval is a success-but-pending control signal: the trial
	// requires admin approval, the request was recorded and admins notified.
	ErrTrialPendingApproval = errors.New("trial request submitted for admin approval")
	// ErrTrialRequestPending means a trial request from this user is already
	// awaiting an admin decision (duplicate submit).
	ErrTrialRequestPending = errors.New("trial request already awaiting admin approval")
)

// WebProfile is one linked subscription as shown in the mini app.
type WebProfile struct {
	RemnawaveID      int64                     `json:"remnawave_id"`
	UUID             string                    `json:"uuid,omitempty"`
	Username         string                    `json:"username"`
	Status           string                    `json:"status"`
	StatusLabel      string                    `json:"status_label"`
	ExpireAt         string                    `json:"expire_at,omitempty"` // DD.MM.YYYY
	DaysLeft         int                       `json:"days_left"`
	HwidLimit        int                       `json:"hwid_limit"`                // 0 = unlimited
	TrafficLimitGB   int64                     `json:"traffic_limit_gb"`          // 0 = unlimited
	UsedTrafficGB    string                    `json:"used_traffic_gb,omitempty"` // formatted GB, e.g. "1.3"
	SubscriptionURL  string                    `json:"subscription_url,omitempty"`
	TrafficExtension *WebTrafficExtensionState `json:"traffic_extension,omitempty"`
}

// WebTariff is one renewal option as shown in the mini app.
type WebTariff struct {
	Months     int    `json:"months"`
	Price      int    `json:"price"`
	PriceLabel string `json:"price_label"`
}

// WebPlan is one tariff preset with its renewal options as shown in the mini
// app. Zero traffic/device limits mean unlimited.
type WebPlan struct {
	Code        string      `json:"code"`
	Name        string      `json:"name"`
	TrafficGB   int         `json:"traffic_gb"`
	DeviceLimit int         `json:"device_limit"`
	Tariffs     []WebTariff `json:"tariffs,omitempty"`
}

// WebGift is one bought gift code as shown in the mini app.
type WebGift struct {
	Code             string `json:"code"`
	Months           int    `json:"months"`
	Status           string `json:"status"`
	StatusLabel      string `json:"status_label"`
	RedeemedUsername string `json:"redeemed_username,omitempty"` // who activated a redeemed gift
	CreatedAt        string `json:"created_at"`                  // DD.MM.YYYY
	Link             string `json:"link,omitempty"`              // t.me redemption deep link, issued gifts only
}

// WebInvites is the invite-requests summary shown in the mini app.
type WebInvites struct {
	Total   int `json:"total"`
	Pending int `json:"pending"`
}

// WebReferral carries the user's personal referral link, the configured bonus
// amounts and their referral counts. Present only when the program is enabled.
type WebReferral struct {
	Link        string `json:"link,omitempty"`
	InviterDays int    `json:"inviter_days"`
	InviteeDays int    `json:"invitee_days"`
	Invited     int    `json:"invited"`
	Credited    int    `json:"credited"`
}

// WebNotifPrefs carries the user's notification mute toggles for the mini app.
type WebNotifPrefs struct {
	ExpiryMuted  bool `json:"expiry_muted"`
	WinbackMuted bool `json:"winback_muted"`
}

// WebTrialResult is returned after a successful free-trial claim from the mini
// app: the created profile name and its subscription link.
type WebTrialResult struct {
	Username        string `json:"username"`
	SubscriptionURL string `json:"subscription_url,omitempty"`
	// Pending is true when the trial required admin approval: the request was
	// recorded and the user must wait for an admin decision (no profile yet).
	Pending bool `json:"pending,omitempty"`
}

type WebTrialTerms struct {
	Days            int `json:"days"`
	TrafficLimitGB  int `json:"traffic_limit_gb"`
	HwidDeviceLimit int `json:"hwid_device_limit"`
}

type WebRegistrationResult struct {
	Username string `json:"username"`
}
type WebGiftRedemptionResult struct {
	Username        string `json:"username"`
	SubscriptionURL string `json:"subscription_url,omitempty"`
	ExpireAt        string `json:"expire_at,omitempty"`
	LinkFailed      bool   `json:"-"`
}
type WebPaymentStatus struct {
	Status string `json:"status"`
}
type WebReceipt struct {
	Filename    string
	ContentType string
	Data        []byte
	Note        string
}

// WebCabinet is the full /api/me payload for the mini app.
type WebCabinet struct {
	Linked            bool                        `json:"linked"`
	Lang              string                      `json:"lang"`
	IsAdmin           bool                        `json:"is_admin,omitempty"`
	Profiles          []WebProfile                `json:"profiles,omitempty"`
	Tariffs           []WebTariff                 `json:"tariffs,omitempty"`
	TrafficExtensions []WebTrafficExtensionOption `json:"traffic_extensions,omitempty"`
	// Plans lists every purchasable preset with its own price grid; the
	// top-level Tariffs stays the standard plan's grid (the gift flow and
	// older clients rely on it). Sent only when custom presets exist.
	Plans      []WebPlan   `json:"plans,omitempty"`
	Requisites string      `json:"requisites,omitempty"`
	Gifts      []WebGift   `json:"gifts,omitempty"`
	Invites    *WebInvites `json:"invites,omitempty"`
	// Referral carries the personal referral link + stats, when the program is
	// enabled. Reachable from the mini app and the bot.
	Referral *WebReferral `json:"referral,omitempty"`
	// Notifications carries the per-user mute toggles (always present).
	Notifications *WebNotifPrefs `json:"notifications,omitempty"`
	// TrialAvailable is true when the free trial is enabled and this user has no
	// linked profile yet, so the frontend can offer the claim card.
	TrialAvailable bool           `json:"trial_available,omitempty"`
	TrialTerms     *WebTrialTerms `json:"trial_terms,omitempty"`
}

// CabinetData assembles the personal-cabinet view for the mini app: linked
// profiles, tariffs, payment requisites and gift/invite summaries.
func (s *Service) CabinetData(ctx context.Context, telegramID int64) (*WebCabinet, error) {
	subs, err := s.finder.FindByTelegramID(ctx, telegramID)
	if err != nil {
		return nil, fmt.Errorf("find by telegram id: %w", err)
	}

	// Presentational only: every /api/admin call re-checks isAdmin server-side.
	out := &WebCabinet{Linked: len(subs) > 0, Lang: string(i18n.Current()), IsAdmin: s.isEnabled() && s.isAdmin(telegramID)}
	now := s.now()
	for i := range subs {
		sub := &subs[i]
		p := WebProfile{
			RemnawaveID:     sub.RemnawaveID,
			UUID:            sub.UUID,
			Username:        sub.Username,
			Status:          sub.Status,
			StatusLabel:     subStatusLabel(sub.Status),
			TrafficLimitGB:  sub.TrafficLimitBytes / bytesPerGB,
			SubscriptionURL: sub.SubscriptionURL,
		}
		if sub.HwidDeviceLimit != nil {
			p.HwidLimit = *sub.HwidDeviceLimit
		}
		if sub.UsedTrafficBytes > 0 {
			p.UsedTrafficGB = strconv.FormatFloat(float64(sub.UsedTrafficBytes)/float64(bytesPerGB), 'f', 2, 64)
		}
		if active, err := s.store.ActiveTrafficExtensionForUUID(ctx, sub.UUID, now); err == nil && active != nil && active.ExtensionExpiresAt != nil {
			p.TrafficExtension = &WebTrafficExtensionState{
				TrafficGB: active.TrafficGB,
				ExpiresAt: active.ExtensionExpiresAt.Format("02.01.2006"),
			}
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

	tariffs, err := s.store.ListTariffs(ctx, store.PlanStandard)
	if err != nil {
		s.logger.Error("webapp: list tariffs failed", "err", err.Error())
	}
	for _, t := range tariffs {
		out.Tariffs = append(out.Tariffs, WebTariff{Months: t.Months, Price: t.Price, PriceLabel: s.priceLabel(t.Price)})
	}
	out.Plans = s.webPlans(ctx, out.Tariffs)
	out.TrafficExtensions = s.webTrafficExtensionOptions(ctx)

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
			Code:             g.Code,
			Months:           g.Months,
			Status:           g.Status,
			StatusLabel:      giftStatusLabel(g),
			RedeemedUsername: g.RedeemedUsername,
			CreatedAt:        g.CreatedAt.Format("02.01.2006"),
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

	// Referral link + stats, when the program is enabled.
	if refEnabled, inviterDays, inviteeDays := s.referralConfig(); refEnabled {
		invited, credited, err := s.store.ReferralStats(ctx, telegramID)
		if err != nil {
			s.logger.Error("webapp: referral stats failed", "err", err.Error())
		}
		out.Referral = &WebReferral{
			Link:        s.referralDeepLink(telegramID),
			InviterDays: inviterDays,
			InviteeDays: inviteeDays,
			Invited:     invited,
			Credited:    credited,
		}
	}

	// Per-user notification mute toggles (always present so the card can render).
	expiryMuted, winbackMuted, err := s.store.GetNotificationPrefs(ctx, telegramID)
	if err != nil {
		s.logger.Error("webapp: get notification prefs failed", "err", err.Error())
	}
	out.Notifications = &WebNotifPrefs{ExpiryMuted: expiryMuted, WinbackMuted: winbackMuted}

	// Offer the free trial only to enabled deployments where this user has no
	// linked profile yet.
	if cfg := s.trialConfig(); cfg.Enabled && len(subs) == 0 {
		out.TrialAvailable = true
		out.TrialTerms = &WebTrialTerms{
			Days: cfg.Days, TrafficLimitGB: cfg.TrafficLimitGB, HwidDeviceLimit: cfg.HwidDeviceLimit,
		}
	}

	return out, nil
}

// SetNotificationPref toggles one of the user's notification mute flags from the
// mini app (kind must be store.NotificationExpiry or store.NotificationWinback).
func (s *Service) SetNotificationPref(ctx context.Context, telegramID int64, kind string, muted bool) error {
	switch kind {
	case store.NotificationExpiry, store.NotificationWinback:
	default:
		return ErrBadInput
	}
	if err := s.store.SetNotificationPref(ctx, telegramID, kind, muted); err != nil {
		return fmt.Errorf("set notification pref: %w", err)
	}
	return nil
}

// AdminSetProxyNotification mutes or unmutes up/down alerts for one proxy server
// from the admin Proxy Health tab. The mute is global (it silences the alert for
// all admins) and keyed by the proxy's canonical identity.
func (s *Service) AdminSetProxyNotification(ctx context.Context, telegramID int64, name, address, subName string, muted bool) error {
	if err := s.adminGuard(telegramID); err != nil {
		return err
	}
	if err := s.store.SetProxyNotifMuted(ctx, proxyMuteKey(address, name, subName), muted); err != nil {
		return fmt.Errorf("set proxy notification: %w", err)
	}
	return nil
}

// ClaimTrial activates the one-time free trial for telegramID from the mini app,
// reusing the shared claimTrial core. Returns the created profile and its
// subscription link, or a trial sentinel error mapped to an HTTP status by the
// webapp handler. On dry-run the result echoes the requested username.
func (s *Service) ClaimTrial(ctx context.Context, telegramID int64, username string) (*WebTrialResult, error) {
	created, _, err := s.claimTrial(ctx, telegramID, strings.TrimSpace(username))
	if errors.Is(err, ErrTrialPendingApproval) {
		return &WebTrialResult{Username: strings.TrimSpace(username), Pending: true}, nil
	}
	if err != nil {
		return nil, err
	}
	if created == nil { // dry-run
		return &WebTrialResult{Username: strings.TrimSpace(username)}, nil
	}
	return &WebTrialResult{Username: created.Username, SubscriptionURL: created.SubscriptionURL}, nil
}

// CreateRenewRequest creates a pending payment request from the mini app:
// verifies the profile belongs to telegramID, resolves the tariff price and
// either notifies the admins (p2p) or opens a Platega transaction. The returned
// string is a non-empty payment URL only for the Platega provider; for p2p it is
// empty and the request reaches the admins (with a trace in the user's chat).
// RenewResult tells the Mini App how to complete a renewal: which payment
// method was chosen (or a chooser to render) and any URL/link to open.
type RenewResult struct {
	RequestID int64 `json:"request_id,omitempty"`
	// Status is "" (P2P request sent), "plan_change_preview",
	// "choose_provider", "platega" or "telegram_stars".
	Status string `json:"status,omitempty"`
	// PayURL is the Platega redirect URL, set when Status == "platega".
	PayURL string `json:"payment_url,omitempty"`
	// InvoiceURL is the Telegram Stars invoice link (opened with openInvoice),
	// set when Status == "telegram_stars".
	InvoiceURL string `json:"invoice_url,omitempty"`
	// Providers lists the enabled providers to choose from, set when
	// Status == "choose_provider".
	Providers  []string           `json:"providers,omitempty"`
	PlanChange *PlanChangePreview `json:"plan_change,omitempty"`
}

func (s *Service) CreateRenewRequest(ctx context.Context, telegramID, remnawaveID int64, months int, provider, plan string, planChangeConfirmed bool) (*RenewResult, error) {
	if !s.isEnabled() {
		return nil, ErrPaymentsDisabled
	}
	planDef, err := s.getPlan(ctx, plan)
	if err != nil {
		return nil, err
	}
	plan = planDef.Code
	subs, err := s.finder.FindByTelegramID(ctx, telegramID)
	if err != nil {
		return nil, fmt.Errorf("find by telegram id: %w", err)
	}
	if len(subs) == 0 {
		return nil, ErrNotLinked
	}
	var sub *Subscriber
	for i := range subs {
		if subs[i].RemnawaveID == remnawaveID {
			sub = &subs[i]
			break
		}
	}
	if sub == nil {
		return nil, ErrProfileUnknown
	}

	// The months=1/price=0 fallback for an empty grid is a legacy standard-only
	// behavior; a custom preset must always name an existing tariff.
	price := 0
	tariffs, err := s.store.ListTariffs(ctx, plan)
	if err != nil {
		return nil, fmt.Errorf("list tariffs: %w", err)
	}
	switch {
	case len(tariffs) > 0:
		tariff, err := s.store.GetTariff(ctx, plan, months)
		if err != nil {
			return nil, fmt.Errorf("get tariff: %w", err)
		}
		if tariff == nil {
			return nil, ErrTariffUnknown
		}
		price = tariff.Price
	case plan != store.PlanStandard:
		return nil, ErrTariffUnknown
	case months < 1:
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

	previewReq := &store.PaymentRequest{
		RemnawaveID: sub.RemnawaveID, UUID: sub.UUID, Username: sub.Username,
		TelegramID: telegramID, Months: months, Price: price, ExpireAt: sub.ExpireAt,
		Plan: plan,
	}
	if preview := s.renewalPlanChangePreview(ctx, previewReq, true); preview != nil && !planChangeConfirmed {
		return &RenewResult{Status: RenewStatusPlanChangePreview, PlanChange: preview}, nil
	}

	if provider != "" && (!s.providerAvailable(provider) || !s.isProviderEnabled(provider)) {
		return nil, ErrBadInput
	}

	// Resolve the provider: when none is requested and several are enabled, ask
	// the mini app to render a chooser; otherwise validate the requested one.
	enabled := s.enabledProviders()
	if provider == "" {
		if len(enabled) > 1 {
			return &RenewResult{Status: "choose_provider", Providers: enabled}, nil
		}
		provider = enabled[0]
	}

	switch provider {
	case ProviderPlatega:
		// Open an online transaction and return its pay URL for the mini app to
		// open. The screenshot requirement does not apply (payment is automatic).
		reqID, payURL, err := s.startPlategaPayment(ctx, u, months, price, plan)
		if err != nil {
			return nil, err
		}
		return &RenewResult{Status: ProviderPlatega, RequestID: reqID, PayURL: payURL}, nil
	case ProviderTelegramStars:
		// Create a Stars invoice link the mini app opens with openInvoice.
		link, err := s.startStarsInvoiceLink(ctx, u, months, price, plan)
		if err != nil {
			return nil, err
		}
		return &RenewResult{Status: ProviderTelegramStars, InvoiceURL: link}, nil
	}

	// P2P: with the screenshot requirement on, the request is deferred (the mini
	// app cannot upload photos to the bot, so the user finishes in the bot chat).
	if s.getRequireScreenshot() {
		s.startPayPhotoFlow(ctx, telegramID, sub.RemnawaveID, months, price, plan)
		return nil, ErrScreenshotRequired
	}

	if _, err := s.createPaymentRequest(ctx, u, months, price, plan, nil); err != nil {
		return nil, err
	}

	_ = s.bot.SendPlain(ctx, telegramID, fmt.Sprintf(
		i18n.T("✅ Заявка на продление «%s» на %d мес. отправлена администратору. После подтверждения оплаты подписка будет продлена."),
		sub.Username, months))
	return &RenewResult{}, nil
}

func (s *Service) CreateTrafficExtensionRequest(ctx context.Context, telegramID, remnawaveID int64, trafficGB int, provider string) (*RenewResult, error) {
	if !s.isEnabled() {
		return nil, ErrPaymentsDisabled
	}
	opt, err := s.store.GetTrafficExtensionOption(ctx, trafficGB)
	if err != nil {
		return nil, fmt.Errorf("get traffic extension option: %w", err)
	}
	if opt == nil {
		return nil, ErrTrafficExtensionUnknown
	}
	subs, err := s.finder.FindByTelegramID(ctx, telegramID)
	if err != nil {
		return nil, fmt.Errorf("find by telegram id: %w", err)
	}
	if len(subs) == 0 {
		return nil, ErrNotLinked
	}
	var sub *Subscriber
	for i := range subs {
		if subs[i].RemnawaveID == remnawaveID {
			sub = &subs[i]
			break
		}
	}
	if sub == nil {
		return nil, ErrProfileUnknown
	}
	if sub.TrafficLimitBytes <= 0 {
		return nil, ErrTrafficExtensionUnavailable
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

	enabled := s.enabledProviders()
	if provider == "" {
		if len(enabled) > 1 {
			return &RenewResult{Status: "choose_provider", Providers: enabled}, nil
		}
		provider = enabled[0]
	} else if !s.providerAvailable(provider) || !s.isProviderEnabled(provider) {
		return nil, ErrBadInput
	}

	switch provider {
	case ProviderPlatega:
		reqID, payURL, err := s.startPlategaTrafficExtension(ctx, u, trafficGB, opt.Price, sub.TrafficLimitBytes)
		if err != nil {
			return nil, err
		}
		return &RenewResult{Status: ProviderPlatega, RequestID: reqID, PayURL: payURL}, nil
	case ProviderTelegramStars:
		link, err := s.startStarsTrafficExtensionLink(ctx, u, trafficGB, opt.Price, sub.TrafficLimitBytes)
		if err != nil {
			return nil, err
		}
		return &RenewResult{Status: ProviderTelegramStars, InvoiceURL: link}, nil
	}
	if s.getRequireScreenshot() {
		s.startTrafficPayPhotoFlow(ctx, telegramID, sub.RemnawaveID, trafficGB, opt.Price, sub.TrafficLimitBytes)
		return nil, ErrScreenshotRequired
	}
	if _, err := s.createTrafficExtensionPaymentRequest(ctx, u, trafficGB, opt.Price, sub.TrafficLimitBytes, ProviderP2P, nil); err != nil {
		return nil, err
	}
	_ = s.bot.SendPlain(ctx, telegramID, fmt.Sprintf(
		i18n.T("✅ Заявка на докупку %d ГБ для «%s» отправлена администратору."),
		trafficGB, sub.Username))
	return &RenewResult{}, nil
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
	tariff, err := s.store.GetTariff(ctx, store.PlanStandard, 1)
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
