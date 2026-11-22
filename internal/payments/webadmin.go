package payments

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Nakedjustice/remnaWake/internal/i18n"
	"github.com/Nakedjustice/remnaWake/internal/store"
)

// WebAdminGift is one active gift code as shown in the mini app admin panel.
// Not-used (issued) codes are revocable; used (redeemed) codes carry redeemer
// info instead.
type WebAdminGift struct {
	ID        int64  `json:"id"`
	Code      string `json:"code"`
	Months    int    `json:"months"`
	Buyer     string `json:"buyer"`
	CreatedAt string `json:"created_at"`         // DD.MM.YYYY
	Redeemer  string `json:"redeemer,omitempty"` // set for used codes
}

// WebAdminGiftBuyer groups one buyer's active gift codes into Not used and Used
// buckets for the mini app admin gift browser.
type WebAdminGiftBuyer struct {
	BuyerTelegramID int64          `json:"buyer_telegram_id"`
	Buyer           string         `json:"buyer"`
	NotUsed         []WebAdminGift `json:"not_used"`
	Used            []WebAdminGift `json:"used"`
}

// WebAdminRequest is one pending payment request as shown in the mini app
// admin panel.
type WebAdminRequest struct {
	ID         int64  `json:"id"`
	Username   string `json:"username"`
	Months     int    `json:"months"`
	Kind       string `json:"kind,omitempty"`
	TrafficGB  int    `json:"traffic_gb,omitempty"`
	Price      int    `json:"price"`
	PriceLabel string `json:"price_label,omitempty"`
	ExpireAt   string `json:"expire_at,omitempty"` // DD.MM.YYYY
	CreatedAt  string `json:"created_at"`          // DD.MM.YYYY
	// HasScreenshot marks requests that carry a payment screenshot; the photo
	// itself is delivered to the admins' Telegram chats.
	HasScreenshot bool `json:"has_screenshot,omitempty"`
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

// WebAdminTrialRequest is one pending free-trial request awaiting an admin
// decision (only created when the trial requires approval).
type WebAdminTrialRequest struct {
	ID         int64  `json:"id"`
	TelegramID int64  `json:"telegram_id"`
	Username   string `json:"username"`
	CreatedAt  string `json:"created_at"` // DD.MM.YYYY
}

// WebAdminPlan is one admin-defined tariff preset as shown (and edited) in the
// mini app admin panel. The squad is exposed as a UUID; the frontend maps it
// to a name via the squad list it already loads for the default-squad picker.
type WebAdminPlan struct {
	Code        string      `json:"code"`
	Name        string      `json:"name"`
	Tag         string      `json:"tag"`
	SquadUUID   string      `json:"squad_uuid"`
	TrafficGB   int         `json:"traffic_gb"`
	DeviceLimit int         `json:"device_limit"`
	Tariffs     []WebTariff `json:"tariffs,omitempty"`
}

// WebRegistrationGuard is one active automatic registration block displayed
// in the Mini App administrator queue. The frontend must render every string
// as text, never as HTML.
type WebRegistrationGuard struct {
	TelegramID     int64  `json:"telegram_id"`
	Username       string `json:"username,omitempty"`
	FirstName      string `json:"first_name,omitempty"`
	LastName       string `json:"last_name,omitempty"`
	MatchedPattern string `json:"matched_pattern"`
	CreatedAt      string `json:"created_at,omitempty"`
}

// WebAdminPanel is the full /api/admin payload for the mini app.
type WebAdminPanel struct {
	Tariffs           []WebTariff                 `json:"tariffs"`
	TrafficExtensions []WebTrafficExtensionOption `json:"traffic_extensions,omitempty"`
	// Plans lists the custom tariff presets (the built-in standard plan is the
	// top-level Tariffs grid and is not editable here).
	Plans          []WebAdminPlan          `json:"plans,omitempty"`
	Requisites     string                  `json:"requisites"`
	GiftBuyers     []WebAdminGiftBuyer     `json:"gift_buyers"`
	Requests       []WebAdminRequest       `json:"requests"`
	GiftRequests   []WebAdminGiftRequest   `json:"gift_requests"`
	InviteRequests []WebAdminInviteRequest `json:"invite_requests"`
	TrialRequests  []WebAdminTrialRequest  `json:"trial_requests"`
	// DefaultSquadName is the display name of the admin-selected internal
	// squad for new users; empty when none is selected yet (the by-name
	// Default-Squad fallback applies then). Read from settings only, so the
	// panel payload never waits on the Remnawave API.
	DefaultSquadName string `json:"default_squad_name"`
	// RequireScreenshot mirrors the "payment screenshot required" toggle.
	RequireScreenshot bool `json:"require_screenshot"`
	// AvailableProviders lists the configured payment providers the admin can
	// enable (always includes "p2p"; "platega"/"telegram_stars" when wired in).
	AvailableProviders []string `json:"available_providers"`
	// EnabledProviders lists the currently enabled providers (subset of
	// AvailableProviders). More than one means the user picks at pay time.
	EnabledProviders []string `json:"enabled_providers"`
	// DefaultTrafficResetStrategy is the traffic-reset strategy applied to newly
	// created users (NO_RESET | DAY | WEEK | MONTH).
	DefaultTrafficResetStrategy string `json:"default_traffic_reset_strategy"`
	// Trial / referral runtime settings, editable from the admin panel.
	TrialEnabled         bool   `json:"trial_enabled"`
	TrialDays            int    `json:"trial_days"`
	TrialTrafficLimitGB  int    `json:"trial_traffic_limit_gb"`
	TrialHwidDeviceLimit int    `json:"trial_hwid_device_limit"`
	TrialSquadUUID       string `json:"trial_squad_uuid"`
	TrialRequireApproval bool   `json:"trial_require_approval"`
	ReferralEnabled      bool   `json:"referral_enabled"`
	ReferralInviterDays  int    `json:"referral_inviter_days"`
	ReferralInviteeDays  int    `json:"referral_invitee_days"`
	// ProxyMonitoring is true when an xray-checker sidecar is wired in, so the
	// Mini App can show the proxy-health entry.
	ProxyMonitoring      bool   `json:"proxy_monitoring"`
	AutoUpdateConfigured bool   `json:"autoupdate_configured"`
	AutoUpdateInterval   string `json:"autoupdate_interval,omitempty"`
	// Scheduled database backup, editable from the admin panel exactly as from
	// the bot admin menu.
	BackupEnabled         bool `json:"backup_enabled"`
	BackupIntervalDays    int  `json:"backup_interval_days"`
	BackupMaxIntervalDays int  `json:"backup_max_interval_days"`
	// RegistrationGuardPatterns are operator-defined additions; built-in
	// impersonation patterns are intentionally fixed in code.
	RegistrationGuardPatterns []string               `json:"registration_guard_patterns,omitempty"`
	BlockedRegistrations      []WebRegistrationGuard `json:"blocked_registrations,omitempty"`
}

type WebUpdateCheckResult struct {
	Status   string `json:"status"`
	Message  string `json:"message"`
	Baseline string `json:"baseline,omitempty"`
	Remote   string `json:"remote,omitempty"`
}

// WebSquad is one panel internal squad offered in the mini app default-squad
// picker.
type WebSquad struct {
	UUID          string `json:"uuid"`
	Name          string `json:"name"`
	Selected      bool   `json:"selected"`
	TrialSelected bool   `json:"trial_selected"`
}

// adminGuard rejects calls from non-admin Telegram IDs. It runs once per
// operation inside RunAdminOp — the only entry point for admin ops — so no
// transport can bypass it. The ID comes from validated initData (web) or the
// callback sender (bot); the is_admin flag in /api/me is presentational only.
func (s *Service) adminGuard(telegramID int64) error {
	if !s.isEnabled() || !s.isAdmin(telegramID) {
		return ErrNotAdmin
	}
	return nil
}

// adminPanelData assembles the admin panel view: tariffs, requisites, issued
// gift codes and pending payment requests. Reached via the "panel.get" op.
func (s *Service) adminPanelData(ctx context.Context) (*WebAdminPanel, error) {
	out := &WebAdminPanel{}

	allTariffs, err := s.store.ListAllTariffs(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tariffs: %w", err)
	}
	tariffsByPlan := map[string][]WebTariff{}
	for _, t := range allTariffs {
		tariffsByPlan[t.Plan] = append(tariffsByPlan[t.Plan], WebTariff{Months: t.Months, Price: t.Price, PriceLabel: s.priceLabel(t.Price)})
	}
	out.Tariffs = tariffsByPlan[store.PlanStandard]
	out.TrafficExtensions = s.webTrafficExtensionOptions(ctx)

	plans, err := s.store.ListPlans(ctx)
	if err != nil {
		return nil, fmt.Errorf("list plans: %w", err)
	}
	for _, p := range plans {
		out.Plans = append(out.Plans, WebAdminPlan{
			Code: p.Code, Name: p.Name, Tag: p.Tag, SquadUUID: p.SquadUUID,
			TrafficGB: p.TrafficGB, DeviceLimit: p.DeviceLimit,
			Tariffs: tariffsByPlan[p.Code],
		})
	}

	s.mu.Lock()
	out.Requisites = s.requisites
	out.RequireScreenshot = s.requireScreenshot
	s.mu.Unlock()

	for _, name := range allProviders {
		if s.providerAvailable(name) {
			out.AvailableProviders = append(out.AvailableProviders, name)
			if s.isProviderEnabled(name) {
				out.EnabledProviders = append(out.EnabledProviders, name)
			}
		}
	}

	_, out.DefaultSquadName = s.defaultSquadSelection(ctx)
	out.DefaultTrafficResetStrategy = s.getDefaultTrafficReset()

	trial := s.trialConfig()
	out.TrialEnabled = trial.Enabled
	out.TrialDays = trial.Days
	out.TrialTrafficLimitGB = trial.TrafficLimitGB
	out.TrialHwidDeviceLimit = trial.HwidDeviceLimit
	out.TrialSquadUUID = trial.SquadUUID
	out.TrialRequireApproval = trial.RequireApproval
	out.ReferralEnabled, out.ReferralInviterDays, out.ReferralInviteeDays = s.referralConfig()
	out.ProxyMonitoring = s.xrayCheckerConfigured()
	if checker := s.updateCheckerLocked(); checker != nil {
		out.AutoUpdateConfigured = true
		out.AutoUpdateInterval = checker.Interval().String()
	}
	out.BackupEnabled, out.BackupIntervalDays = s.backupConfig()
	out.BackupMaxIntervalDays = maxBackupIntervalDays
	out.RegistrationGuardPatterns = s.customRegistrationGuardPatterns()
	blockedRegistrations, err := s.store.ListBlockedRegistrationGuards(ctx)
	if err != nil {
		return nil, fmt.Errorf("list blocked registrations: %w", err)
	}
	for _, guard := range blockedRegistrations {
		out.BlockedRegistrations = append(out.BlockedRegistrations, WebRegistrationGuard{
			TelegramID:     guard.TelegramID,
			Username:       guard.Username,
			FirstName:      guard.FirstName,
			LastName:       guard.LastName,
			MatchedPattern: guard.MatchedPattern,
			CreatedAt:      guard.CreatedAt.Local().Format("02.01.2006 15:04"),
		})
	}

	buyers, err := s.store.ListGiftBuyers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list gift buyers: %w", err)
	}
	for _, b := range buyers {
		name := b.BuyerUsername
		if name == "" {
			name = fmt.Sprintf("%d", b.BuyerTelegramID)
		}
		group := WebAdminGiftBuyer{BuyerTelegramID: b.BuyerTelegramID, Buyer: name}

		notUsed, err := s.store.ListGiftCodesByBuyerStatus(ctx, b.BuyerTelegramID, "issued", b.NotUsed, 0)
		if err != nil {
			return nil, fmt.Errorf("list not-used gifts: %w", err)
		}
		for i := range notUsed {
			g := &notUsed[i]
			group.NotUsed = append(group.NotUsed, WebAdminGift{
				ID: g.ID, Code: g.Code, Months: g.Months, Buyer: name,
				CreatedAt: g.CreatedAt.Format("02.01.2006"),
			})
		}

		used, err := s.store.ListGiftCodesByBuyerStatus(ctx, b.BuyerTelegramID, "redeemed", b.Used, 0)
		if err != nil {
			return nil, fmt.Errorf("list used gifts: %w", err)
		}
		for i := range used {
			g := &used[i]
			group.Used = append(group.Used, WebAdminGift{
				ID: g.ID, Code: g.Code, Months: g.Months, Buyer: name,
				CreatedAt: g.CreatedAt.Format("02.01.2006"),
				Redeemer:  g.RedeemedUsername,
			})
		}

		out.GiftBuyers = append(out.GiftBuyers, group)
	}

	requests, err := s.store.ListPaymentRequestsByStatus(ctx, "pending")
	if err != nil {
		return nil, fmt.Errorf("list payment requests: %w", err)
	}
	for i := range requests {
		r := &requests[i]
		wr := WebAdminRequest{
			ID:            r.ID,
			Username:      r.Username,
			Months:        r.Months,
			Kind:          r.Kind,
			TrafficGB:     r.TrafficGB,
			Price:         r.Price,
			CreatedAt:     r.CreatedAt.Format("02.01.2006"),
			HasScreenshot: r.ScreenshotFileID != "",
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

	trialReqs, err := s.store.ListTrialRequestsByStatus(ctx, "pending")
	if err != nil {
		return nil, fmt.Errorf("list trial requests: %w", err)
	}
	for i := range trialReqs {
		r := &trialReqs[i]
		out.TrialRequests = append(out.TrialRequests, WebAdminTrialRequest{
			ID:         r.ID,
			TelegramID: r.TelegramID,
			Username:   r.Username,
			CreatedAt:  r.CreatedAt.Format("02.01.2006"),
		})
	}

	return out, nil
}

func (s *Service) adminCheckUpdates(ctx context.Context) (*WebUpdateCheckResult, error) {
	checker := s.updateCheckerLocked()
	if checker == nil {
		return nil, ErrBadInput
	}
	result, err := checker.CheckNow(ctx)
	if err != nil {
		return nil, err
	}
	return &WebUpdateCheckResult{
		Status:   string(result.Status),
		Message:  formatUpdateCheckResult(result),
		Baseline: shortDigest(result.Baseline),
		Remote:   shortDigest(result.Remote),
	}, nil
}

func (s *Service) adminSetUpdateInterval(ctx context.Context, raw string) error {
	interval, err := parseUpdateInterval(raw)
	if err != nil {
		return ErrBadInput
	}
	checker := s.updateCheckerLocked()
	if checker == nil {
		return ErrBadInput
	}
	return checker.SetInterval(ctx, interval)
}

// adminSetBackupSchedule is the core of the "backup.schedule.set" op: it
// persists the scheduled-backup state and cadence through the same core as the
// adm:backup:* callbacks, so both surfaces share the reset-on-change rule and
// the interval ceiling.
func (s *Service) adminSetBackupSchedule(ctx context.Context, enabled bool, intervalDays int) error {
	if intervalDays < 1 || intervalDays > maxBackupIntervalDays {
		return ErrBadInput
	}
	wasEnabled, current := s.backupConfig()
	resetCadence := enabled && (!wasEnabled || intervalDays != current)
	if err := s.setBackupConfig(ctx, enabled, intervalDays, resetCadence); err != nil {
		return fmt.Errorf("save backup schedule: %w", err)
	}
	return nil
}

// adminSetTariff adds or updates a tariff. An empty plan targets the built-in
// standard grid; a custom plan must exist.
func (s *Service) adminSetTariff(ctx context.Context, plan string, months, price int) error {
	if months < 1 || price < 1 {
		return ErrBadInput
	}
	planDef, err := s.getPlan(ctx, plan)
	if err != nil {
		return err
	}
	if err := s.store.UpsertTariff(ctx, planDef.Code, months, price); err != nil {
		return fmt.Errorf("upsert tariff: %w", err)
	}
	return nil
}

// adminDeleteTariff removes a tariff.
func (s *Service) adminDeleteTariff(ctx context.Context, plan string, months int) error {
	planDef, err := s.getPlan(ctx, plan)
	if err != nil {
		return err
	}
	deleted, err := s.store.DeleteTariff(ctx, planDef.Code, months)
	if err != nil {
		return fmt.Errorf("delete tariff: %w", err)
	}
	if !deleted {
		return ErrTariffUnknown
	}
	return nil
}

func (s *Service) adminSetTrafficExtensionOption(ctx context.Context, trafficGB, price int) error {
	if trafficGB < 1 || price < 1 {
		return ErrBadInput
	}
	if err := s.store.UpsertTrafficExtensionOption(ctx, trafficGB, price); err != nil {
		return fmt.Errorf("upsert traffic extension option: %w", err)
	}
	return nil
}

func (s *Service) adminDeleteTrafficExtensionOption(ctx context.Context, trafficGB int) error {
	deleted, err := s.store.DeleteTrafficExtensionOption(ctx, trafficGB)
	if err != nil {
		return fmt.Errorf("delete traffic extension option: %w", err)
	}
	if !deleted {
		return ErrTrafficExtensionUnknown
	}
	return nil
}

// adminSetPlan creates or updates a custom tariff preset. The built-in standard
// plan is not editable; the tag must match the panel's tag rules (uppercase) or
// be empty; a non-empty squad UUID must name an existing internal squad.
func (s *Service) adminSetPlan(ctx context.Context, in WebAdminPlan) error {
	code := strings.ToLower(strings.TrimSpace(in.Code))
	name := strings.TrimSpace(in.Name)
	tag := strings.ToUpper(strings.TrimSpace(in.Tag))
	if code == store.PlanStandard || !planCodePattern.MatchString(code) {
		return ErrBadInput
	}
	if name == "" || in.TrafficGB < 0 || in.DeviceLimit < 0 {
		return ErrBadInput
	}
	if tag != "" && !planTagPattern.MatchString(tag) {
		return ErrBadInput
	}
	squadUUID := strings.TrimSpace(in.SquadUUID)
	if squadUUID != "" {
		if s.squads == nil {
			return ErrPanelUnavailable
		}
		squads, err := s.squads.GetInternalSquads(ctx)
		if err != nil {
			s.logger.Error("set plan: list squads failed", "err", err.Error())
			return fmt.Errorf("%w: %v", ErrPanelUnavailable, err)
		}
		known := false
		for _, sq := range squads {
			if sq.UUID == squadUUID {
				known = true
				break
			}
		}
		if !known {
			return ErrBadInput
		}
	}
	if err := s.store.UpsertPlan(ctx, store.Plan{
		Code: code, Name: name, Tag: tag, SquadUUID: squadUUID,
		TrafficGB: in.TrafficGB, DeviceLimit: in.DeviceLimit,
	}); err != nil {
		return fmt.Errorf("upsert plan: %w", err)
	}
	return nil
}

// adminDeletePlan removes a custom tariff preset together with its price grid.
func (s *Service) adminDeletePlan(ctx context.Context, code string) error {
	code = strings.ToLower(strings.TrimSpace(code))
	if code == "" || code == store.PlanStandard {
		return ErrBadInput
	}
	deleted, err := s.store.DeletePlan(ctx, code)
	if err != nil {
		return fmt.Errorf("delete plan: %w", err)
	}
	if !deleted {
		return ErrPlanUnknown
	}
	return nil
}

// adminSetRequisites replaces the payment requisites text, persisting it and
// refreshing the in-memory cache (same as the chat /setrequisites flow).
func (s *Service) adminSetRequisites(ctx context.Context, text string) error {
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

// adminSetReferral enables/disables the referral bonus and sets the inviter and
// invitee day amounts. Enabling with both at zero returns ErrBadInput.
func (s *Service) adminSetReferral(ctx context.Context, enabled bool, inviterDays, inviteeDays int) error {
	if err := s.setReferralDays(ctx, inviterDays, inviteeDays); err != nil {
		return err
	}
	return s.setReferralEnabled(ctx, enabled)
}

// adminListSquads returns the panel's internal squads for the mini app
// default-squad picker, marking the currently selected one.
func (s *Service) adminListSquads(ctx context.Context) ([]WebSquad, error) {
	if s.squads == nil {
		return nil, ErrPanelUnavailable
	}
	squads, err := s.squads.GetInternalSquads(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPanelUnavailable, err)
	}
	selectedUUID, _ := s.defaultSquadSelection(ctx)
	trialUUID := s.trialConfig().SquadUUID
	out := make([]WebSquad, 0, len(squads))
	for _, sq := range squads {
		out = append(out, WebSquad{UUID: sq.UUID, Name: sq.Name, Selected: sq.UUID == selectedUUID, TrialSelected: sq.UUID == trialUUID})
	}
	return out, nil
}

// WebUserRow is one lightweight row in the mini app "Manage users" table. Full
// detail (limits, squads) is fetched per-user via the "user.find" op when a row
// is opened.
type WebUserRow struct {
	RemnawaveID int64  `json:"remnawave_id"`
	Username    string `json:"username"`
	Status      string `json:"status"`
	ExpireAt    string `json:"expire_at"` // DD.MM.YYYY
}

// adminListUsers returns panel users for the mini app "Manage users" page. An
// empty cohort (or "total") returns every user; a named cohort filters with the
// same rules as adminStatsData (via classifyUser) so each returned list matches
// its tile count exactly; an unknown cohort is rejected.
func (s *Service) adminListUsers(ctx context.Context, cohort string) ([]WebUserRow, error) {
	switch cohort {
	case "", "total", "active", "expiring_soon", "expired", "linked", "low_traffic":
	default:
		return nil, ErrBadInput
	}
	subs, err := s.finder.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: list users: %v", ErrPanelUnavailable, err)
	}
	now := s.now()
	out := make([]WebUserRow, 0, len(subs))
	for i := range subs {
		c := classifyUser(&subs[i], now)
		keep := false
		switch cohort {
		case "", "total":
			keep = true
		case "active":
			keep = c.active
		case "expiring_soon":
			keep = c.expiringSoon
		case "expired":
			keep = c.expired
		case "linked":
			keep = c.linked
		case "low_traffic":
			keep = c.active && lowTrafficUser(&subs[i])
		}
		if keep {
			out = append(out, toWebUserRow(&subs[i]))
		}
	}
	return out, nil
}

// toWebUserRow projects a panel user onto the lightweight list row.
func toWebUserRow(sub *Subscriber) WebUserRow {
	return WebUserRow{
		RemnawaveID: sub.RemnawaveID,
		Username:    sub.Username,
		Status:      sub.Status,
		ExpireAt:    sub.ExpireAt.Format("02.01.2006"),
	}
}

// WebManagedUser is the editable user card shown in the mini app "Manage user"
// flow.
type WebManagedUser struct {
	RemnawaveID    int64      `json:"remnawave_id"`
	Username       string     `json:"username"`
	Status         string     `json:"status"`
	ExpireAt       string     `json:"expire_at"` // DD.MM.YYYY
	HwidLimit      int        `json:"hwid_limit"`
	TrafficLimitGB int64      `json:"traffic_limit_gb"`
	ResetStrategy  string     `json:"reset_strategy"`
	Squads         []WebSquad `json:"squads"` // panel squads, Selected = user is in it
}

// toWebManagedUser builds the card payload, marking the squads the user belongs
// to among all panel squads.
func (s *Service) toWebManagedUser(ctx context.Context, sub *Subscriber) (*WebManagedUser, error) {
	out := &WebManagedUser{
		RemnawaveID:    sub.RemnawaveID,
		Username:       sub.Username,
		Status:         sub.Status,
		ExpireAt:       sub.ExpireAt.Format("02.01.2006"),
		TrafficLimitGB: sub.TrafficLimitBytes / bytesPerGB,
		ResetStrategy:  sub.TrafficLimitStrategy,
	}
	if sub.HwidDeviceLimit != nil {
		out.HwidLimit = *sub.HwidDeviceLimit
	}
	inUser := make(map[string]bool, len(sub.SquadUUIDs))
	for _, u := range sub.SquadUUIDs {
		inUser[u] = true
	}
	if s.squads != nil {
		squads, err := s.squads.GetInternalSquads(ctx)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrPanelUnavailable, err)
		}
		for _, sq := range squads {
			out.Squads = append(out.Squads, WebSquad{UUID: sq.UUID, Name: sq.Name, Selected: inUser[sq.UUID]})
		}
	}
	return out, nil
}

