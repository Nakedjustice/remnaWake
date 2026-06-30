package webapp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/payments"
)

type fakeCabinet struct {
	data        *payments.WebCabinet
	renewErr    error
	renewResult *payments.RenewResult
	renewed     []int64
	giftErr     error
	gifted      []int
	inviteErr   error
	invited     []string
	notifErr    error
	notifSet    []string // "<kind>:<muted>" per call
	trialErr    error
	trialResult *payments.WebTrialResult
	trialNames  []string
	parityErr   error
	receipt     payments.WebReceipt
}

func TestWebParityJSONRoutes(t *testing.T) {
	for _, tc := range []struct{ path, body, want string }{
		{"/api/register", `{"query":"alice"}`, `"username":"alice"`},
		{"/api/gift/redeem", `{"code":"RW-X","remnawave_id":7,"username":"alice"}`, `"ok":true`},
		{"/api/platega/check", `{"request_id":9}`, `"status":"pending"`},
	} {
		srv := newTestServer(&fakeCabinet{})
		req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Authorization", validAuth(t))
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), tc.want) {
			t.Fatalf("%s: code=%d body=%s", tc.path, w.Code, w.Body.String())
		}
	}
}

func TestWebParityErrorMappings(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want int
	}{
		{payments.ErrInvalidProfileQuery, 400}, {payments.ErrProfileLinkedElsewhere, 403},
		{payments.ErrGiftUsed, 409}, {payments.ErrReceiptSessionExpired, 410},
		{payments.ErrReceiptTooLarge, 413}, {payments.ErrPaymentRequestInaccessible, 404},
	} {
		srv := newTestServer(&fakeCabinet{parityErr: tc.err})
		req := httptest.NewRequest(http.MethodPost, "/api/register", strings.NewReader(`{"query":"alice"}`))
		req.Header.Set("Authorization", validAuth(t))
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Fatalf("err=%v code=%d want=%d body=%s", tc.err, w.Code, tc.want, w.Body.String())
		}
	}
}

func TestReceiptMultipartRoute(t *testing.T) {
	cab := &fakeCabinet{}
	srv := newTestServer(cab)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("note", "paid")
	part, _ := mw.CreateFormFile("file", "receipt.pdf")
	_, _ = part.Write([]byte("pdf-data"))
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/receipt", &body)
	req.Header.Set("Authorization", validAuth(t))
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != 200 || cab.receipt.Filename != "receipt.pdf" || cab.receipt.Note != "paid" || string(cab.receipt.Data) != "pdf-data" {
		t.Fatalf("code=%d receipt=%+v", w.Code, cab.receipt)
	}
}

func TestEmbeddedFrontendContainsNativeParityFlows(t *testing.T) {
	srv := newTestServer(&fakeCabinet{})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	body := w.Body.String()
	for _, want := range []string{"/api/register", "/api/gift/redeem", "/api/receipt", "/api/platega/check", "localStorage.setItem(checkKey"} {
		if !strings.Contains(body, want) {
			t.Fatalf("frontend missing %q", want)
		}
	}
	if strings.Contains(body, "Привязка профиля и активация подарочных кодов оформляются в диалоге с ботом") {
		t.Fatal("obsolete bot handoff remains")
	}
}

func (f *fakeCabinet) CabinetData(_ context.Context, _ int64) (*payments.WebCabinet, error) {
	return f.data, nil
}

func (f *fakeCabinet) CreateRenewRequest(_ context.Context, _, remnawaveID int64, _ int, _ string) (*payments.RenewResult, error) {
	f.renewed = append(f.renewed, remnawaveID)
	return f.renewResult, f.renewErr
}

func (f *fakeCabinet) CreateGiftRequest(_ context.Context, _ int64, months int) error {
	f.gifted = append(f.gifted, months)
	return f.giftErr
}

func (f *fakeCabinet) CreateInviteRequest(_ context.Context, _ int64, username string) error {
	f.invited = append(f.invited, username)
	return f.inviteErr
}
func (f *fakeCabinet) RegisterProfile(_ context.Context, _ int64, query string) (*payments.WebRegistrationResult, error) {
	return &payments.WebRegistrationResult{Username: query}, f.parityErr
}
func (f *fakeCabinet) RedeemGift(_ context.Context, _ int64, code string, _ int64, username string) (*payments.WebGiftRedemptionResult, error) {
	return &payments.WebGiftRedemptionResult{Username: username}, f.parityErr
}
func (f *fakeCabinet) UploadRenewReceipt(_ context.Context, _ int64, receipt payments.WebReceipt) error {
	f.receipt = receipt
	return f.parityErr
}
func (f *fakeCabinet) CheckPlategaPayment(_ context.Context, _ int64, _ int64) (*payments.WebPaymentStatus, error) {
	return &payments.WebPaymentStatus{Status: "pending"}, f.parityErr
}

func (f *fakeCabinet) SetNotificationPref(_ context.Context, _ int64, kind string, muted bool) error {
	f.notifSet = append(f.notifSet, fmt.Sprintf("%s:%t", kind, muted))
	return f.notifErr
}

func (f *fakeCabinet) ClaimTrial(_ context.Context, _ int64, username string) (*payments.WebTrialResult, error) {
	f.trialNames = append(f.trialNames, username)
	return f.trialResult, f.trialErr
}

func (f *fakeCabinet) SupportHistoryUser(_ context.Context, _ int64) (*payments.WebSupport, error) {
	return &payments.WebSupport{}, nil
}

