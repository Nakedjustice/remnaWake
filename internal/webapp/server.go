package webapp

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/payments"
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

//go:embed static
var staticFS embed.FS

// initDataMaxAge is how long a Mini App initData signature stays acceptable.
const initDataMaxAge = 24 * time.Hour

// Cabinet is the subset of *payments.Service the mini app user API needs.
type Cabinet interface {
	CheckTelegramRegistration(ctx context.Context, user *tg.User) bool
	CabinetData(ctx context.Context, telegramID int64) (*payments.WebCabinet, error)
	CreateRenewRequest(ctx context.Context, telegramID, remnawaveID int64, months int, provider, plan string, planChangeConfirmed bool) (*payments.RenewResult, error)
	CreateTrafficExtensionRequest(ctx context.Context, telegramID, remnawaveID int64, trafficGB int, provider string) (*payments.RenewResult, error)
	CreateGiftRequest(ctx context.Context, telegramID int64, months int) error
	CreateInviteRequest(ctx context.Context, telegramID int64, username string) error
	RegisterProfile(ctx context.Context, telegramID int64, query string) (*payments.WebRegistrationResult, error)
	RedeemGift(ctx context.Context, telegramID int64, code string, remnawaveID int64, username string) (*payments.WebGiftRedemptionResult, error)
	UploadRenewReceipt(ctx context.Context, telegramID int64, receipt payments.WebReceipt) error
	CheckPlategaPayment(ctx context.Context, telegramID, requestID int64) (*payments.WebPaymentStatus, error)
	SetNotificationPref(ctx context.Context, telegramID int64, kind string, muted bool) error
	ClaimTrial(ctx context.Context, telegramID int64, username string) (*payments.WebTrialResult, error)
	SupportHistoryUser(ctx context.Context, telegramID int64) (*payments.WebSupport, error)
	SupportSendUser(ctx context.Context, telegramID int64, text string) error
	SupportCloseUser(ctx context.Context, telegramID int64) error
	SupportTicketsUser(ctx context.Context, telegramID int64) (*payments.WebSupportTickets, error)
	SupportTicketThreadUser(ctx context.Context, telegramID, ticketID int64) (*payments.WebSupportThread, error)
	SupportCreateTicket(ctx context.Context, telegramID int64, subject, text string) (int64, error)
	SupportSendUserToTicket(ctx context.Context, telegramID, ticketID int64, text string) error
	SupportCloseTicketUser(ctx context.Context, telegramID, ticketID int64) error
	DeviceOverview(ctx context.Context, telegramID int64) (*payments.WebDevices, error)
	RevokeDevice(ctx context.Context, telegramID, remnawaveID int64, hwid string) (*payments.WebDevices, error)
}

// Admin is the single door every admin HTTP route goes through; the op
// registry in the payments package owns authorization, validation and effects.
type Admin interface {
	RunAdminOp(ctx context.Context, actorID int64, name string, in any) (any, error)
}

// Webhooks is the subset of *payments.Service the public (non-initData)
// payment-gateway callbacks need. Optional: nil disables the webhook routes.
type Webhooks interface {
	HandlePlategaWebhook(ctx context.Context, body []byte) error
}

// Server hosts the Telegram Mini App: embedded static frontend plus the JSON
// API authenticated via Telegram initData.
type Server struct {
	cabinet  Cabinet
	admin    Admin
	webhooks Webhooks
	botToken string
	logger   *slog.Logger
	now      func() time.Time
}

func NewServer(cabinet Cabinet, admin Admin, webhooks Webhooks, botToken string, logger *slog.Logger) *Server {
	return &Server{cabinet: cabinet, admin: admin, webhooks: webhooks, botToken: botToken, logger: logger, now: time.Now}
}