// adminFindUser resolves a username/link query to an editable user card.
func (s *Service) adminFindUser(ctx context.Context, query string) (*WebManagedUser, error) {
	sub, err := s.findManagedUser(ctx, query)
	if err != nil {
		return nil, err
	}
	return s.toWebManagedUser(ctx, sub)
}

// WebUserUpdate carries optional field edits from the mini app "Manage user"
// card. A nil pointer leaves the field unchanged. Username is echoed back from
// the find call so an expiry delta can be applied relative to the user's
// current expiry.
type WebUserUpdate struct {
	RemnawaveID   int64     `json:"remnawave_id"`
	Username      string    `json:"username"`
	ExpireDays    *int      `json:"expire_days"`    // +/- days relative to current expiry
	HwidLimit     *int      `json:"hwid_limit"`     // 0 = unlimited
	TrafficGB     *int64    `json:"traffic_gb"`     // 0 = unlimited
	ResetStrategy *string   `json:"reset_strategy"` // NO_RESET | DAY | WEEK | MONTH
	Status        *string   `json:"status"`         // ACTIVE | DISABLED
	SquadUUIDs    *[]string `json:"squad_uuids"`    // replaces the user's squad set
}

// adminUpdateUser applies the editable fields of an existing user from the
// "Manage user" card. Exactly the fields set in req are changed.
func (s *Service) adminUpdateUser(ctx context.Context, req WebUserUpdate) error {
	if req.RemnawaveID == 0 {
		return ErrBadInput
	}
	patch := UserPatch{}
	if req.HwidLimit != nil {
		if *req.HwidLimit < 0 {
			return ErrBadInput
		}
		patch.HwidDeviceLimit = req.HwidLimit
	}
	if req.TrafficGB != nil {
		if *req.TrafficGB < 0 {
			return ErrBadInput
		}
		bytesLimit := *req.TrafficGB * bytesPerGB
		patch.TrafficLimitBytes = &bytesLimit
		if err := s.store.MarkActiveTrafficExtensionsRestored(ctx, req.RemnawaveID, s.now()); err != nil {
			s.logger.Error("mark traffic extension superseded failed", "user_id", req.RemnawaveID, "err", err.Error())
		}
	}
	if req.ResetStrategy != nil {
		if !validTrafficResetStrategy(*req.ResetStrategy) {
			return ErrBadInput
		}
		patch.TrafficLimitStrategy = req.ResetStrategy
	}
	if req.Status != nil {
		if *req.Status != "ACTIVE" && *req.Status != "DISABLED" {
			return ErrBadInput
		}
		patch.Status = req.Status
	}
	if req.SquadUUIDs != nil {
		patch.ActiveInternalSquads = req.SquadUUIDs
	}

	// Expiry is a delta against the user's current expiry, so re-read the user.
	if req.ExpireDays != nil {
		if req.Username == "" {
			return ErrBadInput
		}
		sub, err := s.findManagedUser(ctx, req.Username)
		if err != nil {
			return err
		}
		if _, err := s.applyUserExpiryDelta(ctx, req.RemnawaveID, sub.ExpireAt, *req.ExpireDays); err != nil {
			return err
		}
	}

	// Apply the remaining single-shot fields only when at least one is set.
	if patch.HwidDeviceLimit == nil && patch.TrafficLimitBytes == nil &&
		patch.TrafficLimitStrategy == nil && patch.Status == nil && patch.ActiveInternalSquads == nil {
		return nil
	}
	return s.updateUser(ctx, req.RemnawaveID, patch, "webadmin")
}