func (f *fakeCabinet) SupportSendUser(_ context.Context, _ int64, _ string) error { return nil }

func (f *fakeCabinet) SupportCloseUser(_ context.Context, _ int64) error { return nil }

type adminCall struct {
	Name string
	A, B int64
	Text string
}

type fakeAdmin struct {
	panel       *payments.WebAdminPanel
	stats       *payments.WebAdminStats
	report      *payments.WebPaymentReport
	proxyHealth *payments.WebProxyHealth
	err         error
	calls       []adminCall
}

func (f *fakeAdmin) AdminPanelData(_ context.Context, tgID int64) (*payments.WebAdminPanel, error) {
	f.calls = append(f.calls, adminCall{Name: "panel", A: tgID})
	return f.panel, f.err
}
func (f *fakeAdmin) AdminStatsData(_ context.Context, tgID int64) (*payments.WebAdminStats, error) {
	f.calls = append(f.calls, adminCall{Name: "stats", A: tgID})
	return f.stats, f.err
}
func (f *fakeAdmin) AdminProxyHealth(_ context.Context, tgID int64) (*payments.WebProxyHealth, error) {
	f.calls = append(f.calls, adminCall{Name: "proxy-health", A: tgID})
	return f.proxyHealth, f.err
}
func (f *fakeAdmin) AdminSetProxyNotification(_ context.Context, tgID int64, name, address, subName string, muted bool) error {
	f.calls = append(f.calls, adminCall{Name: "proxy-notification", A: tgID, Text: fmt.Sprintf("%s|%s|%s|%t", address, name, subName, muted)})
	return f.err
}
func (f *fakeAdmin) AdminPaymentReport(_ context.Context, tgID int64, filter payments.WebPaymentFilter) (*payments.WebPaymentReport, error) {
	f.calls = append(f.calls, adminCall{Name: "payments", A: tgID, B: int64(filter.Page), Text: fmt.Sprintf("%d:%s:%s:%s", filter.Days, filter.Status, filter.Provider, filter.Query)})
	return f.report, f.err
}
func (f *fakeAdmin) AdminDeletePaymentRequest(_ context.Context, tgID, id int64) error {
	f.calls = append(f.calls, adminCall{Name: "delete payment", A: tgID, B: id})
	return f.err
}
func (f *fakeAdmin) AdminCheckUpdates(_ context.Context, tgID int64) (*payments.WebUpdateCheckResult, error) {
	f.calls = append(f.calls, adminCall{Name: "checkupdates", A: tgID})
	return &payments.WebUpdateCheckResult{Status: "up_to_date", Message: "ok"}, f.err
}
func (f *fakeAdmin) AdminSetUpdateInterval(_ context.Context, tgID int64, interval string) error {
	f.calls = append(f.calls, adminCall{Name: "setupdateinterval", A: tgID, Text: interval})
	return f.err
}
func (f *fakeAdmin) AdminSetTariff(_ context.Context, tgID int64, months, price int) error {
	f.calls = append(f.calls, adminCall{Name: "settariff", A: int64(months), B: int64(price)})
	return f.err
}
func (f *fakeAdmin) AdminDeleteTariff(_ context.Context, tgID int64, months int) error {
	f.calls = append(f.calls, adminCall{Name: "deltariff", A: int64(months)})
	return f.err
}
func (f *fakeAdmin) AdminSetRequisites(_ context.Context, tgID int64, text string) error {
	f.calls = append(f.calls, adminCall{Name: "setreq", Text: text})
	return f.err
}
func (f *fakeAdmin) AdminSetTrial(_ context.Context, tgID int64, cfg payments.TrialConfig) error {
	f.calls = append(f.calls, adminCall{Name: "settrial", A: tgID, B: int64(cfg.Days), Text: fmt.Sprintf("%t:%d:%d:%s", cfg.Enabled, cfg.TrafficLimitGB, cfg.HwidDeviceLimit, cfg.SquadUUID)})
	return f.err
}
func (f *fakeAdmin) AdminSetReferral(_ context.Context, tgID int64, enabled bool, inviter, invitee int) error {
	f.calls = append(f.calls, adminCall{Name: "setreferral", A: int64(inviter), B: int64(invitee), Text: fmt.Sprintf("%t", enabled)})
	return f.err
}
func (f *fakeAdmin) AdminSetRequireScreenshot(_ context.Context, tgID int64, on bool) error {
	var b int64
	if on {
		b = 1
	}
	f.calls = append(f.calls, adminCall{Name: "setshot", A: tgID, B: b})
	return f.err
}