// Handler returns the mini app HTTP handler (static files + /api routes).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", staticHandler())
	// Public payment-gateway callback (no initData auth): Platega verifies the
	// transaction itself via its API, so the body is trusted only for its id.
	mux.HandleFunc("POST /platega/callback", s.handlePlategaCallback)
	mux.HandleFunc("GET /api/me", s.withUser(s.handleMe))
	mux.HandleFunc("POST /api/renew", s.withUser(s.handleRenew))
	mux.HandleFunc("POST /api/traffic-extension", s.withUser(s.handleTrafficExtension))
	mux.HandleFunc("POST /api/gift", s.withUser(s.handleGift))
	mux.HandleFunc("POST /api/invite", s.withUser(s.handleInvite))
	mux.HandleFunc("POST /api/register", s.withUser(s.handleRegister))
	mux.HandleFunc("POST /api/gift/redeem", s.withUser(s.handleGiftRedeem))
	mux.HandleFunc("POST /api/receipt", s.withUser(s.handleReceipt))
	mux.HandleFunc("POST /api/platega/check", s.withUser(s.handlePlategaCheck))
	mux.HandleFunc("POST /api/notifications", s.withUser(s.handleNotifications))
	mux.HandleFunc("POST /api/trial", s.withUser(s.handleTrial))
	mux.HandleFunc("GET /api/devices", s.withUser(s.handleDevices))
	mux.HandleFunc("POST /api/devices/revoke", s.withUser(s.handleDeviceRevoke))
	mux.HandleFunc("GET /api/support", s.withSupportUser(s.handleSupport))
	mux.HandleFunc("POST /api/support/send", s.withSupportUser(s.handleSupportSend))
	mux.HandleFunc("POST /api/support/close", s.withSupportUser(s.handleSupportClose))
	mux.HandleFunc("GET /api/support/tickets", s.withSupportUser(s.handleSupportTickets))
	mux.HandleFunc("POST /api/support/ticket", s.withSupportUser(s.handleSupportTicketThread))
	mux.HandleFunc("POST /api/support/ticket/new", s.withSupportUser(s.handleSupportTicketCreate))
	mux.HandleFunc("POST /api/support/ticket/send", s.withSupportUser(s.handleSupportTicketSend))
	mux.HandleFunc("POST /api/support/ticket/close", s.withSupportUser(s.handleSupportTicketClose))
	mux.HandleFunc("GET /api/admin", s.adminOp("panel.get", nil, ""))
	mux.HandleFunc("POST /api/admin/backup", s.adminOp("backup.send", nil, ""))
	mux.HandleFunc("GET /api/admin/action-center", s.adminOp("action_center.get", nil, ""))
	mux.HandleFunc("GET /api/admin/stats", s.adminOp("stats.get", nil, ""))
	mux.HandleFunc("GET /api/admin/analytics", s.handleAdminAnalytics) // query-param input
	mux.HandleFunc("GET /api/admin/proxy-health", s.adminOp("proxy_health.get", nil, ""))
	mux.HandleFunc("POST /api/admin/proxy-notification", s.adminOp("proxy_notification.set", func() any { return &payments.AdminProxyNotifInput{} }, ""))
	mux.HandleFunc("GET /api/admin/payments", s.handleAdminPayments) // query-param input
	mux.HandleFunc("POST /api/admin/payment/delete", s.adminIDOp("request.delete"))
	mux.HandleFunc("POST /api/admin/gift/delete", s.adminIDOp("gift.delete"))
	mux.HandleFunc("POST /api/admin/invite/delete", s.adminIDOp("invite.delete"))
	mux.HandleFunc("POST /api/admin/backup/schedule", s.adminOp("backup.schedule.set", func() any { return &payments.AdminBackupScheduleInput{} }, ""))
	mux.HandleFunc("POST /api/admin/updates/check", s.adminOp("updates.check", nil, ""))
	mux.HandleFunc("POST /api/admin/updates/interval", s.adminOp("updates.interval.set", func() any { return &payments.AdminIntervalInput{} }, ""))
	mux.HandleFunc("GET /api/admin/support", s.adminOp("support.conversations", nil, "conversations"))
	mux.HandleFunc("POST /api/admin/support/thread", s.adminOp("support.thread", func() any { return &payments.AdminSupportUserInput{} }, ""))
	mux.HandleFunc("POST /api/admin/support/send", s.adminOp("support.send", func() any { return &payments.AdminSupportSendInput{} }, ""))
	mux.HandleFunc("POST /api/admin/support/close", s.adminIDOp("support.close"))
	mux.HandleFunc("GET /api/admin/support/tickets", s.adminOp("support.tickets", nil, "tickets"))
	mux.HandleFunc("POST /api/admin/support/ticket", s.adminOp("support.ticket.thread", func() any { return &payments.AdminTicketInput{} }, ""))
	mux.HandleFunc("POST /api/admin/support/ticket/reply", s.adminOp("support.ticket.reply", func() any { return &payments.AdminTicketReplyInput{} }, ""))
	mux.HandleFunc("POST /api/admin/support/ticket/close", s.adminOp("support.ticket.close", func() any { return &payments.AdminTicketInput{} }, ""))
	mux.HandleFunc("POST /api/admin/tariff", s.adminOp("tariff.set", func() any { return &payments.AdminTariffInput{} }, ""))
	mux.HandleFunc("POST /api/admin/tariff/delete", s.adminOp("tariff.delete", func() any { return &payments.AdminTariffDeleteInput{} }, ""))
	mux.HandleFunc("POST /api/admin/traffic-extension", s.adminOp("traffic_ext.set", func() any { return &payments.AdminTrafficExtInput{} }, ""))
	mux.HandleFunc("POST /api/admin/traffic-extension/delete", s.adminOp("traffic_ext.delete", func() any { return &payments.AdminTrafficExtDeleteInput{} }, ""))
	mux.HandleFunc("POST /api/admin/plan", s.adminOp("plan.set", func() any { return &payments.WebAdminPlan{} }, ""))
	mux.HandleFunc("POST /api/admin/plan/delete", s.adminOp("plan.delete", func() any { return &payments.AdminPlanDeleteInput{} }, ""))
	mux.HandleFunc("POST /api/admin/requisites", s.adminOp("requisites.set", func() any { return &payments.AdminTextInput{} }, ""))
	mux.HandleFunc("POST /api/admin/screenshot-toggle", s.adminOp("screenshot.set", func() any { return &payments.AdminToggleInput{} }, ""))
	mux.HandleFunc("POST /api/admin/payment-provider", s.adminOp("provider.set", func() any { return &payments.AdminProviderInput{} }, ""))
	mux.HandleFunc("GET /api/admin/squads", s.adminOp("squads.list", nil, "squads"))
	mux.HandleFunc("POST /api/admin/squad", s.adminOp("squad.default.set", func() any { return &payments.AdminUUIDInput{} }, ""))
	mux.HandleFunc("GET /api/admin/users", s.handleAdminListUsers) // query-param input
	mux.HandleFunc("POST /api/admin/traffic-reset", s.adminOp("traffic_reset.set", func() any { return &payments.AdminStrategyInput{} }, ""))
	mux.HandleFunc("POST /api/admin/trial", s.adminOp("trial.set", func() any { return &payments.AdminTrialInput{} }, ""))
	mux.HandleFunc("POST /api/admin/referral", s.adminOp("referral.set", func() any { return &payments.AdminReferralInput{} }, ""))
	mux.HandleFunc("POST /api/admin/registration-guard/pattern", s.adminOp("guard.pattern.add", func() any { return &payments.AdminPatternInput{} }, ""))
	mux.HandleFunc("POST /api/admin/registration-guard/pattern/delete", s.adminOp("guard.pattern.delete", func() any { return &payments.AdminPatternInput{} }, ""))
	mux.HandleFunc("POST /api/admin/registration-guard/unblock", s.adminOp("guard.unblock", func() any { return &payments.AdminUnblockInput{} }, ""))
	mux.HandleFunc("POST /api/admin/user/find", s.adminOp("user.find", func() any { return &payments.AdminFindUserInput{} }, "user"))
	mux.HandleFunc("POST /api/admin/user/update", s.adminOp("user.update", func() any { return &payments.WebUserUpdate{} }, ""))
	mux.HandleFunc("POST /api/admin/broadcast", s.adminOp("broadcast.start", func() any { return &payments.AdminBroadcastInput{} }, ""))
	mux.HandleFunc("GET /api/admin/servers", s.adminOp("servers.list", nil, ""))
	mux.HandleFunc("POST /api/admin/server", s.adminOp("server.save", func() any { return &payments.WebInfraServerInput{} }, ""))
	mux.HandleFunc("POST /api/admin/server/delete", s.adminIDOp("server.delete"))
	mux.HandleFunc("POST /api/admin/server/paid", s.adminIDOp("server.paid"))
	mux.HandleFunc("GET /api/admin/fx", s.adminOp("fx.list", nil, ""))
	mux.HandleFunc("POST /api/admin/fx/manual", s.adminOp("fx.manual.set", func() any { return &payments.AdminFxRateInput{} }, ""))
	mux.HandleFunc("POST /api/admin/fx/base", s.adminOp("fx.base.set", func() any { return &payments.AdminCurrencyInput{} }, ""))
	mux.HandleFunc("POST /api/admin/fx/refresh", s.adminOp("fx.refresh", nil, ""))
	mux.HandleFunc("POST /api/admin/gift/revoke", s.adminIDOp("gift.revoke"))
	mux.HandleFunc("POST /api/admin/request/confirm", s.adminIDOp("request.confirm"))
	mux.HandleFunc("POST /api/admin/request/reject", s.adminIDOp("request.reject"))
	mux.HandleFunc("POST /api/admin/gift-request/confirm", s.adminIDOp("gift_request.confirm"))
	mux.HandleFunc("POST /api/admin/gift-request/reject", s.adminIDOp("gift_request.reject"))
	mux.HandleFunc("POST /api/admin/invite-request/confirm", s.adminIDOp("invite_request.approve"))
	mux.HandleFunc("POST /api/admin/invite-request/reject", s.adminIDOp("invite_request.reject"))
	mux.HandleFunc("POST /api/admin/trial-request/confirm", s.adminIDOp("trial_request.approve"))
	mux.HandleFunc("POST /api/admin/trial-request/reject", s.adminIDOp("trial_request.reject"))
	return gzipMiddleware(mux)
}

