package webapp

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
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
	CreateRenewRequest(ctx context.Context, telegramID, remnawaveID int64, months int) error
}

// Admin is the subset of *payments.Service the mini app admin API needs.
// Every method re-checks that telegramID is an admin and returns
// payments.ErrNotAdmin otherwise.
type Admin interface {
	AdminPanelData(ctx context.Context, telegramID int64) (*payments.WebAdminPanel, error)
	AdminSetTariff(ctx context.Context, telegramID int64, months, price int) error
	AdminDeleteTariff(ctx context.Context, telegramID int64, months int) error
	AdminSetRequisites(ctx context.Context, telegramID int64, text string) error
	AdminRevokeGiftCode(ctx context.Context, telegramID, giftID int64) error
	AdminConfirmRequest(ctx context.Context, telegramID, reqID int64) error
	AdminRejectRequest(ctx context.Context, telegramID, reqID int64) error
}

// Server hosts the Telegram Mini App: embedded static frontend plus the JSON
// API authenticated via Telegram initData.
type Server struct {
	cabinet  Cabinet
	admin    Admin
	botToken string
	logger   *slog.Logger
	now      func() time.Time
}

func NewServer(cabinet Cabinet, admin Admin, botToken string, logger *slog.Logger) *Server {
	return &Server{cabinet: cabinet, admin: admin, botToken: botToken, logger: logger, now: time.Now}
}

// Handler returns the mini app HTTP handler (static files + /api routes).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(staticFS, "static")
	mux.Handle("/", http.FileServer(http.FS(static)))
	mux.HandleFunc("GET /api/me", s.handleMe)
	mux.HandleFunc("POST /api/renew", s.handleRenew)
	mux.HandleFunc("GET /api/admin", s.handleAdminPanel)
	mux.HandleFunc("POST /api/admin/tariff", s.handleAdminSetTariff)
	mux.HandleFunc("POST /api/admin/tariff/delete", s.handleAdminDeleteTariff)
	mux.HandleFunc("POST /api/admin/requisites", s.handleAdminSetRequisites)
	mux.HandleFunc("POST /api/admin/gift/revoke", s.adminIDAction("revoke gift", func(ctx context.Context, tgID, id int64) error {
		return s.admin.AdminRevokeGiftCode(ctx, tgID, id)
	}))
	mux.HandleFunc("POST /api/admin/request/confirm", s.adminIDAction("confirm request", func(ctx context.Context, tgID, id int64) error {
		return s.admin.AdminConfirmRequest(ctx, tgID, id)
	}))
	mux.HandleFunc("POST /api/admin/request/reject", s.adminIDAction("reject request", func(ctx context.Context, tgID, id int64) error {
		return s.admin.AdminRejectRequest(ctx, tgID, id)
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
		RemnawaveID int64 `json:"remnawave_id"`
		Months      int   `json:"months"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "malformed request body")
		return
	}

	err := s.cabinet.CreateRenewRequest(r.Context(), userID, req.RemnawaveID, req.Months)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case errors.Is(err, payments.ErrNotLinked),
		errors.Is(err, payments.ErrProfileUnknown):
		writeJSONError(w, http.StatusForbidden, "profile is not linked to this account")
	case errors.Is(err, payments.ErrTariffUnknown):
		writeJSONError(w, http.StatusBadRequest, "tariff not found")
	case errors.Is(err, payments.ErrPaymentsDisabled):
		writeJSONError(w, http.StatusServiceUnavailable, "payments are disabled")
	default:
		s.logger.Error("webapp: renew failed", "err", err.Error(), "telegram_id", userID)
		writeJSONError(w, http.StatusInternalServerError, "internal error, try again later")
	}
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