func (f *fakeAdmin) AdminSetProviderEnabled(_ context.Context, tgID int64, _ string, _ bool) error {
	f.calls = append(f.calls, adminCall{Name: "setprovider", A: tgID})
	return f.err
}
func (f *fakeAdmin) AdminListSquads(_ context.Context, tgID int64) ([]payments.WebSquad, error) {
	f.calls = append(f.calls, adminCall{Name: "listsquads", A: tgID})
	if f.err != nil {
		return nil, f.err
	}
	return []payments.WebSquad{
		{UUID: "sq-1", Name: "Default-Squad", Selected: true},
		{UUID: "sq-2", Name: "Premium"},
	}, nil
}
func (f *fakeAdmin) AdminSetDefaultSquad(_ context.Context, tgID int64, uuid string) error {
	f.calls = append(f.calls, adminCall{Name: "setsquad", A: tgID, Text: uuid})
	return f.err
}
func (f *fakeAdmin) AdminListUsers(_ context.Context, tgID int64) ([]payments.WebUserRow, error) {
	f.calls = append(f.calls, adminCall{Name: "listusers", A: tgID})
	if f.err != nil {
		return nil, f.err
	}
	return []payments.WebUserRow{
		{UUID: "u-1", Username: "alice", Status: "ACTIVE", ExpireAt: "01.07.2026"},
	}, nil
}
func (f *fakeAdmin) AdminListUsersByCohort(_ context.Context, tgID int64, cohort string) ([]payments.WebUserRow, error) {
	f.calls = append(f.calls, adminCall{Name: "listuserscohort", A: tgID, Text: cohort})
	if f.err != nil {
		return nil, f.err
	}
	return []payments.WebUserRow{
		{UUID: "u-2", Username: "bob", Status: "EXPIRED", ExpireAt: "01.06.2026"},
	}, nil
}
func (f *fakeAdmin) AdminSetDefaultTrafficReset(_ context.Context, tgID int64, strategy string) error {
	f.calls = append(f.calls, adminCall{Name: "settreset", A: tgID, Text: strategy})
	return f.err
}
func (f *fakeAdmin) AdminFindUser(_ context.Context, tgID int64, query string) (*payments.WebManagedUser, error) {
	f.calls = append(f.calls, adminCall{Name: "finduser", A: tgID, Text: query})
	if f.err != nil {
		return nil, f.err
	}
	return &payments.WebManagedUser{UUID: "u-1", Username: query, Status: "ACTIVE"}, nil
}
func (f *fakeAdmin) AdminUpdateUser(_ context.Context, tgID int64, req payments.WebUserUpdate) error {
	f.calls = append(f.calls, adminCall{Name: "updateuser", A: tgID, Text: req.UUID})
	return f.err
}
func (f *fakeAdmin) AdminRevokeGiftCode(_ context.Context, tgID, giftID int64) error {
	f.calls = append(f.calls, adminCall{Name: "revoke", A: giftID})
	return f.err
}
func (f *fakeAdmin) AdminConfirmRequest(_ context.Context, tgID, reqID int64) error {
	f.calls = append(f.calls, adminCall{Name: "confirm", A: reqID})
	return f.err
}
func (f *fakeAdmin) AdminRejectRequest(_ context.Context, tgID, reqID int64) error {
	f.calls = append(f.calls, adminCall{Name: "reject", A: reqID})
	return f.err
}
func (f *fakeAdmin) AdminConfirmGiftRequest(_ context.Context, tgID, giftID int64) error {
	f.calls = append(f.calls, adminCall{Name: "giftconfirm", A: giftID})
	return f.err
}
func (f *fakeAdmin) AdminRejectGiftRequest(_ context.Context, tgID, giftID int64) error {
	f.calls = append(f.calls, adminCall{Name: "giftreject", A: giftID})
	return f.err
}
func (f *fakeAdmin) AdminApproveInviteRequest(_ context.Context, tgID, reqID int64) error {
	f.calls = append(f.calls, adminCall{Name: "inviteapprove", A: reqID})
	return f.err
}
func (f *fakeAdmin) AdminRejectInviteRequest(_ context.Context, tgID, reqID int64) error {
	f.calls = append(f.calls, adminCall{Name: "invitereject", A: reqID})
	return f.err
}
func (f *fakeAdmin) AdminApproveTrialRequest(_ context.Context, tgID, reqID int64) error {
	f.calls = append(f.calls, adminCall{Name: "trialapprove", A: reqID})
	return f.err
}
func (f *fakeAdmin) AdminRejectTrialRequest(_ context.Context, tgID, reqID int64) error {
	f.calls = append(f.calls, adminCall{Name: "trialreject", A: reqID})
	return f.err
}
func (f *fakeAdmin) AdminBroadcast(_ context.Context, tgID int64, text string) (*payments.WebBroadcastResult, error) {
	f.calls = append(f.calls, adminCall{Name: "broadcast", A: tgID, Text: text})
	if f.err != nil {
		return nil, f.err
	}
	return &payments.WebBroadcastResult{Sent: 3, Failed: 1}, nil
}

func (f *fakeAdmin) AdminListInfraServers(_ context.Context, tgID int64) ([]payments.WebInfraServer, error) {
	f.calls = append(f.calls, adminCall{Name: "list_servers", A: tgID})
	return nil, f.err
}
func (f *fakeAdmin) AdminSaveInfraServer(_ context.Context, tgID int64, in payments.WebInfraServerInput) error {
	f.calls = append(f.calls, adminCall{Name: "save_server", A: tgID, Text: in.Name})
	return f.err
}
func (f *fakeAdmin) AdminDeleteInfraServer(_ context.Context, tgID, id int64) error {
	f.calls = append(f.calls, adminCall{Name: "delete_server", A: tgID, B: id})
	return f.err
}
func (f *fakeAdmin) AdminMarkInfraServerPaid(_ context.Context, tgID, id int64) error {
	f.calls = append(f.calls, adminCall{Name: "paid_server", A: tgID, B: id})
	return f.err
}
func (f *fakeAdmin) AdminListFxRates(_ context.Context, tgID int64) (*payments.WebFxRates, error) {
	f.calls = append(f.calls, adminCall{Name: "list_fx", A: tgID})
	if f.err != nil {
		return nil, f.err
	}
	return &payments.WebFxRates{}, nil
}
func (f *fakeAdmin) AdminSetManualFxRate(_ context.Context, tgID int64, currency string, rate float64) error {
	f.calls = append(f.calls, adminCall{Name: "set_fx", A: tgID, Text: currency})
	return f.err
}
func (f *fakeAdmin) AdminSetBaseCurrency(_ context.Context, tgID int64, iso string) error {
	f.calls = append(f.calls, adminCall{Name: "set_base", A: tgID, Text: iso})
	return f.err
}
func (f *fakeAdmin) AdminRefreshFxRates(_ context.Context, tgID int64) error {
	f.calls = append(f.calls, adminCall{Name: "refresh_fx", A: tgID})
	return f.err
}

