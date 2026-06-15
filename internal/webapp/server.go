package webapp

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/payments"
)

//go:embed static
var staticFS embed.FS

// initDataMaxAge is how long a Mini App initData signature stays acceptable.
const initDataMaxAge = 24 * time.Hour

// Cabinet is the subset of *payments.Service the mini app user API needs.
type Cabinet interface {
	CabinetData(ctx context.Context, telegramID int64) (*payments.WebCabinet, error)
	CreateRenewRequest(ctx context.Context, telegramID, remnawaveID int64, months int, provider string) (*payments.RenewResult, error)
	CreateGiftRequest(ctx context.Context, telegramID int64, months int) error
	CreateInviteRequest(ctx context.Context, telegramID int64, username string) error
}

// Admin is the subset of *payments.Service the mini app admin API needs.
// Every method re-checks that telegramID is an admin and returns
// payments.ErrNotAdmin otherwise.
type Admin interface {
	AdminPanelData(ctx context.Context, telegramID int64) (*payments.WebAdminPanel, error)
	AdminSetTariff(ctx context.Context, telegramID int64, months, price int) error
	AdminDeleteTariff(ctx context.Context, telegramID int64, months int) error
	AdminSetRequisites(ctx context.Context, telegramID int64, text string) error
	AdminSetRequireScreenshot(ctx context.Context, telegramID int64, on bool) error
	AdminSetProviderEnabled(ctx context.Context, telegramID int64, provider string, on bool) error
	AdminListSquads(ctx context.Context, telegramID int64) ([]payments.WebSquad, error)
	AdminSetDefaultSquad(ctx context.Context, telegramID int64, uuid string) error
	AdminListUsers(ctx context.Context, telegramID int64) ([]payments.WebUserRow, error)
	AdminSetDefaultTrafficReset(ctx context.Context, telegramID int64, strategy string) error
	AdminFindUser(ctx context.Context, telegramID int64, query string) (*payments.WebManagedUser, error)
	AdminUpdateUser(ctx context.Context, telegramID int64, req payments.WebUserUpdate) error
	AdminRevokeGiftCode(ctx context.Context, telegramID, giftID int64) error
	AdminConfirmRequest(ctx context.Context, telegramID, reqID int64) error
	AdminRejectRequest(ctx context.Context, telegramID, reqID int64) error
	AdminConfirmGiftRequest(ctx context.Context, telegramID, giftID int64) error
	AdminRejectGiftRequest(ctx context.Context, telegramID, giftID int64) error
	AdminApproveInviteRequest(ctx context.Context, telegramID, reqID int64) error
	AdminRejectInviteRequest(ctx context.Context, telegramID, reqID int64) error
	AdminBroadcast(ctx context.Context, telegramID int64, text string) (*payments.WebBroadcastResult, error)
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
	static, _ := fs.Sub(staticFS, "static")
	mux.Handle("/", http.FileServer(http.FS(static)))
	// Public payment-gateway callback (no initData auth): Platega verifies the
	// transaction itself via its API, so the body is trusted only for its id.
	mux.HandleFunc("POST /platega/callback", s.handlePlategaCallback)
	mux.HandleFunc("GET /api/me", s.handleMe)
	mux.HandleFunc("POST /api/renew", s.handleRenew)
	mux.HandleFunc("POST /api/gift", s.handleGift)
	mux.HandleFunc("POST /api/invite", s.handleInvite)
	mux.HandleFunc("GET /api/admin", s.handleAdminPanel)
	mux.HandleFunc("POST /api/admin/tariff", s.handleAdminSetTariff)
	mux.HandleFunc("POST /api/admin/tariff/delete", s.handleAdminDeleteTariff)
	mux.HandleFunc("POST /api/admin/requisites", s.handleAdminSetRequisites)
	mux.HandleFunc("POST /api/admin/screenshot-toggle", s.handleAdminSetRequireScreenshot)
	mux.HandleFunc("POST /api/admin/payment-provider", s.handleAdminSetPaymentProvider)
	mux.HandleFunc("GET /api/admin/squads", s.handleAdminListSquads)
	mux.HandleFunc("POST /api/admin/squad", s.handleAdminSetDefaultSquad)
	mux.HandleFunc("GET /api/admin/users", s.handleAdminListUsers)
	mux.HandleFunc("POST /api/admin/traffic-reset", s.handleAdminSetDefaultTrafficReset)
	mux.HandleFunc("POST /api/admin/user/find", s.handleAdminFindUser)
	mux.HandleFunc("POST /api/admin/user/update", s.handleAdminUpdateUser)
	mux.HandleFunc("POST /api/admin/broadcast", s.handleAdminBroadcast)
	mux.HandleFunc("POST /api/admin/gift/revoke", s.adminIDAction("revoke gift", func(ctx context.Context, tgID, id int64) error {
		return s.admin.AdminRevokeGiftCode(ctx, tgID, id)
	}))
	mux.HandleFunc("POST /api/admin/request/confirm", s.adminIDAction("confirm request", func(ctx context.Context, tgID, id int64) error {
		return s.admin.AdminConfirmRequest(ctx, tgID, id)
	}))
	mux.HandleFunc("POST /api/admin/request/reject", s.adminIDAction("reject request", func(ctx context.Context, tgID, id int64) error {
		return s.admin.AdminRejectRequest(ctx, tgID, id)
	}))
	mux.HandleFunc("POST /api/admin/gift-request/confirm", s.adminIDAction("confirm gift request", func(ctx context.Context, tgID, id int64) error {
		return s.admin.AdminConfirmGiftRequest(ctx, tgID, id)
	}))
	mux.HandleFunc("POST /api/admin/gift-request/reject", s.adminIDAction("reject gift request", func(ctx context.Context, tgID, id int64) error {
		return s.admin.AdminRejectGiftRequest(ctx, tgID, id)
	}))
	mux.HandleFunc("POST /api/admin/invite-request/confirm", s.adminIDAction("approve invite request", func(ctx context.Context, tgID, id int64) error {
		return s.admin.AdminApproveInviteRequest(ctx, tgID, id)
	}))
	mux.HandleFunc("POST /api/admin/invite-request/reject", s.adminIDAction("reject invite request", func(ctx context.Context, tgID, id int64) error {
		return s.admin.AdminRejectInviteRequest(ctx, tgID, id)
	}))
	return mux
}