// WebBroadcastResult tells the mini app the broadcast was accepted. Counts do
// not come back on the response: the run takes minutes, so it is reported to
// the admin's chat when it finishes, exactly as the bot flow does.
type WebBroadcastResult struct {
	Ok      bool `json:"ok"`
	Started bool `json:"started"`
}

// adminBroadcast starts a broadcast to every panel user with a linked Telegram
// ID and returns immediately; progress is reported to the actor's chat.
func (s *Service) adminBroadcast(ctx context.Context, actorID int64, text string) (*WebBroadcastResult, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, ErrBadInput
	}
	err := s.startBroadcast(ctx, text, func(ctx context.Context, sent, failed int, err error) {
		if err != nil {
			s.logger.Error("broadcast: list users failed", "err", err.Error())
			_ = s.bot.SendPlain(ctx, actorID, i18n.T("Ошибка получения списка пользователей, рассылка не выполнена."))
			return
		}
		_ = s.bot.SendPlain(ctx, actorID,
			fmt.Sprintf(i18n.T("Рассылка завершена: отправлено %d, ошибок %d."), sent, failed))
	})
	if err != nil {
		return nil, err
	}
	return &WebBroadcastResult{Ok: true, Started: true}, nil
}

// adminRevokeGiftCode revokes an issued gift code and notifies the buyer,
// mirroring the chat revoke flow.
func (s *Service) adminRevokeGiftCode(ctx context.Context, giftID int64) error {
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

// adminConfirmRequest confirms a pending payment request: extends the
// subscription and notifies the paying user in chat (a webapp confirmation
// otherwise leaves no visible trace for them).
func (s *Service) adminConfirmRequest(ctx context.Context, actorID, reqID int64) error {
	req, newExpireAt, err := s.confirmPaymentRequest(ctx, reqID)
	if errors.Is(err, ErrConfirmedNotMarked) {
		// The extension itself succeeded; warn the admin in chat, because the
		// webapp can only show a generic error for this state.
		_ = s.bot.SendPlain(ctx, actorID, fmt.Sprintf(
			i18n.T("⚠️ Подписка для %s продлена до %s, но заявку №%d не удалось отметить подтверждённой в базе. Не подтверждайте её повторно — это продлит подписку ещё раз."),
			req.Username, newExpireAt.Format("02.01.2006"), reqID))
	}
	if err != nil {
		return err
	}
	if req.TelegramID != 0 {
		if req.Kind == store.PaymentKindTrafficExtension {
			_ = s.bot.SendPlain(ctx, req.TelegramID, fmt.Sprintf(
				i18n.T("✅ Для «%s» добавлено %d ГБ трафика до %s."),
				req.Username, req.TrafficGB, newExpireAt.Format("02.01.2006")))
		} else {
			_ = s.bot.SendPlain(ctx, req.TelegramID, fmt.Sprintf(
				i18n.T("✅ Подписка «%s» продлена на %d мес. до %s."),
				req.Username, req.Months, newExpireAt.Format("02.01.2006")))
		}
	}
	return nil
}

// adminRejectRequest rejects a pending payment request; the requesting user is
// notified by the shared helper.
func (s *Service) adminRejectRequest(ctx context.Context, reqID int64) error {
	_, err := s.rejectPaymentRequest(ctx, reqID)
	return err
}

// adminDeletePaymentRequest permanently removes a payment record from local
// history (any status) so admins can purge test data that distorts the revenue
// and conversion reporting. It only touches the local ledger — a confirmed
// subscription extension already applied on the panel is not reversed — and
// notifies nobody.
func (s *Service) adminDeletePaymentRequest(ctx context.Context, reqID int64) error {
	ok, err := s.store.DeletePaymentRequest(ctx, reqID)
	if err != nil {
		return fmt.Errorf("delete payment request: %w", err)
	}
	if !ok {
		return ErrRequestNotFound
	}
	return nil
}

// adminDeleteGiftCode permanently removes a gift code from local history (any
// status) so admins can purge test/junk records that distort reporting. It only
// touches the local ledger and notifies nobody; deleting an unredeemed code
// simply makes that code unusable.
func (s *Service) adminDeleteGiftCode(ctx context.Context, giftID int64) error {
	ok, err := s.store.DeleteGiftCode(ctx, giftID)
	if err != nil {
		return fmt.Errorf("delete gift code: %w", err)
	}
	if !ok {
		return ErrRequestNotFound
	}
	return nil
}

// adminDeleteInviteRequest permanently removes an invite request from local
// history (any status) so admins can purge test/junk records that distort
// reporting. It only touches the local ledger and notifies nobody.
func (s *Service) adminDeleteInviteRequest(ctx context.Context, reqID int64) error {
	ok, err := s.store.DeleteInviteRequest(ctx, reqID)
	if err != nil {
		return fmt.Errorf("delete invite request: %w", err)
	}
	if !ok {
		return ErrRequestNotFound
	}
	return nil
}

// adminConfirmGiftRequest confirms payment of a pending gift purchase; the
// buyer gets the redemption link from the shared helper.
func (s *Service) adminConfirmGiftRequest(ctx context.Context, giftID int64) error {
	_, err := s.issueGiftRequest(ctx, giftID)
	return err
}

// adminRejectGiftRequest rejects a pending gift purchase; the buyer is notified
// by the shared helper.
func (s *Service) adminRejectGiftRequest(ctx context.Context, giftID int64) error {
	_, err := s.rejectGiftRequest(ctx, giftID)
	return err
}

// adminApproveInviteRequest approves a pending invite request: creates the user
// in the panel and notifies the inviter via the shared helper.
func (s *Service) adminApproveInviteRequest(ctx context.Context, reqID int64) error {
	_, _, _, err := s.approveInviteRequest(ctx, reqID)
	return err
}

// adminRejectInviteRequest rejects a pending invite request; the inviter is
// notified by the shared helper.
func (s *Service) adminRejectInviteRequest(ctx context.Context, reqID int64) error {
	_, err := s.rejectInviteRequest(ctx, reqID)
	return err
}

// adminApproveTrialRequest approves a pending trial request: creates the trial
// profile and notifies the user via the shared helper.
func (s *Service) adminApproveTrialRequest(ctx context.Context, reqID int64) error {
	_, _, _, err := s.approveTrialRequest(ctx, reqID)
	return err
}

// adminRejectTrialRequest rejects a pending trial request; the user is notified
// by the shared helper.
func (s *Service) adminRejectTrialRequest(ctx context.Context, reqID int64) error {
	_, err := s.rejectTrialRequest(ctx, reqID)
	return err
}