func (f *fakeAdmin) SupportConversations(_ context.Context, tgID int64) ([]payments.WebSupportConversation, error) {
	f.calls = append(f.calls, adminCall{Name: "support_conversations", A: tgID})
	return nil, f.err
}

func (f *fakeAdmin) SupportThreadAdmin(_ context.Context, tgID, targetUserID int64) (*payments.WebSupport, error) {
	f.calls = append(f.calls, adminCall{Name: "support_thread", A: tgID, B: targetUserID})
	if f.err != nil {
		return nil, f.err
	}
	return &payments.WebSupport{}, nil
}

func (f *fakeAdmin) SupportSendAdmin(_ context.Context, tgID, targetUserID int64, text string) error {
	f.calls = append(f.calls, adminCall{Name: "support_send", A: tgID, B: targetUserID, Text: text})
	return f.err
}

func (f *fakeAdmin) SupportCloseAdmin(_ context.Context, tgID, targetUserID int64) error {
	f.calls = append(f.calls, adminCall{Name: "support_close", A: tgID, B: targetUserID})
	return f.err
}

// fakeWebhooks records Platega webhook bodies handed to the service.
type fakeWebhooks struct {
	bodies [][]byte
	err    error
}

func (f *fakeWebhooks) HandlePlategaWebhook(_ context.Context, body []byte) error {
	f.bodies = append(f.bodies, body)
	return f.err
}

func newTestServer(cab *fakeCabinet) *Server {
	return newTestServerWithAdmin(cab, &fakeAdmin{})
}

func newTestServerWithAdmin(cab *fakeCabinet, adm *fakeAdmin) *Server {
	return newTestServerFull(cab, adm, &fakeWebhooks{})
}

func newTestServerFull(cab *fakeCabinet, adm *fakeAdmin, wh Webhooks) *Server {
	s := NewServer(cab, adm, wh, testToken, slog.New(slog.DiscardHandler))
	s.now = func() time.Time { return time.Unix(1700000000, 0) }
	return s
}

func validAuth(t *testing.T) string {
	return "tma " + signInitData(t, map[string]string{
		"auth_date": "1699999000",
		"user":      `{"id":42}`,
	})
}

func TestHandleMeUnauthorized(t *testing.T) {
	srv := newTestServer(&fakeCabinet{})
	for _, auth := range []string{"", "tma garbage", "Bearer x"} {
		req := httptest.NewRequest("GET", "/api/me", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		if w.Code != 401 {
			t.Errorf("auth %q: status = %d, want 401", auth, w.Code)
		}
	}
}

func TestPlategaCallbackNoAuth(t *testing.T) {
	wh := &fakeWebhooks{}
	srv := newTestServerFull(&fakeCabinet{}, &fakeAdmin{}, wh)

	body := `{"transactionId":"tx-1"}`
	req := httptest.NewRequest("POST", "/platega/callback", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200 (webhook needs no initData)", w.Code)
	}
	if len(wh.bodies) != 1 || string(wh.bodies[0]) != body {
		t.Fatalf("webhook body not forwarded: %v", wh.bodies)
	}
}

func TestHandleMeOK(t *testing.T) {
	cab := &fakeCabinet{data: &payments.WebCabinet{
		Linked:   true,
		Profiles: []payments.WebProfile{{RemnawaveID: 7, Username: "alice"}},
	}}
	srv := newTestServer(cab)

	req := httptest.NewRequest("GET", "/api/me", nil)
	req.Header.Set("Authorization", validAuth(t))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var got payments.WebCabinet
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Linked || len(got.Profiles) != 1 || got.Profiles[0].Username != "alice" {
		t.Fatalf("unexpected payload: %+v", got)
	}
}

func TestHandleRenewErrorMapping(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{nil, 200},
		{payments.ErrNotLinked, 403},
		{payments.ErrProfileUnknown, 403},
		{payments.ErrTariffUnknown, 400},
		{payments.ErrPaymentsDisabled, 503},
	}
	for _, tc := range cases {
		srv := newTestServer(&fakeCabinet{renewErr: tc.err})
		req := httptest.NewRequest("POST", "/api/renew", strings.NewReader(`{"remnawave_id":7,"months":3}`))
		req.Header.Set("Authorization", validAuth(t))
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Errorf("err=%v: status = %d, want %d", tc.err, w.Code, tc.want)
		}
	}
}

func TestHandleRenewAwaitingScreenshot(t *testing.T) {
	srv := newTestServer(&fakeCabinet{renewErr: payments.ErrScreenshotRequired})
	req := httptest.NewRequest("POST", "/api/renew", strings.NewReader(`{"remnawave_id":7,"months":3}`))
	req.Header.Set("Authorization", validAuth(t))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["status"] != "awaiting_screenshot" {
		t.Fatalf("unexpected payload: %v", got)
	}
}