// Run serves the mini app on addr until ctx is cancelled.
func (s *Server) Run(ctx context.Context, addr string) error {
	srv := s.newHTTPServer(addr)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	s.logger.Info("mini app server started", "addr", addr)
	err := srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// newHTTPServer builds the mini app's HTTP server.
//
// ReadTimeout is generous because /api/receipt carries a photo from a phone on
// mobile data. IdleTimeout bounds keep-alive connections that a closed mini app
// leaves behind. There is deliberately no WriteTimeout: the admin backup
// download streams the whole database to Telegram inside its request, and no
// fixed cap fits both that and an ordinary JSON reply.
func (s *Server) newHTTPServer(addr string) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
}

// authenticateUser extracts and validates the Telegram identity from initData
// without applying access policy. Support uses this path so an automatically
// blocked user can still contact an administrator.
func (s *Server) authenticateUser(w http.ResponseWriter, r *http.Request) (*tg.User, bool) {
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "tma ")
	if !ok || raw == "" {
		writeJSONError(w, http.StatusUnauthorized, "missing initData")
		return nil, false
	}
	user, err := validateInitData(raw, s.botToken, initDataMaxAge, s.now())
	if err != nil {
		s.logger.Warn("webapp: initData validation failed", "err", err.Error())
		writeJSONError(w, http.StatusUnauthorized, "invalid initData")
		return nil, false
	}
	return user, true
}

