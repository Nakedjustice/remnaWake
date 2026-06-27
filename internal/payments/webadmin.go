package payments

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Nakedjustice/remnaWake/internal/i18n"
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

// WebAdminPanel is the full /api/admin payload for the mini app.
type WebAdminPanel struct {
	Tariffs        []WebTariff             `json:"tariffs"`
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

func (s *Service) AdminCheckUpdates(ctx context.Context, telegramID int64) (*WebUpdateCheckResult, error) {
	if err := s.adminGuard(telegramID); err != nil {
		return nil, err
	}
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

func (s *Service) AdminSetUpdateInterval(ctx context.Context, telegramID int64, raw string) error {
	if err := s.adminGuard(telegramID); err != nil {
		return err
	}
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

// AdminSetRequireScreenshot turns the payment-screenshot requirement on or off
// from the mini app admin panel (same setting as the adm:shot_toggle button).
func (s *Service) AdminSetRequireScreenshot(ctx context.Context, telegramID int64, on bool) error {
	if err := s.adminGuard(telegramID); err != nil {
		return err
	}
	if err := s.setRequireScreenshot(ctx, on); err != nil {
		return fmt.Errorf("save screenshot setting: %w", err)
	}
	return nil
}

// AdminSetProviderEnabled enables or disables one payment provider from the mini
// app admin panel (same setting as the adm:provtog:<name> button). Enabling an
// unconfigured provider, or disabling the last enabled one, returns ErrBadInput.
func (s *Service) AdminSetProviderEnabled(ctx context.Context, telegramID int64, provider string, on bool) error {
	if err := s.adminGuard(telegramID); err != nil {
		return err
	}
	return s.setProviderEnabled(ctx, provider, on)
}

// AdminSetTrial enables/disables the free trial and sets its length (days ≥ 1)
// from the mini app admin panel (same settings as the adm:trial bot card).
func (s *Service) AdminSetTrial(ctx context.Context, telegramID int64, cfg TrialConfig) error {
	if err := s.adminGuard(telegramID); err != nil {
		return err
	}
	return s.setTrialConfig(ctx, cfg)
}

// AdminSetReferral enables/disables the referral bonus and sets the inviter and
// invitee day amounts. Enabling with both at zero returns ErrBadInput.
func (s *Service) AdminSetReferral(ctx context.Context, telegramID int64, enabled bool, inviterDays, inviteeDays int) error {
	if err := s.adminGuard(telegramID); err != nil {
		return err
	}
	if err := s.setReferralDays(ctx, inviterDays, inviteeDays); err != nil {
		return err
	}
	return s.setReferralEnabled(ctx, enabled)
}

// AdminListSquads returns the panel's internal squads for the mini app
// default-squad picker, marking the currently selected one.
func (s *Service) AdminListSquads(ctx context.Context, telegramID int64) ([]WebSquad, error) {
	if err := s.adminGuard(telegramID); err != nil {
		return nil, err
	}
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

// AdminSetDefaultSquad persists the internal squad newly created users are
// assigned to, selected from the mini app admin panel.
func (s *Service) AdminSetDefaultSquad(ctx context.Context, telegramID int64, uuid string) error {
	if err := s.adminGuard(telegramID); err != nil {
		return err
	}
	_, err := s.setDefaultSquad(ctx, uuid)
	return err
}

// WebUserRow is one lightweight row in the mini app "Manage users" table. Full
// detail (limits, squads) is fetched per-user via AdminFindUser when a row is
// opened.
type WebUserRow struct {
	UUID     string `json:"uuid"`
	Username string `json:"username"`
	Status   string `json:"status"`
	ExpireAt string `json:"expire_at"` // DD.MM.YYYY
}

// AdminListUsers returns every panel user for the mini app "Manage users" page,
// where the admin filters by username client-side.
func (s *Service) AdminListUsers(ctx context.Context, telegramID int64) ([]WebUserRow, error) {
	if err := s.adminGuard(telegramID); err != nil {
		return nil, err
	}
	subs, err := s.finder.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: list users: %v", ErrPanelUnavailable, err)
	}
	out := make([]WebUserRow, 0, len(subs))
	for i := range subs {
		out = append(out, WebUserRow{
			UUID:     subs[i].UUID,
			Username: subs[i].Username,
			Status:   subs[i].Status,
			ExpireAt: subs[i].ExpireAt.Format("02.01.2006"),
		})
	}
	return out, nil
}

// AdminSetDefaultTrafficReset persists the traffic-reset strategy applied to
// newly created users, selected from the mini app admin panel.
func (s *Service) AdminSetDefaultTrafficReset(ctx context.Context, telegramID int64, strategy string) error {
	if err := s.adminGuard(telegramID); err != nil {
		return err
	}
	return s.setDefaultTrafficReset(ctx, strategy)
}

// WebManagedUser is the editable user card shown in the mini app "Manage user"
// flow.
type WebManagedUser struct {
	UUID           string     `json:"uuid"`
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
		UUID:           sub.UUID,
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

// AdminFindUser resolves a username/link query to an editable user card.
func (s *Service) AdminFindUser(ctx context.Context, telegramID int64, query string) (*WebManagedUser, error) {
	if err := s.adminGuard(telegramID); err != nil {
		return nil, err
	}
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
	UUID          string    `json:"uuid"`
	Username      string    `json:"username"`
	ExpireDays    *int      `json:"expire_days"`    // +/- days relative to current expiry
	HwidLimit     *int      `json:"hwid_limit"`     // 0 = unlimited
	TrafficGB     *int64    `json:"traffic_gb"`     // 0 = unlimited
	ResetStrategy *string   `json:"reset_strategy"` // NO_RESET | DAY | WEEK | MONTH
	Status        *string   `json:"status"`         // ACTIVE | DISABLED
	SquadUUIDs    *[]string `json:"squad_uuids"`    // replaces the user's squad set
}

// AdminUpdateUser applies the editable fields of an existing user from the mini
// app "Manage user" card. Exactly the fields set in req are changed.
func (s *Service) AdminUpdateUser(ctx context.Context, telegramID int64, req WebUserUpdate) error {
	if err := s.adminGuard(telegramID); err != nil {
		return err
	}
	if req.UUID == "" {
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
		if _, err := s.applyUserExpiryDelta(ctx, req.UUID, sub.ExpireAt, *req.ExpireDays); err != nil {
			return err
		}
	}

	// Apply the remaining single-shot fields only when at least one is set.
	if patch.HwidDeviceLimit == nil && patch.TrafficLimitBytes == nil &&
		patch.TrafficLimitStrategy == nil && patch.Status == nil && patch.ActiveInternalSquads == nil {
		return nil
	}
	return s.updateUser(ctx, req.UUID, patch, "webadmin")
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
	_, err := s.rejectPaymentRequest(ctx, reqID)
	return err
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

// AdminApproveTrialRequest approves a pending trial request from the mini app
// admin panel: creates the trial profile and notifies the user via the shared
// helper.
func (s *Service) AdminApproveTrialRequest(ctx context.Context, telegramID, reqID int64) error {
	if err := s.adminGuard(telegramID); err != nil {
		return err
	}
	_, _, _, err := s.approveTrialRequest(ctx, reqID)
	return err
}

// AdminRejectTrialRequest rejects a pending trial request from the mini app
// admin panel; the user is notified by the shared helper.
func (s *Service) AdminRejectTrialRequest(ctx context.Context, telegramID, reqID int64) error {
	if err := s.adminGuard(telegramID); err != nil {
		return err
	}
	_, err := s.rejectTrialRequest(ctx, reqID)
	return err
}