func TestHandleAdminScreenshotToggle(t *testing.T) {
	adm := &fakeAdmin{}
	srv := newTestServerWithAdmin(&fakeCabinet{}, adm)
	req := httptest.NewRequest("POST", "/api/admin/screenshot-toggle", strings.NewReader(`{"enabled":true}`))
	req.Header.Set("Authorization", validAuth(t))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if len(adm.calls) != 1 || adm.calls[0].Name != "setshot" || adm.calls[0].B != 1 {
		t.Fatalf("unexpected admin calls: %+v", adm.calls)
	}

	// Non-admin (service error) maps to 403.
	adm = &fakeAdmin{err: payments.ErrNotAdmin}
	srv = newTestServerWithAdmin(&fakeCabinet{}, adm)
	req = httptest.NewRequest("POST", "/api/admin/screenshot-toggle", strings.NewReader(`{"enabled":true}`))
	req.Header.Set("Authorization", validAuth(t))
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
	}
}

func TestHandleAdminProxyNotification(t *testing.T) {
	adm := &fakeAdmin{}
	srv := newTestServerWithAdmin(&fakeCabinet{}, adm)
	body := `{"name":"node-a","address":"1.2.3.4:443","sub_name":"eu","muted":true}`
	req := httptest.NewRequest("POST", "/api/admin/proxy-notification", strings.NewReader(body))
	req.Header.Set("Authorization", validAuth(t))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if len(adm.calls) != 1 || adm.calls[0].Name != "proxy-notification" ||
		adm.calls[0].Text != "1.2.3.4:443|node-a|eu|true" {
		t.Fatalf("unexpected admin calls: %+v", adm.calls)
	}

	// Non-admin (service error) maps to 403.
	adm = &fakeAdmin{err: payments.ErrNotAdmin}
	srv = newTestServerWithAdmin(&fakeCabinet{}, adm)
	req = httptest.NewRequest("POST", "/api/admin/proxy-notification", strings.NewReader(body))
	req.Header.Set("Authorization", validAuth(t))
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
	}
}

func TestHandleGift(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{nil, 200},
		{payments.ErrNotLinked, 403},
		{payments.ErrTariffUnknown, 400},
		{payments.ErrBadInput, 400},
		{payments.ErrPaymentsDisabled, 503},
	}
	for _, tc := range cases {
		cab := &fakeCabinet{giftErr: tc.err}
		srv := newTestServer(cab)
		req := httptest.NewRequest("POST", "/api/gift", strings.NewReader(`{"months":3}`))
		req.Header.Set("Authorization", validAuth(t))
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Errorf("err=%v: status = %d, want %d", tc.err, w.Code, tc.want)
		}
		if len(cab.gifted) != 1 || cab.gifted[0] != 3 {
			t.Errorf("err=%v: service calls = %v", tc.err, cab.gifted)
		}
	}
}

func TestHandleInvite(t *testing.T) {
	cab := &fakeCabinet{}
	srv := newTestServer(cab)
	req := httptest.NewRequest("POST", "/api/invite", strings.NewReader(`{"username":"newbie"}`))
	req.Header.Set("Authorization", validAuth(t))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if len(cab.invited) != 1 || cab.invited[0] != "newbie" {
		t.Fatalf("service calls = %v", cab.invited)
	}

	// Invalid username → 400 via ErrBadInput mapping.
	cab2 := &fakeCabinet{inviteErr: payments.ErrBadInput}
	srv = newTestServer(cab2)
	req = httptest.NewRequest("POST", "/api/invite", strings.NewReader(`{"username":"x"}`))
	req.Header.Set("Authorization", validAuth(t))
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("bad username: status = %d, want 400", w.Code)
	}
}

func TestHandleAdminProxyHealthCarriesDashboardURL(t *testing.T) {
	adm := &fakeAdmin{proxyHealth: &payments.WebProxyHealth{Configured: false, DashboardURL: "https://bot.example.com/checker"}}
	srv := newTestServerWithAdmin(&fakeCabinet{}, adm)
	req := httptest.NewRequest("GET", "/api/admin/proxy-health", nil)
	req.Header.Set("Authorization", validAuth(t))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["dashboard_url"] != "https://bot.example.com/checker" {
		t.Fatalf("dashboard_url = %v, want the configured URL", got["dashboard_url"])
	}
}

func TestHandleAdminAuth(t *testing.T) {
	adm := &fakeAdmin{err: payments.ErrNotAdmin}
	srv := newTestServerWithAdmin(&fakeCabinet{}, adm)

	// No initData at all → 401.
	req := httptest.NewRequest("GET", "/api/admin", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("unauthenticated: status = %d, want 401", w.Code)
	}

	// Valid initData but not an admin → 403.
	req = httptest.NewRequest("GET", "/api/admin", nil)
	req.Header.Set("Authorization", validAuth(t))
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatalf("non-admin: status = %d, want 403; body=%s", w.Code, w.Body.String())
	}
}

func TestHandleAdminOK(t *testing.T) {
	adm := &fakeAdmin{panel: &payments.WebAdminPanel{
		Requisites: "card 1234",
		Tariffs:    []payments.WebTariff{{Months: 3, Price: 450}},
		Requests:   []payments.WebAdminRequest{{ID: 7, Username: "alice", Months: 3}},
	}}
	srv := newTestServerWithAdmin(&fakeCabinet{}, adm)

	req := httptest.NewRequest("GET", "/api/admin", nil)
	req.Header.Set("Authorization", validAuth(t))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var got payments.WebAdminPanel
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Requisites != "card 1234" || len(got.Tariffs) != 1 || len(got.Requests) != 1 {
		t.Fatalf("unexpected payload: %+v", got)
	}
}