// authenticate applies the shared registration guard to every ordinary Mini
// App route. A blocked identity gets a machine-readable 403 used by the
// frontend to render its dedicated support escape hatch.
func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) (int64, bool) {
	user, ok := s.authenticateUser(w, r)
	if !ok {
		return 0, false
	}
	if s.cabinet.CheckTelegramRegistration(r.Context(), user) {
		writeJSONErrorCode(w, http.StatusForbidden, "registration blocked", "registration_blocked")
		return 0, false
	}
	return user.ID, true
}

// authenticateSupport records a first Mini App visit through the same guard,
// but deliberately permits the three support endpoints even when it blocks the
// identity.
func (s *Server) authenticateSupport(w http.ResponseWriter, r *http.Request) (int64, bool) {
	user, ok := s.authenticateUser(w, r)
	if !ok {
		return 0, false
	}
	_ = s.cabinet.CheckTelegramRegistration(r.Context(), user)
	return user.ID, true
}

// withUser authenticates a Mini App identity and applies the registration
// guard before invoking fn, so handlers receive the caller's Telegram ID
// without repeating the prologue.
func (s *Server) withUser(fn func(w http.ResponseWriter, r *http.Request, userID int64)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.authenticate(w, r)
		if !ok {
			return
		}
		fn(w, r, userID)
	}
}

// withSupportUser is the support-route variant of withUser that keeps the
// blocked-user exemption: the guard still runs but never rejects.
func (s *Server) withSupportUser(fn func(w http.ResponseWriter, r *http.Request, userID int64)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.authenticateSupport(w, r)
		if !ok {
			return
		}
		fn(w, r, userID)
	}
}

// decodeBody JSON-decodes a size-capped request body, answering 400 itself on
// garbage so handlers keep their happy path.
func decodeBody(w http.ResponseWriter, r *http.Request, limit int64, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit)).Decode(v); err != nil {
		writeJSONError(w, http.StatusBadRequest, "malformed request body")
		return false
	}
	return true
}

// handlePlategaCallback receives Platega's webhook. The body carries only a
// transaction id; the service re-fetches the real status from Platega's API.
// It always answers 200 (unless the body is unreadable) so Platega does not
// retry already-handled or not-yet-confirmed events.
func (s *Server) handlePlategaCallback(w http.ResponseWriter, r *http.Request) {
	if s.webhooks == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "platega not configured")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 256*1024))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "unreadable body")
		return
	}
	if err := s.webhooks.HandlePlategaWebhook(r.Context(), body); err != nil {
		// Log and still 200: a 5xx would make Platega retry, but our errors here
		// are transient verification failures the next callback (or the user's
		// "check payment" button) will resolve.
		s.logger.Error("webapp: platega webhook failed", "err", err.Error())
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, userID int64) {
	data, err := s.cabinet.CabinetData(r.Context(), userID)
	if err != nil {
		s.logger.Error("webapp: cabinet data failed", "err", err.Error(), "telegram_id", userID)
		writeJSONError(w, http.StatusBadGateway, "panel request failed, try again later")
		return
	}
	writeJSON(w, http.StatusOK, data)
}