// Run serves the mini app on addr until ctx is cancelled.
func (s *Server) Run(ctx context.Context, addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
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

// authenticate extracts and validates initData from the Authorization header
// ("tma <initData>") and returns the Telegram user ID, or writes an error
// response and returns false.
func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "tma ")
	if !ok || raw == "" {
		writeJSONError(w, http.StatusUnauthorized, "missing initData")
		return 0, false
	}
	userID, err := validateInitData(raw, s.botToken, initDataMaxAge, s.now())
	if err != nil {
		s.logger.Warn("webapp: initData validation failed", "err", err.Error())
		writeJSONError(w, http.StatusUnauthorized, "invalid initData")
		return 0, false
	}
	return userID, true
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

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	data, err := s.cabinet.CabinetData(r.Context(), userID)
	if err != nil {
		s.logger.Error("webapp: cabinet data failed", "err", err.Error(), "telegram_id", userID)
		writeJSONError(w, http.StatusBadGateway, "panel request failed, try again later")
		return
	}
	writeJSON(w, http.StatusOK, data)
}

func (s *Server) handleRenew(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	var req struct {
		RemnawaveID int64  `json:"remnawave_id"`
		Months      int    `json:"months"`
		Provider    string `json:"provider"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	result, err := s.cabinet.CreateRenewRequest(r.Context(), userID, req.RemnawaveID, req.Months, req.Provider)
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
		if result.InvoiceURL != "" {
			resp["invoice_url"] = result.InvoiceURL
		}
		if len(result.Providers) > 0 {
			resp["providers"] = result.Providers
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// writeCabinetError maps user-facing cabinet service errors to HTTP statuses.
func (s *Server) writeCabinetError(w http.ResponseWriter, action string, telegramID int64, err error) {
	switch {
	case errors.Is(err, payments.ErrNotLinked), errors.Is(err, payments.ErrProfileUnknown):
		writeJSONError(w, http.StatusForbidden, "profile is not linked to this account")
	case errors.Is(err, payments.ErrTariffUnknown):
		writeJSONError(w, http.StatusBadRequest, "tariff not found")
	case errors.Is(err, payments.ErrBadInput):
		writeJSONError(w, http.StatusBadRequest, "invalid input")
	case errors.Is(err, payments.ErrPaymentsDisabled):
		writeJSONError(w, http.StatusServiceUnavailable, "payments are disabled")
	default:
		s.logger.Error("webapp: "+action+" failed", "err", err.Error(), "telegram_id", telegramID)
		writeJSONError(w, http.StatusInternalServerError, "internal error, try again later")
	}
}

func (s *Server) handleGift(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	var req struct {
		Months int `json:"months"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if err := s.cabinet.CreateGiftRequest(r.Context(), userID, req.Months); err != nil {
		s.writeCabinetError(w, "gift", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleInvite(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	var req struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if err := s.cabinet.CreateInviteRequest(r.Context(), userID, req.Username); err != nil {
		s.writeCabinetError(w, "invite", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// writeAdminError maps admin service errors to HTTP statuses.
func (s *Server) writeAdminError(w http.ResponseWriter, action string, telegramID int64, err error) {
	switch {
	case errors.Is(err, payments.ErrNotAdmin), errors.Is(err, payments.ErrPaymentsDisabled):
		writeJSONError(w, http.StatusForbidden, "доступ запрещён")
	case errors.Is(err, payments.ErrBadInput):
		writeJSONError(w, http.StatusBadRequest, "некорректные данные")
	case errors.Is(err, payments.ErrTariffUnknown), errors.Is(err, payments.ErrRequestNotFound):
		writeJSONError(w, http.StatusNotFound, "не найдено")
	case errors.Is(err, payments.ErrRequestResolved):
		writeJSONError(w, http.StatusConflict, "уже обработано")
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

func (s *Server) handleAdminPanel(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	panel, err := s.admin.AdminPanelData(r.Context(), userID)
	if err != nil {
		s.writeAdminError(w, "panel data", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, panel)
}

func (s *Server) handleAdminSetTariff(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	var req struct {
		Months int `json:"months"`
		Price  int `json:"price"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if err := s.admin.AdminSetTariff(r.Context(), userID, req.Months, req.Price); err != nil {
		s.writeAdminError(w, "set tariff", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAdminDeleteTariff(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	var req struct {
		Months int `json:"months"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if err := s.admin.AdminDeleteTariff(r.Context(), userID, req.Months); err != nil {
		s.writeAdminError(w, "delete tariff", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAdminSetRequisites(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	var req struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if err := s.admin.AdminSetRequisites(r.Context(), userID, req.Text); err != nil {
		s.writeAdminError(w, "set requisites", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAdminSetRequireScreenshot(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if err := s.admin.AdminSetRequireScreenshot(r.Context(), userID, req.Enabled); err != nil {
		s.writeAdminError(w, "set screenshot requirement", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAdminSetPaymentProvider(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	var req struct {
		Provider string `json:"provider"`
		Enabled  bool   `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if err := s.admin.AdminSetProviderEnabled(r.Context(), userID, req.Provider, req.Enabled); err != nil {
		s.writeAdminError(w, "set payment provider", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAdminListSquads(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	squads, err := s.admin.AdminListSquads(r.Context(), userID)
	if err != nil {
		s.writeAdminError(w, "list squads", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"squads": squads})
}

func (s *Server) handleAdminSetDefaultSquad(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	var req struct {
		UUID string `json:"uuid"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if err := s.admin.AdminSetDefaultSquad(r.Context(), userID, req.UUID); err != nil {
		s.writeAdminError(w, "set default squad", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAdminListUsers(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	users, err := s.admin.AdminListUsers(r.Context(), userID)
	if err != nil {
		s.writeAdminError(w, "list users", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

func (s *Server) handleAdminSetDefaultTrafficReset(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	var req struct {
		Strategy string `json:"strategy"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if err := s.admin.AdminSetDefaultTrafficReset(r.Context(), userID, req.Strategy); err != nil {
		s.writeAdminError(w, "set default traffic reset", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAdminFindUser(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	var req struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	user, err := s.admin.AdminFindUser(r.Context(), userID, req.Query)
	if err != nil {
		s.writeAdminError(w, "find user", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (s *Server) handleAdminUpdateUser(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	var req payments.WebUserUpdate
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if err := s.admin.AdminUpdateUser(r.Context(), userID, req); err != nil {
		s.writeAdminError(w, "update user", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAdminBroadcast(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	var req struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	res, err := s.admin.AdminBroadcast(r.Context(), userID, req.Message)
	if err != nil {
		s.writeAdminError(w, "broadcast", userID, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "sent": res.Sent, "failed": res.Failed})
}

// adminIDAction builds a handler for admin mutations whose body is a single
// {"id": N} payload (gift revoke, request confirm/reject).
func (s *Server) adminIDAction(action string, fn func(ctx context.Context, telegramID, id int64) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := s.authenticate(w, r)
		if !ok {
			return
		}
		var req struct {
			ID int64 `json:"id"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "malformed request body")
			return
		}
		if err := fn(r.Context(), userID, req.ID); err != nil {
			s.writeAdminError(w, action, userID, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