func TestHandleAdminStats(t *testing.T) {
	adm := &fakeAdmin{stats: &payments.WebAdminStats{
		UsersTotal: 25, UsersActive: 20, PaymentsConfirmed30d: 7, RevenueLabel: "3500₽",
	}}
	srv := newTestServerWithAdmin(&fakeCabinet{}, adm)

	req := httptest.NewRequest("GET", "/api/admin/stats", nil)
	req.Header.Set("Authorization", validAuth(t))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var got payments.WebAdminStats
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.UsersTotal != 25 || got.UsersActive != 20 || got.PaymentsConfirmed30d != 7 || got.RevenueLabel != "3500₽" {
		t.Fatalf("unexpected payload: %+v", got)
	}
	if len(adm.calls) != 1 || adm.calls[0].Name != "stats" || adm.calls[0].A != 42 {
		t.Fatalf("unexpected service calls: %+v", adm.calls)
	}
}

func TestHandleAdminPaymentsDefaultsAndFilters(t *testing.T) {
	adm := &fakeAdmin{report: &payments.WebPaymentReport{
		Days: 7, Total: 1, Page: 2, PageSize: 25, TotalPages: 2,
		Items: []payments.WebPaymentRecord{{ID: 9, Username: "alice", ProviderTxnID: "txn-9"}},
	}}
	srv := newTestServerWithAdmin(&fakeCabinet{}, adm)
	req := httptest.NewRequest(http.MethodGet, "/api/admin/payments?days=7&status=confirmed&provider=platega&q=alice&page=2", nil)
	req.Header.Set("Authorization", validAuth(t))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if len(adm.calls) != 1 || adm.calls[0].Name != "payments" || adm.calls[0].B != 2 || adm.calls[0].Text != "7:confirmed:platega:alice" {
		t.Fatalf("calls = %+v", adm.calls)
	}
	var got payments.WebPaymentReport
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Total != 1 || got.Items[0].ProviderTxnID != "txn-9" {
		t.Fatalf("payload = %+v", got)
	}
}

func TestHandleAdminPaymentsDefaultQueryAndValidation(t *testing.T) {
	adm := &fakeAdmin{report: &payments.WebPaymentReport{}}
	srv := newTestServerWithAdmin(&fakeCabinet{}, adm)
	req := httptest.NewRequest(http.MethodGet, "/api/admin/payments", nil)
	req.Header.Set("Authorization", validAuth(t))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK || len(adm.calls) != 1 || adm.calls[0].Text != "30:all:all:" || adm.calls[0].B != 1 {
		t.Fatalf("status=%d calls=%+v", w.Code, adm.calls)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/admin/payments?page=zero", nil)
	req.Header.Set("Authorization", validAuth(t))
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest || len(adm.calls) != 1 {
		t.Fatalf("invalid status=%d calls=%+v", w.Code, adm.calls)
	}
}

func TestStaticAdminStatisticsView(t *testing.T) {
	b, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		t.Fatalf("read embedded frontend: %v", err)
	}
	src := string(b)
	for _, want := range []string{
		"/api/admin/stats",
		"/api/admin/payments",
		"showAdminStats",
		"📊 Статистика",
		"Panel users",
		"conversion_rate",
		"provider_txn_id",
		"payment-chart",
		"payment-history",
		"admin-stat-value",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("frontend is missing %q", want)
		}
	}
}

func TestHandleAdminUserRoutesAuth(t *testing.T) {
	adm := &fakeAdmin{err: payments.ErrNotAdmin}
	srv := newTestServerWithAdmin(&fakeCabinet{}, adm)
	for _, path := range []string{"/api/admin/user/find", "/api/admin/user/update", "/api/admin/traffic-reset"} {
		// No initData → 401.
		req := httptest.NewRequest("POST", path, strings.NewReader(`{}`))
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		if w.Code != 401 {
			t.Fatalf("%s unauthenticated: status = %d, want 401", path, w.Code)
		}
		// Valid initData, non-admin → 403.
		req = httptest.NewRequest("POST", path, strings.NewReader(`{}`))
		req.Header.Set("Authorization", validAuth(t))
		w = httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		if w.Code != 403 {
			t.Fatalf("%s non-admin: status = %d, want 403", path, w.Code)
		}
	}
}