func (s *Server) handleRenew(w http.ResponseWriter, r *http.Request, userID int64) {
	var req struct {
		RemnawaveID         int64  `json:"remnawave_id"`
		Months              int    `json:"months"`
		Provider            string `json:"provider"`
		Plan                string `json:"plan"` // "" = standard
		PlanChangeConfirmed bool   `json:"plan_change_confirmed"`
	}
	if !decodeBody(w, r, 4096, &req) {
		return
	}

	result, err := s.cabinet.CreateRenewRequest(r.Context(), userID, req.RemnawaveID, req.Months, req.Provider, req.Plan, req.PlanChangeConfirmed)
	if errors.Is(err, payments.ErrScreenshotRequired) {
		// Not an error for the user: the bot chat is waiting for the screenshot.
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": "awaiting_screenshot"})
		return
	}
	if err != nil {
		s.writeCabinetError(w, "renew", userID, err)
		return
	}
	resp := map[string]any{"ok": true}
	if result != nil {
		if result.Status != "" {
			resp["status"] = result.Status
		}
		if result.PayURL != "" {
			resp["payment_url"] = result.PayURL
		}
		if result.RequestID != 0 {
			resp["request_id"] = result.RequestID
		}
		if result.InvoiceURL != "" {
			resp["invoice_url"] = result.InvoiceURL
		}
		if len(result.Providers) > 0 {
			resp["providers"] = result.Providers
		}
		if result.PlanChange != nil {
			resp["plan_change"] = result.PlanChange
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleTrafficExtension(w http.ResponseWriter, r *http.Request, userID int64) {
	var req struct {
		RemnawaveID int64  `json:"remnawave_id"`
		TrafficGB   int    `json:"traffic_gb"`
		Provider    string `json:"provider"`
	}
	if !decodeBody(w, r, 4096, &req) {
		return
	}
	result, err := s.cabinet.CreateTrafficExtensionRequest(r.Context(), userID, req.RemnawaveID, req.TrafficGB, req.Provider)
	if errors.Is(err, payments.ErrScreenshotRequired) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": "awaiting_screenshot"})
		return
	}
	if err != nil {
		s.writeCabinetError(w, "traffic extension", userID, err)
		return
	}
	resp := map[string]any{"ok": true}
	if result != nil {
		if result.Status != "" {
			resp["status"] = result.Status
		}
		if result.PayURL != "" {
			resp["payment_url"] = result.PayURL
		}
		if result.RequestID != 0 {
			resp["request_id"] = result.RequestID
		}
		if result.InvoiceURL != "" {
			resp["invoice_url"] = result.InvoiceURL
		}
		if len(result.Providers) > 0 {
			resp["providers"] = result.Providers
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleDevices returns the caller's HWID slot usage, one section per linked
// profile.
func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request, userID int64) {
	data, err := s.cabinet.DeviceOverview(r.Context(), userID)
	if err != nil {
		s.writeCabinetError(w, "devices", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, data)
}

// handleDeviceRevoke frees one slot. The hwid travels in the body because it is
// long and arbitrary; the profile is named by id and re-checked against the
// caller's own profiles by the service.
func (s *Server) handleDeviceRevoke(w http.ResponseWriter, r *http.Request, userID int64) {
	var req struct {
		RemnawaveID int64  `json:"remnawave_id"`
		HWID        string `json:"hwid"`
	}
	if !decodeBody(w, r, 4096, &req) {
		return
	}
	data, err := s.cabinet.RevokeDevice(r.Context(), userID, req.RemnawaveID, req.HWID)
	if err != nil {
		s.writeCabinetError(w, "revoke device", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "devices": data})
}

// writeCabinetError maps user-facing cabinet service errors to HTTP statuses.
func (s *Server) writeCabinetError(w http.ResponseWriter, action string, telegramID int64, err error) {
	switch {
	case errors.Is(err, payments.ErrNotLinked), errors.Is(err, payments.ErrProfileUnknown):
		writeJSONError(w, http.StatusForbidden, "profile is not linked to this account")
	case errors.Is(err, payments.ErrTariffUnknown):
		writeJSONError(w, http.StatusBadRequest, "tariff not found")
	case errors.Is(err, payments.ErrTrafficExtensionUnknown):
		writeJSONError(w, http.StatusBadRequest, "traffic extension option not found")
	case errors.Is(err, payments.ErrTrafficExtensionActive):
		writeJSONError(w, http.StatusConflict, "traffic extension already active")
	case errors.Is(err, payments.ErrTrafficExtensionUnavailable):
		writeJSONError(w, http.StatusConflict, "traffic extension is unavailable for this profile")
	case errors.Is(err, payments.ErrPlanUnknown):
		writeJSONError(w, http.StatusBadRequest, "plan not found")
	case errors.Is(err, payments.ErrInvalidUsername):
		writeJSONErrorCode(w, http.StatusBadRequest, "invalid username", "invalid_username")
	case errors.Is(err, payments.ErrBadInput):
		writeJSONError(w, http.StatusBadRequest, "invalid input")
	case errors.Is(err, payments.ErrInvalidProfileQuery), errors.Is(err, payments.ErrGiftInvalid), errors.Is(err, payments.ErrReceiptType):
		writeJSONError(w, http.StatusBadRequest, "invalid input")
	case errors.Is(err, payments.ErrProfileLinkedElsewhere):
		writeJSONError(w, http.StatusForbidden, "profile is linked to another account")
	case errors.Is(err, payments.ErrPaymentRequestInaccessible):
		writeJSONError(w, http.StatusNotFound, "payment request not found")
	// A foreign ticket is reported exactly like a missing one (see userTicket).
	case errors.Is(err, payments.ErrTicketNotFound):
		writeJSONError(w, http.StatusNotFound, "support ticket not found")
	case errors.Is(err, payments.ErrTicketClosed):
		writeJSONError(w, http.StatusConflict, "support ticket is closed")
	case errors.Is(err, payments.ErrDeviceNotFound):
		writeJSONError(w, http.StatusNotFound, "device not found")
	case errors.Is(err, payments.ErrDevicesUnavailable):
		writeJSONError(w, http.StatusServiceUnavailable, "device management is unavailable")
	case errors.Is(err, payments.ErrGiftUsed):
		writeJSONError(w, http.StatusConflict, "gift code already used")
	case errors.Is(err, payments.ErrReceiptSessionExpired):
		writeJSONError(w, http.StatusGone, "receipt session expired")
	case errors.Is(err, payments.ErrReceiptTooLarge):
		writeJSONError(w, http.StatusRequestEntityTooLarge, "receipt is too large")
	case errors.Is(err, payments.ErrPaymentsDisabled), errors.Is(err, payments.ErrTrialDisabled):
		writeJSONError(w, http.StatusServiceUnavailable, "payments are disabled")
	case errors.Is(err, payments.ErrUsernameTaken):
		writeJSONError(w, http.StatusConflict, "username already taken")
	case errors.Is(err, payments.ErrTrialAlreadyUsed), errors.Is(err, payments.ErrTrialNotEligible):
		writeJSONError(w, http.StatusConflict, "trial not available")
	case errors.Is(err, payments.ErrTrialRequestPending):
		writeJSONError(w, http.StatusConflict, "trial request already pending")
	case errors.Is(err, payments.ErrPanelCreateFailed):
		s.logger.Error("webapp: "+action+" failed", "err", err.Error(), "telegram_id", telegramID)
		writeJSONError(w, http.StatusBadGateway, "panel request failed, try again later")
	case errors.Is(err, payments.ErrPanelUnavailable), errors.Is(err, payments.ErrProviderUnavailable):
		s.logger.Error("webapp: "+action+" failed", "err", err.Error(), "telegram_id", telegramID)
		writeJSONError(w, http.StatusBadGateway, "upstream service failed, try again later")
	default:
		s.logger.Error("webapp: "+action+" failed", "err", err.Error(), "telegram_id", telegramID)
		writeJSONError(w, http.StatusInternalServerError, "internal error, try again later")
	}
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request, userID int64) {
	var req struct {
		Query string `json:"query"`
	}
	if !decodeBody(w, r, 4096, &req) {
		return
	}
	result, err := s.cabinet.RegisterProfile(r.Context(), userID, req.Query)
	if err != nil {
		s.writeCabinetError(w, "register", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "username": result.Username})
}

func (s *Server) handleGiftRedeem(w http.ResponseWriter, r *http.Request, userID int64) {
	var req struct {
		Code        string `json:"code"`
		RemnawaveID int64  `json:"remnawave_id"`
		Username    string `json:"username"`
	}
	if !decodeBody(w, r, 8192, &req) {
		return
	}
	result, err := s.cabinet.RedeemGift(r.Context(), userID, req.Code, req.RemnawaveID, req.Username)
	if err != nil {
		s.writeCabinetError(w, "redeem gift", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "username": result.Username, "subscription_url": result.SubscriptionURL, "expire_at": result.ExpireAt})
}

func (s *Server) handleReceipt(w http.ResponseWriter, r *http.Request, userID int64) {
	r.Body = http.MaxBytesReader(w, r.Body, (50<<20)+(1<<20))
	if err := r.ParseMultipartForm(50 << 20); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "receipt is too large")
		} else {
			writeJSONError(w, http.StatusBadRequest, "malformed multipart body")
		}
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "receipt file is required")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "unreadable receipt")
		return
	}
	receipt := payments.WebReceipt{Filename: header.Filename, ContentType: header.Header.Get("Content-Type"), Data: data, Note: r.FormValue("note")}
	if err := s.cabinet.UploadRenewReceipt(r.Context(), userID, receipt); err != nil {
		s.writeCabinetError(w, "upload receipt", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handlePlategaCheck(w http.ResponseWriter, r *http.Request, userID int64) {
	var req struct {
		RequestID int64 `json:"request_id"`
	}
	if !decodeBody(w, r, 4096, &req) {
		return
	}
	result, err := s.cabinet.CheckPlategaPayment(r.Context(), userID, req.RequestID)
	if err != nil {
		s.writeCabinetError(w, "check payment", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": result.Status})
}

func (s *Server) handleGift(w http.ResponseWriter, r *http.Request, userID int64) {
	var req struct {
		Months int `json:"months"`
	}
	if !decodeBody(w, r, 4096, &req) {
		return
	}
	if err := s.cabinet.CreateGiftRequest(r.Context(), userID, req.Months); err != nil {
		s.writeCabinetError(w, "gift", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleInvite(w http.ResponseWriter, r *http.Request, userID int64) {
	var req struct {
		Username string `json:"username"`
	}
	if !decodeBody(w, r, 4096, &req) {
		return
	}
	if err := s.cabinet.CreateInviteRequest(r.Context(), userID, req.Username); err != nil {
		s.writeCabinetError(w, "invite", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleNotifications(w http.ResponseWriter, r *http.Request, userID int64) {
	var req struct {
		Kind  string `json:"kind"`
		Muted bool   `json:"muted"`
	}
	if !decodeBody(w, r, 4096, &req) {
		return
	}
	if err := s.cabinet.SetNotificationPref(r.Context(), userID, req.Kind, req.Muted); err != nil {
		s.writeCabinetError(w, "notification pref", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleTrial(w http.ResponseWriter, r *http.Request, userID int64) {
	var req struct {
		Username string `json:"username"`
	}
	if !decodeBody(w, r, 4096, &req) {
		return
	}
	result, err := s.cabinet.ClaimTrial(r.Context(), userID, req.Username)
	if err != nil {
		s.writeCabinetError(w, "claim trial", userID, err)
		return
	}
	resp := map[string]any{"ok": true, "username": result.Username}
	if result.Pending {
		resp["pending"] = true
	}
	if result.SubscriptionURL != "" {
		resp["subscription_url"] = result.SubscriptionURL
	}
	writeJSON(w, http.StatusOK, resp)
}

// writeAdminError maps admin service errors to HTTP statuses.
func (s *Server) writeAdminError(w http.ResponseWriter, action string, telegramID int64, err error) {
	switch {
	case errors.Is(err, payments.ErrNotAdmin), errors.Is(err, payments.ErrPaymentsDisabled):
		writeJSONError(w, http.StatusForbidden, "доступ запрещён")
	// An approval that trips the username contract has already rejected the
	// request and notified the user, so this reports a resolved request rather
	// than bad admin input. The code lets the panel refresh its request list.
	case errors.Is(err, payments.ErrInvalidUsername):
		writeJSONErrorCode(w, http.StatusConflict,
			"заявка отклонена: некорректное имя профиля", "invalid_username")
	case errors.Is(err, payments.ErrBadInput):
		writeJSONError(w, http.StatusBadRequest, "некорректные данные")
	case errors.Is(err, payments.ErrTariffUnknown), errors.Is(err, payments.ErrPlanUnknown), errors.Is(err, payments.ErrTrafficExtensionUnknown),
		errors.Is(err, payments.ErrRequestNotFound), errors.Is(err, payments.ErrInfraServerUnknown),
		errors.Is(err, payments.ErrRegistrationGuardPatternNotFound), errors.Is(err, payments.ErrRegistrationGuardNotFound):
		writeJSONError(w, http.StatusNotFound, "не найдено")
	case errors.Is(err, payments.ErrTicketNotFound):
		writeJSONError(w, http.StatusNotFound, "обращение не найдено")
	case errors.Is(err, payments.ErrTicketClosed):
		writeJSONError(w, http.StatusConflict, "обращение уже закрыто")
	case errors.Is(err, payments.ErrRequestResolved):
		writeJSONError(w, http.StatusConflict, "уже обработано")
	case errors.Is(err, payments.ErrBroadcastInProgress):
		writeJSONError(w, http.StatusConflict, "рассылка уже выполняется")
	case errors.Is(err, payments.ErrRegistrationGuardPatternExists):
		writeJSONError(w, http.StatusConflict, "паттерн уже добавлен")
	case errors.Is(err, payments.ErrBackupDryRun):
		writeJSONError(w, http.StatusConflict, "резервная копия недоступна в dry-run")
	case errors.Is(err, payments.ErrBackupTooLarge):
		writeJSONError(w, http.StatusRequestEntityTooLarge, "резервная копия больше 50 МБ")
	case errors.Is(err, payments.ErrBackupCreate):
		s.logger.Error("webapp: admin "+action+" failed", "err", err.Error(), "telegram_id", telegramID)
		writeJSONError(w, http.StatusInternalServerError, "не удалось создать резервную копию")
	case errors.Is(err, payments.ErrBackupDelivery):
		s.logger.Error("webapp: admin "+action+" failed", "err", err.Error(), "telegram_id", telegramID)
		writeJSONError(w, http.StatusInternalServerError, "не удалось отправить резервную копию")
	case errors.Is(err, payments.ErrPanelCreateFailed):
		s.logger.Error("webapp: admin "+action+" failed", "err", err.Error(), "telegram_id", telegramID)
		writeJSONError(w, http.StatusBadGateway, "ошибка создания пользователя в панели, попробуйте позже")
	case errors.Is(err, payments.ErrPanelUnavailable):
		s.logger.Error("webapp: admin "+action+" failed", "err", err.Error(), "telegram_id", telegramID)
		writeJSONError(w, http.StatusBadGateway, "панель недоступна, попробуйте позже")
	default:
		s.logger.Error("webapp: admin "+action+" failed", "err", err.Error(), "telegram_id", telegramID)
		writeJSONError(w, http.StatusInternalServerError, "internal error, try again later")
	}
}

// adminOp builds a handler for one admin op: authenticate, decode the JSON
// body into a fresh input value (a nil factory means the op takes no body),
// run the op through the single door and write the result. A wrapKey wraps a
// non-nil result as {key: result} to keep the established response shapes;
// a nil result becomes {"ok": true}.
func (s *Server) adminOp(name string, newInput func() any, wrapKey string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.authenticate(w, r)
		if !ok {
			return
		}
		var in any
		if newInput != nil {
			in = newInput()
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(in); err != nil {
				writeJSONError(w, http.StatusBadRequest, "malformed request body")
				return
			}
		}
		out, err := s.admin.RunAdminOp(r.Context(), userID, name, in)
		if err != nil {
			s.writeAdminError(w, name, userID, err)
			return
		}
		if wrapKey != "" {
			out = map[string]any{wrapKey: out}
		}
		if out == nil {
			out = map[string]bool{"ok": true}
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// adminIDOp builds a handler for admin mutations whose body is a single
// {"id": N} payload (request confirm/reject/delete, gift revoke, ...).
func (s *Server) adminIDOp(name string) http.HandlerFunc {
	return s.adminOp(name, func() any { return &payments.AdminIDInput{} }, "")
}

// handleAdminAnalytics has a query-param (?days=) input, so it builds the op
// payload itself instead of going through the generic body decoder.
func (s *Server) handleAdminAnalytics(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	days, err := positiveQueryInt(r.URL.Query().Get("days"), 30)
	if err != nil || (days != 7 && days != 30 && days != 90) {
		writeJSONError(w, http.StatusBadRequest, "invalid analytics filters")
		return
	}
	report, err := s.admin.RunAdminOp(r.Context(), userID, "analytics.get", &payments.AdminAnalyticsInput{Days: days})
	if err != nil {
		s.writeAdminError(w, "analytics.get", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

// handleAdminPayments has a query-param filter input, so it builds the op
// payload itself instead of going through the generic body decoder.
func (s *Server) handleAdminPayments(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	days, err := positiveQueryInt(query.Get("days"), 30)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid payment report filters")
		return
	}
	page, err := positiveQueryInt(query.Get("page"), 1)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid payment report filters")
		return
	}
	status := query.Get("status")
	if status == "" {
		status = "all"
	}
	provider := query.Get("provider")
	if provider == "" {
		provider = "all"
	}
	kind := query.Get("kind")
	if kind == "" {
		kind = "all"
	}
	if !validPaymentQuery(days, status, provider, kind, query.Get("q")) {
		writeJSONError(w, http.StatusBadRequest, "invalid payment report filters")
		return
	}
	report, err := s.admin.RunAdminOp(r.Context(), userID, "payments.report", &payments.WebPaymentFilter{
		Days: days, Status: status, Provider: provider, Kind: kind, Query: query.Get("q"), Page: page,
	})
	if err != nil {
		s.writeAdminError(w, "payments.report", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func positiveQueryInt(raw string, fallback int) (int, error) {
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, payments.ErrBadInput
	}
	return n, nil
}

func validPaymentQuery(days int, status, provider, kind, search string) bool {
	if days != 7 && days != 30 && days != 90 || len(strings.TrimSpace(search)) > 100 {
		return false
	}
	if status != "all" && status != "pending" && status != "confirmed" && status != "rejected" {
		return false
	}
	if kind != "all" && kind != "payment" && kind != "gift" && kind != "invite" {
		return false
	}
	return provider == "all" || provider == payments.ProviderP2P || provider == payments.ProviderPlatega || provider == payments.ProviderTelegramStars
}

// handleAdminListUsers has a query-param (?cohort=) input, so it builds the op
// payload itself instead of going through the generic body decoder.
func (s *Server) handleAdminListUsers(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	out, err := s.admin.RunAdminOp(r.Context(), userID, "users.list",
		&payments.AdminCohortInput{Cohort: r.URL.Query().Get("cohort")})
	if err != nil {
		s.writeAdminError(w, "users.list", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func writeJSONErrorCode(w http.ResponseWriter, status int, msg, code string) {
	writeJSON(w, status, map[string]string{"error": msg, "code": code})
}