func TestHandleAdminFindUserPayload(t *testing.T) {
	adm := &fakeAdmin{}
	srv := newTestServerWithAdmin(&fakeCabinet{}, adm)
	req := httptest.NewRequest("POST", "/api/admin/user/find", strings.NewReader(`{"query":"alice"}`))
	req.Header.Set("Authorization", validAuth(t))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var got struct {
		User payments.WebManagedUser `json:"user"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.User.UUID != "u-1" || got.User.Username != "alice" {
		t.Fatalf("unexpected user: %+v", got.User)
	}
}

func TestHandleAdminMutations(t *testing.T) {
	adm := &fakeAdmin{}
	srv := newTestServerWithAdmin(&fakeCabinet{}, adm)

	post := func(path, body string) int {
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		req.Header.Set("Authorization", validAuth(t))
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		return w.Code
	}

	cases := []struct {
		path, body, call string
		a, b             int64
		text             string
	}{
		{"/api/admin/tariff", `{"months":3,"price":450}`, "settariff", 3, 450, ""},
		{"/api/admin/tariff/delete", `{"months":3}`, "deltariff", 3, 0, ""},
		{"/api/admin/requisites", `{"text":"card 1"}`, "setreq", 0, 0, "card 1"},
		{"/api/admin/gift/revoke", `{"id":5}`, "revoke", 5, 0, ""},
		{"/api/admin/request/confirm", `{"id":7}`, "confirm", 7, 0, ""},
		{"/api/admin/request/reject", `{"id":8}`, "reject", 8, 0, ""},
		{"/api/admin/gift-request/confirm", `{"id":9}`, "giftconfirm", 9, 0, ""},
		{"/api/admin/gift-request/reject", `{"id":10}`, "giftreject", 10, 0, ""},
		{"/api/admin/invite-request/confirm", `{"id":11}`, "inviteapprove", 11, 0, ""},
		{"/api/admin/invite-request/reject", `{"id":12}`, "invitereject", 12, 0, ""},
		{"/api/admin/broadcast", `{"message":"hello"}`, "broadcast", 42, 0, "hello"},
		{"/api/admin/squad", `{"uuid":"sq-2"}`, "setsquad", 42, 0, "sq-2"},
		{"/api/admin/traffic-reset", `{"strategy":"WEEK"}`, "settreset", 42, 0, "WEEK"},
		{"/api/admin/user/find", `{"query":"alice"}`, "finduser", 42, 0, "alice"},
		{"/api/admin/user/update", `{"uuid":"u-1"}`, "updateuser", 42, 0, "u-1"},
	}
	for i, tc := range cases {
		if code := post(tc.path, tc.body); code != 200 {
			t.Fatalf("%s: status = %d, want 200", tc.path, code)
		}
		c := adm.calls[i]
		if c.Name != tc.call || c.A != tc.a || (tc.b != 0 && c.B != tc.b) || c.Text != tc.text {
			t.Fatalf("%s: call = %+v", tc.path, c)
		}
	}

	// Malformed body → 400 without reaching the service.
	before := len(adm.calls)
	if code := post("/api/admin/tariff", `not json`); code != 400 {
		t.Fatalf("malformed: status = %d, want 400", code)
	}
	if len(adm.calls) != before {
		t.Fatal("service called on malformed body")
	}
}

func TestHandleAdminErrorMapping(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{payments.ErrNotAdmin, 403},
		{payments.ErrPaymentsDisabled, 403},
		{payments.ErrBadInput, 400},
		{payments.ErrTariffUnknown, 404},
		{payments.ErrRequestNotFound, 404},
		{payments.ErrRequestResolved, 409},
		{payments.ErrPanelCreateFailed, 502},
		{payments.ErrPanelUnavailable, 502},
	}
	for _, tc := range cases {
		srv := newTestServerWithAdmin(&fakeCabinet{}, &fakeAdmin{err: tc.err})
		req := httptest.NewRequest("POST", "/api/admin/request/confirm", strings.NewReader(`{"id":7}`))
		req.Header.Set("Authorization", validAuth(t))
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Errorf("err=%v: status = %d, want %d", tc.err, w.Code, tc.want)
		}
	}
}

func TestHandleAdminListSquads(t *testing.T) {
	adm := &fakeAdmin{}
	srv := newTestServerWithAdmin(&fakeCabinet{}, adm)

	req := httptest.NewRequest("GET", "/api/admin/squads", nil)
	req.Header.Set("Authorization", validAuth(t))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var got struct {
		Squads []payments.WebSquad `json:"squads"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Squads) != 2 || !got.Squads[0].Selected || got.Squads[1].UUID != "sq-2" {
		t.Fatalf("unexpected payload: %+v", got)
	}
}

func TestHandleAdminListUsers(t *testing.T) {
	adm := &fakeAdmin{}
	srv := newTestServerWithAdmin(&fakeCabinet{}, adm)

	// No initData → 401.
	req := httptest.NewRequest("GET", "/api/admin/users", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("unauthenticated: status = %d, want 401", w.Code)
	}

	// Valid initData, non-admin → 403.
	denied := newTestServerWithAdmin(&fakeCabinet{}, &fakeAdmin{err: payments.ErrNotAdmin})
	req = httptest.NewRequest("GET", "/api/admin/users", nil)
	req.Header.Set("Authorization", validAuth(t))
	w = httptest.NewRecorder()
	denied.Handler().ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatalf("non-admin: status = %d, want 403", w.Code)
	}

	req = httptest.NewRequest("GET", "/api/admin/users", nil)
	req.Header.Set("Authorization", validAuth(t))
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var got struct {
		Users []payments.WebUserRow `json:"users"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Users) != 1 || got.Users[0].Username != "alice" || got.Users[0].ExpireAt != "01.07.2026" {
		t.Fatalf("unexpected payload: %+v", got)
	}

	// A ?cohort= query routes to the filtered method.
	req = httptest.NewRequest("GET", "/api/admin/users?cohort=expired", nil)
	req.Header.Set("Authorization", validAuth(t))
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("cohort status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	got.Users = nil
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode cohort: %v", err)
	}
	if len(got.Users) != 1 || got.Users[0].Username != "bob" {
		t.Fatalf("unexpected cohort payload: %+v", got)
	}
	var sawCohort bool
	for _, c := range adm.calls {
		if c.Name == "listuserscohort" && c.Text == "expired" {
			sawCohort = true
		}
	}
	if !sawCohort {
		t.Fatalf("AdminListUsersByCohort not called with cohort=expired; calls=%+v", adm.calls)
	}
}

func TestHandleAdminBroadcastReturnsCounts(t *testing.T) {
	srv := newTestServerWithAdmin(&fakeCabinet{}, &fakeAdmin{})

	req := httptest.NewRequest("POST", "/api/admin/broadcast", strings.NewReader(`{"message":"hello"}`))
	req.Header.Set("Authorization", validAuth(t))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var got struct {
		OK     bool `json:"ok"`
		Sent   int  `json:"sent"`
		Failed int  `json:"failed"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.OK || got.Sent != 3 || got.Failed != 1 {
		t.Fatalf("unexpected payload: %+v", got)
	}
}

func TestHandleMeCarriesIsAdmin(t *testing.T) {
	cab := &fakeCabinet{data: &payments.WebCabinet{IsAdmin: true}}
	srv := newTestServer(cab)

	req := httptest.NewRequest("GET", "/api/me", nil)
	req.Header.Set("Authorization", validAuth(t))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["is_admin"] != true {
		t.Fatalf("is_admin missing: %v", got)
	}
}

func TestServesIndex(t *testing.T) {
	srv := newTestServer(&fakeCabinet{})
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Личный кабинет") {
		t.Fatalf("index not served: status=%d", w.Code)
	}
}

func TestHandleNotifications(t *testing.T) {
	cab := &fakeCabinet{}
	srv := newTestServer(cab)
	req := httptest.NewRequest("POST", "/api/notifications", strings.NewReader(`{"kind":"expiry","muted":true}`))
	req.Header.Set("Authorization", validAuth(t))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if len(cab.notifSet) != 1 || cab.notifSet[0] != "expiry:true" {
		t.Fatalf("service calls = %v", cab.notifSet)
	}

	// No initData → 401.
	req = httptest.NewRequest("POST", "/api/notifications", strings.NewReader(`{"kind":"expiry","muted":true}`))
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("unauthenticated: status = %d, want 401", w.Code)
	}

	// Bad kind → 400 via ErrBadInput.
	cab2 := &fakeCabinet{notifErr: payments.ErrBadInput}
	srv = newTestServer(cab2)
	req = httptest.NewRequest("POST", "/api/notifications", strings.NewReader(`{"kind":"x","muted":true}`))
	req.Header.Set("Authorization", validAuth(t))
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("bad kind: status = %d, want 400", w.Code)
	}
}

func TestHandleTrial(t *testing.T) {
	cab := &fakeCabinet{trialResult: &payments.WebTrialResult{Username: "newbie", SubscriptionURL: "https://sub/x"}}
	srv := newTestServer(cab)
	req := httptest.NewRequest("POST", "/api/trial", strings.NewReader(`{"username":"newbie"}`))
	req.Header.Set("Authorization", validAuth(t))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["subscription_url"] != "https://sub/x" {
		t.Fatalf("unexpected payload: %v", resp)
	}
	if len(cab.trialNames) != 1 || cab.trialNames[0] != "newbie" {
		t.Fatalf("service calls = %v", cab.trialNames)
	}

	// Sentinel error mapping.
	cases := []struct {
		err  error
		want int
	}{
		{payments.ErrTrialDisabled, 503},
		{payments.ErrTrialAlreadyUsed, 409},
		{payments.ErrTrialNotEligible, 409},
		{payments.ErrUsernameTaken, 409},
		{payments.ErrBadInput, 400},
	}
	for _, tc := range cases {
		c := &fakeCabinet{trialErr: tc.err}
		s := newTestServer(c)
		r := httptest.NewRequest("POST", "/api/trial", strings.NewReader(`{"username":"newbie"}`))
		r.Header.Set("Authorization", validAuth(t))
		rw := httptest.NewRecorder()
		s.Handler().ServeHTTP(rw, r)
		if rw.Code != tc.want {
			t.Errorf("err=%v: status = %d, want %d", tc.err, rw.Code, tc.want)
		}
	}
}

func TestHandleAdminTrialReferral(t *testing.T) {
	adm := &fakeAdmin{}
	srv := newTestServerWithAdmin(&fakeCabinet{}, adm)
	req := httptest.NewRequest("POST", "/api/admin/trial", strings.NewReader(`{"enabled":true,"days":5,"traffic_limit_gb":10,"hwid_device_limit":1,"squad_uuid":"trial-squad"}`))
	req.Header.Set("Authorization", validAuth(t))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("trial status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if len(adm.calls) != 1 || adm.calls[0].Name != "settrial" || adm.calls[0].B != 5 || adm.calls[0].Text != "true:10:1:trial-squad" {
		t.Fatalf("trial calls = %+v", adm.calls)
	}

	adm2 := &fakeAdmin{}
	srv2 := newTestServerWithAdmin(&fakeCabinet{}, adm2)
	req = httptest.NewRequest("POST", "/api/admin/referral", strings.NewReader(`{"enabled":true,"inviter_days":30,"invitee_days":10}`))
	req.Header.Set("Authorization", validAuth(t))
	w = httptest.NewRecorder()
	srv2.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("referral status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if len(adm2.calls) != 1 || adm2.calls[0].Name != "setreferral" || adm2.calls[0].A != 30 || adm2.calls[0].B != 10 {
		t.Fatalf("referral calls = %+v", adm2.calls)
	}

	// Non-admin service error → 403.
	adm3 := &fakeAdmin{err: payments.ErrNotAdmin}
	srv3 := newTestServerWithAdmin(&fakeCabinet{}, adm3)
	req = httptest.NewRequest("POST", "/api/admin/trial", strings.NewReader(`{"enabled":true,"days":5}`))
	req.Header.Set("Authorization", validAuth(t))
	w = httptest.NewRecorder()
	srv3.Handler().ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatalf("non-admin: status = %d, want 403", w.Code)
	}
}
