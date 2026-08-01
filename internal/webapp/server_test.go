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
	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

type fakeCabinet struct {
	data                *payments.WebCabinet
	renewErr            error
	renewResult         *payments.RenewResult
	renewed             []int64
	renewConfirmed      []bool
	trafficExtended     []int
	giftErr             error
	gifted              []int
	inviteErr           error
	invited             []string
	notifErr            error
	notifSet            []string // "<kind>:<muted>" per call
	trialErr            error
	trialResult         *payments.WebTrialResult
	trialNames          []string
	parityErr           error
	receipt             payments.WebReceipt
	registrationBlocked bool
	guardUsers          []tg.User
	supportHistory      []int64
	supportSent         []int64
	supportClosed       []int64
	devices             *payments.WebDevices
	deviceErr           error
	revoked             []string // "<remnawave_id>:<hwid>" per revoke call
	// ticketCalls records the multi-ticket service calls as "<op>:<args>" and
	// ticketErr is the error every one of them returns.
	ticketCalls []string
	ticketErr   error
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
	for _, want := range []string{"/api/register", "/api/gift/redeem", "/api/receipt", "/api/platega/check", "/api/admin/action-center", "localStorage.setItem(checkKey",
		"registration_blocked", "/api/admin/registration-guard/pattern", "/api/admin/registration-guard/unblock", "renderAdminRegistrationGuard",
		"refreshAdminActionCenter", "action-group", "admin-registration-guard"} {
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

func TestRegistrationGuardBlocksRegularMiniAppRoutesAndKeepsSupport(t *testing.T) {
	cab := &fakeCabinet{registrationBlocked: true}
	srv := newTestServer(cab)
	auth := validAuthUser(t, `{"id":77,"first_name":"Support","last_name":"<scam>","username":"support_team"}`)

	for _, tc := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/me", ""},
		{http.MethodPost, "/api/register", `{"query":"alice"}`},
		{http.MethodPost, "/api/renew", `{"remnawave_id":7,"months":1}`},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Authorization", auth)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `"code":"registration_blocked"`) {
			t.Fatalf("%s %s: code=%d body=%s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	if len(cab.guardUsers) != 3 {
		t.Fatalf("guard calls = %d, want 3", len(cab.guardUsers))
	}
	got := cab.guardUsers[0]
	if got.ID != 77 || got.FirstName != "Support" || got.LastName != "<scam>" || got.Username != "support_team" {
		t.Fatalf("signed identity forwarded to guard = %+v", got)
	}

	for _, tc := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/support", ""},
		{http.MethodPost, "/api/support/send", `{"text":"please review"}`},
		{http.MethodPost, "/api/support/close", `{}`},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Authorization", auth)
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("support %s %s: code=%d body=%s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	if len(cab.supportHistory) != 1 || len(cab.supportSent) != 1 || len(cab.supportClosed) != 1 {
		t.Fatalf("support calls = history:%v sent:%v closed:%v", cab.supportHistory, cab.supportSent, cab.supportClosed)
	}
}

func (f *fakeCabinet) CheckTelegramRegistration(_ context.Context, user *tg.User) bool {
	if user != nil {
		f.guardUsers = append(f.guardUsers, *user)
	}
	return f.registrationBlocked
}

func (f *fakeCabinet) CreateRenewRequest(_ context.Context, _, remnawaveID int64, _ int, _, _ string, planChangeConfirmed bool) (*payments.RenewResult, error) {
	f.renewed = append(f.renewed, remnawaveID)
	f.renewConfirmed = append(f.renewConfirmed, planChangeConfirmed)
	return f.renewResult, f.renewErr
}

func (f *fakeCabinet) CreateTrafficExtensionRequest(_ context.Context, _ int64, remnawaveID int64, trafficGB int, _ string) (*payments.RenewResult, error) {
	f.renewed = append(f.renewed, remnawaveID)
	f.trafficExtended = append(f.trafficExtended, trafficGB)
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

func (f *fakeCabinet) SupportHistoryUser(_ context.Context, telegramID int64) (*payments.WebSupport, error) {
	f.supportHistory = append(f.supportHistory, telegramID)
	return &payments.WebSupport{}, nil
}

func (f *fakeCabinet) SupportSendUser(_ context.Context, telegramID int64, _ string) error {
	f.supportSent = append(f.supportSent, telegramID)
	return nil
}

func (f *fakeCabinet) SupportCloseUser(_ context.Context, telegramID int64) error {
	f.supportClosed = append(f.supportClosed, telegramID)
	return nil
}

func (f *fakeCabinet) DeviceOverview(_ context.Context, _ int64) (*payments.WebDevices, error) {
	if f.deviceErr != nil {
		return nil, f.deviceErr
	}
	return f.devices, nil
}

func (f *fakeCabinet) RevokeDevice(_ context.Context, _, remnawaveID int64, hwid string) (*payments.WebDevices, error) {
	f.revoked = append(f.revoked, fmt.Sprintf("%d:%s", remnawaveID, hwid))
	if f.deviceErr != nil {
		return nil, f.deviceErr
	}
	return f.devices, nil
}

func TestDeviceRoutes(t *testing.T) {
	cab := &fakeCabinet{devices: &payments.WebDevices{Profiles: []payments.WebDeviceProfile{{
		RemnawaveID: 42, Username: "alice", Limit: 2, Used: 1,
		Devices: []payments.WebDevice{{HWID: "hw-phone", DeviceModel: "iPhone 15", Platform: "iOS"}},
	}}}}
	srv := newTestServer(cab)

	req := httptest.NewRequest(http.MethodGet, "/api/devices", nil)
	req.Header.Set("Authorization", validAuth(t))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"hwid":"hw-phone"`) {
		t.Fatalf("list: code=%d body=%s", w.Code, w.Body.String())
	}

	// The hwid travels in the JSON body (it never fits in callback data), and
	// the profile is named by id so the service can re-derive ownership.
	req = httptest.NewRequest(http.MethodPost, "/api/devices/revoke", strings.NewReader(`{"remnawave_id":42,"hwid":"hw-phone"}`))
	req.Header.Set("Authorization", validAuth(t))
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("revoke: code=%d body=%s", w.Code, w.Body.String())
	}
	if len(cab.revoked) != 1 || cab.revoked[0] != "42:hw-phone" {
		t.Fatalf("revoked = %v", cab.revoked)
	}
}

func TestDeviceErrorMappings(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want int
	}{
		{payments.ErrDevicesUnavailable, http.StatusServiceUnavailable},
		{payments.ErrDeviceNotFound, http.StatusNotFound},
		{payments.ErrProfileUnknown, http.StatusForbidden},
		{payments.ErrNotLinked, http.StatusForbidden},
		{payments.ErrPanelUnavailable, http.StatusBadGateway},
	} {
		srv := newTestServer(&fakeCabinet{deviceErr: tc.err})
		req := httptest.NewRequest(http.MethodPost, "/api/devices/revoke", strings.NewReader(`{"remnawave_id":42,"hwid":"hw-phone"}`))
		req.Header.Set("Authorization", validAuth(t))
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Fatalf("err=%v code=%d want=%d body=%s", tc.err, w.Code, tc.want, w.Body.String())
		}
	}
}

func TestEmbeddedFrontendHasDeviceScreen(t *testing.T) {
	srv := newTestServer(&fakeCabinet{})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	body := w.Body.String()
	for _, want := range []string{"/api/devices", "/api/devices/revoke", "showDevices", "renderDevices"} {
		if !strings.Contains(body, want) {
			t.Fatalf("frontend missing %q", want)
		}
	}
}

type adminCall struct {
	Name string
	A, B int64
	Text string
}

type fakeAdmin struct {
	panel        *payments.WebAdminPanel
	actionCenter *payments.WebAdminActionCenter
	stats        *payments.WebAdminStats
	analytics    *payments.WebOperatorAnalytics
	report       *payments.WebPaymentReport
	proxyHealth  *payments.WebProxyHealth
	err          error
	calls        []adminCall
}

func (f *fakeAdmin) AdminSendBackup(_ context.Context, tgID int64) error {
	f.calls = append(f.calls, adminCall{Name: "backup", A: tgID})
	return f.err
}

func (f *fakeAdmin) AdminSetBackupSchedule(_ context.Context, tgID int64, enabled bool, intervalDays int) error {
	f.calls = append(f.calls, adminCall{
		Name: "backup schedule", A: tgID, B: int64(intervalDays), Text: fmt.Sprintf("%t", enabled),
	})
	return f.err
}

func (f *fakeAdmin) AdminPanelData(_ context.Context, tgID int64) (*payments.WebAdminPanel, error) {
	f.calls = append(f.calls, adminCall{Name: "panel", A: tgID})
	return f.panel, f.err
}
func (f *fakeAdmin) AdminActionCenter(_ context.Context, tgID int64) (*payments.WebAdminActionCenter, error) {
	f.calls = append(f.calls, adminCall{Name: "action-center", A: tgID})
	return f.actionCenter, f.err
}
func (f *fakeAdmin) AdminStatsData(_ context.Context, tgID int64) (*payments.WebAdminStats, error) {
	f.calls = append(f.calls, adminCall{Name: "stats", A: tgID})
	return f.stats, f.err
}
func (f *fakeAdmin) AdminOperatorAnalytics(_ context.Context, tgID int64, days int) (*payments.WebOperatorAnalytics, error) {
	f.calls = append(f.calls, adminCall{Name: "analytics", A: tgID, B: int64(days)})
	return f.analytics, f.err
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
	f.calls = append(f.calls, adminCall{Name: "payments", A: tgID, B: int64(filter.Page), Text: fmt.Sprintf("%d:%s:%s:%s:%s", filter.Days, filter.Status, filter.Provider, filter.Kind, filter.Query)})
	return f.report, f.err
}
func (f *fakeAdmin) AdminDeletePaymentRequest(_ context.Context, tgID, id int64) error {
	f.calls = append(f.calls, adminCall{Name: "delete payment", A: tgID, B: id})
	return f.err
}
func (f *fakeAdmin) AdminDeleteGiftCode(_ context.Context, tgID, id int64) error {
	f.calls = append(f.calls, adminCall{Name: "delete gift", A: tgID, B: id})
	return f.err
}
func (f *fakeAdmin) AdminDeleteInviteRequest(_ context.Context, tgID, id int64) error {
	f.calls = append(f.calls, adminCall{Name: "delete invite", A: tgID, B: id})
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
func (f *fakeAdmin) AdminSetTariff(_ context.Context, tgID int64, plan string, months, price int) error {
	f.calls = append(f.calls, adminCall{Name: "settariff", A: int64(months), B: int64(price), Text: plan})
	return f.err
}
func (f *fakeAdmin) AdminDeleteTariff(_ context.Context, tgID int64, plan string, months int) error {
	f.calls = append(f.calls, adminCall{Name: "deltariff", A: int64(months), Text: plan})
	return f.err
}
func (f *fakeAdmin) AdminSetTrafficExtensionOption(_ context.Context, tgID int64, trafficGB, price int) error {
	f.calls = append(f.calls, adminCall{Name: "settraffic", A: int64(trafficGB), B: int64(price)})
	return f.err
}
func (f *fakeAdmin) AdminDeleteTrafficExtensionOption(_ context.Context, tgID int64, trafficGB int) error {
	f.calls = append(f.calls, adminCall{Name: "deltraffic", A: int64(trafficGB)})
	return f.err
}
func (f *fakeAdmin) AdminSetPlan(_ context.Context, tgID int64, in payments.WebAdminPlan) error {
	f.calls = append(f.calls, adminCall{Name: "setplan", A: tgID, Text: in.Code})
	return f.err
}
func (f *fakeAdmin) AdminDeletePlan(_ context.Context, tgID int64, code string) error {
	f.calls = append(f.calls, adminCall{Name: "delplan", A: tgID, Text: code})
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
func (f *fakeAdmin) AdminAddRegistrationGuardPattern(_ context.Context, tgID int64, pattern string) error {
	f.calls = append(f.calls, adminCall{Name: "addguardpattern", A: tgID, Text: pattern})
	return f.err
}
func (f *fakeAdmin) AdminDeleteRegistrationGuardPattern(_ context.Context, tgID int64, pattern string) error {
	f.calls = append(f.calls, adminCall{Name: "deleteguardpattern", A: tgID, Text: pattern})
	return f.err
}
func (f *fakeAdmin) AdminUnblockRegistration(_ context.Context, tgID, blockedTGID int64) error {
	f.calls = append(f.calls, adminCall{Name: "unblockregistration", A: tgID, B: blockedTGID})
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
		{Username: "alice", Status: "ACTIVE", ExpireAt: "01.07.2026"},
	}, nil
}
func (f *fakeAdmin) AdminListUsersByCohort(_ context.Context, tgID int64, cohort string) ([]payments.WebUserRow, error) {
	f.calls = append(f.calls, adminCall{Name: "listuserscohort", A: tgID, Text: cohort})
	if f.err != nil {
		return nil, f.err
	}
	return []payments.WebUserRow{
		{Username: "bob", Status: "EXPIRED", ExpireAt: "01.06.2026"},
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
	return &payments.WebManagedUser{Username: query, Status: "ACTIVE"}, nil
}
func (f *fakeAdmin) AdminUpdateUser(_ context.Context, tgID int64, req payments.WebUserUpdate) error {
	f.calls = append(f.calls, adminCall{Name: "updateuser", A: tgID, B: req.RemnawaveID})
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
	return validAuthUser(t, `{"id":42}`)
}

func validAuthUser(t *testing.T, user string) string {
	return "tma " + signInitData(t, map[string]string{
		"auth_date": "1699999000",
		"user":      user,
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

func TestHandleRenewPlanChangePreview(t *testing.T) {
	cab := &fakeCabinet{renewResult: &payments.RenewResult{
		Status: payments.RenewStatusPlanChangePreview,
		PlanChange: &payments.PlanChangePreview{
			Kind:            payments.PlanChangeUpgradeRecalculation,
			CurrentPlan:     "Basic",
			TargetPlan:      "Standard",
			CurrentExpireAt: "01.09.2026",
			NewExpireAt:     "24.07.2026",
		},
	}}
	srv := newTestServer(cab)
	req := httptest.NewRequest("POST", "/api/renew", strings.NewReader(`{"remnawave_id":7,"months":1,"plan_change_confirmed":true}`))
	req.Header.Set("Authorization", validAuth(t))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if len(cab.renewConfirmed) != 1 || !cab.renewConfirmed[0] {
		t.Fatalf("plan_change_confirmed was not forwarded: %+v", cab.renewConfirmed)
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	change, _ := got["plan_change"].(map[string]any)
	if got["status"] != payments.RenewStatusPlanChangePreview || change["new_expire_at"] != "24.07.2026" {
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

func TestHandleAdminBackup(t *testing.T) {
	adm := &fakeAdmin{}
	srv := newTestServerWithAdmin(&fakeCabinet{}, adm)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/backup", nil)
	req.Header.Set("Authorization", validAuth(t))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if len(adm.calls) != 1 || adm.calls[0].Name != "backup" || adm.calls[0].A != 42 {
		t.Fatalf("unexpected admin calls: %+v", adm.calls)
	}

	unauthenticated := httptest.NewRequest(http.MethodPost, "/api/admin/backup", nil)
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, unauthenticated)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("missing initData: status=%d want=%d body=%s", w.Code, http.StatusUnauthorized, w.Body.String())
	}

	for _, tc := range []struct {
		err  error
		want int
	}{
		{payments.ErrNotAdmin, http.StatusForbidden},
		{payments.ErrBackupDryRun, http.StatusConflict},
		{payments.ErrBackupTooLarge, http.StatusRequestEntityTooLarge},
		{payments.ErrBackupCreate, http.StatusInternalServerError},
		{payments.ErrBackupDelivery, http.StatusInternalServerError},
	} {
		srv := newTestServerWithAdmin(&fakeCabinet{}, &fakeAdmin{err: tc.err})
		req := httptest.NewRequest(http.MethodPost, "/api/admin/backup", nil)
		req.Header.Set("Authorization", validAuth(t))
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Fatalf("err=%v: status=%d want=%d body=%s", tc.err, w.Code, tc.want, w.Body.String())
		}
	}
}

func TestHandleAdminSetBackupSchedule(t *testing.T) {
	adm := &fakeAdmin{}
	srv := newTestServerWithAdmin(&fakeCabinet{}, adm)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/backup/schedule",
		strings.NewReader(`{"enabled":true,"interval_days":7}`))
	req.Header.Set("Authorization", validAuth(t))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if len(adm.calls) != 1 || adm.calls[0].Name != "backup schedule" ||
		adm.calls[0].A != 42 || adm.calls[0].B != 7 || adm.calls[0].Text != "true" {
		t.Fatalf("unexpected admin calls: %+v", adm.calls)
	}

	unauthenticated := httptest.NewRequest(http.MethodPost, "/api/admin/backup/schedule",
		strings.NewReader(`{"enabled":true,"interval_days":7}`))
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, unauthenticated)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("missing initData: status=%d want=%d body=%s", w.Code, http.StatusUnauthorized, w.Body.String())
	}

	rejecting := newTestServerWithAdmin(&fakeCabinet{}, &fakeAdmin{err: payments.ErrBadInput})
	bad := httptest.NewRequest(http.MethodPost, "/api/admin/backup/schedule",
		strings.NewReader(`{"enabled":true,"interval_days":0}`))
	bad.Header.Set("Authorization", validAuth(t))
	w = httptest.NewRecorder()
	rejecting.Handler().ServeHTTP(w, bad)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("out-of-range interval: status=%d want=%d body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
}

// An approval that trips the profile-name contract has already rejected the
// request and notified the user, so the admin surface must report a resolved
// request — not the generic "invalid input" the wrapped ErrBadInput would give.
func TestAdminApprovalInvalidUsernameReportsResolved(t *testing.T) {
	srv := newTestServerWithAdmin(&fakeCabinet{}, &fakeAdmin{err: payments.ErrInvalidUsername})
	req := httptest.NewRequest(http.MethodPost, "/api/admin/trial-request/confirm", strings.NewReader(`{"id":1}`))
	req.Header.Set("Authorization", validAuth(t))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusConflict, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["code"] != "invalid_username" {
		t.Fatalf("response code = %v, want invalid_username; body=%v", body["code"], body)
	}
}

func TestHandleAdminRegistrationGuardMutations(t *testing.T) {
	adm := &fakeAdmin{}
	srv := newTestServerWithAdmin(&fakeCabinet{}, adm)
	for _, tc := range []struct {
		path string
		body string
		name string
	}{
		{"/api/admin/registration-guard/pattern", `{"pattern":"brand helper"}`, "addguardpattern"},
		{"/api/admin/registration-guard/pattern/delete", `{"pattern":"brand helper"}`, "deleteguardpattern"},
		{"/api/admin/registration-guard/unblock", `{"telegram_id":77}`, "unblockregistration"},
	} {
		req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Authorization", validAuth(t))
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status=%d body=%s", tc.path, w.Code, w.Body.String())
		}
		call := adm.calls[len(adm.calls)-1]
		if call.Name != tc.name || call.A != 42 {
			t.Fatalf("%s: call=%+v", tc.path, call)
		}
	}
	if got := adm.calls[2].B; got != 77 {
		t.Fatalf("unblock target = %d, want 77", got)
	}

	denied := newTestServerWithAdmin(&fakeCabinet{}, &fakeAdmin{err: payments.ErrNotAdmin})
	req := httptest.NewRequest(http.MethodPost, "/api/admin/registration-guard/pattern", strings.NewReader(`{"pattern":"brand helper"}`))
	req.Header.Set("Authorization", validAuth(t))
	w := httptest.NewRecorder()
	denied.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-admin: status=%d body=%s", w.Code, w.Body.String())
	}
	for _, tc := range []struct {
		err  error
		want int
	}{
		{payments.ErrRegistrationGuardPatternExists, http.StatusConflict},
		{payments.ErrBadInput, http.StatusBadRequest},
	} {
		srv := newTestServerWithAdmin(&fakeCabinet{}, &fakeAdmin{err: tc.err})
		req := httptest.NewRequest(http.MethodPost, "/api/admin/registration-guard/pattern", strings.NewReader(`{"pattern":"brand helper"}`))
		req.Header.Set("Authorization", validAuth(t))
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Fatalf("err=%v: status=%d want=%d body=%s", tc.err, w.Code, tc.want, w.Body.String())
		}
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

func TestHandleAdminActionCenterAuth(t *testing.T) {
	srv := newTestServerWithAdmin(&fakeCabinet{}, &fakeAdmin{err: payments.ErrNotAdmin})
	for _, auth := range []string{"", "tma garbage"} {
		req := httptest.NewRequest(http.MethodGet, "/api/admin/action-center", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("auth %q: status = %d, want 401", auth, w.Code)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/action-center", nil)
	req.Header.Set("Authorization", validAuth(t))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("non-admin: status = %d, want 403; body=%s", w.Code, w.Body.String())
	}
}

func TestHandleAdminActionCenterOK(t *testing.T) {
	adm := &fakeAdmin{actionCenter: &payments.WebAdminActionCenter{
		GeneratedAt: "2026-07-03T12:00:00Z",
		Summary:     payments.WebAdminActionSummary{Total: 1, Affected: 2, Warning: 1, NeedsAction: 1},
		Items: []payments.WebAdminActionItem{{
			ID:       "pending_payments",
			Group:    "needs_action",
			Category: "payments",
			Severity: "warning",
			Title:    "Заявки на оплату ждут решения",
			Detail:   "Откройте список заявок на оплату.",
			Count:    2,
			Target:   "payment_requests",
			OldestAt: "2026-07-03T10:00:00Z",
		}},
		Sources: []payments.WebAdminActionSource{
			{Name: "local_store", Status: "ok"},
			{Name: "remnawave", Status: "degraded", Error: "panel down"},
		},
	}}
	srv := newTestServerWithAdmin(&fakeCabinet{}, adm)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/action-center", nil)
	req.Header.Set("Authorization", validAuth(t))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var got payments.WebAdminActionCenter
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Summary.Total != 1 || got.Summary.Affected != 2 || got.Summary.NeedsAction != 1 || len(got.Items) != 1 || got.Items[0].Target != "payment_requests" {
		t.Fatalf("unexpected action-center payload: %+v", got)
	}
	if got.Items[0].Group != "needs_action" || got.Items[0].OldestAt != "2026-07-03T10:00:00Z" {
		t.Fatalf("missing action-center group/timing fields: %+v", got.Items[0])
	}
	if len(got.Sources) != 2 || got.Sources[1].Status != "degraded" || got.Sources[1].Error == "" {
		t.Fatalf("missing degraded source metadata: %+v", got.Sources)
	}
	if len(adm.calls) != 1 || adm.calls[0].Name != "action-center" || adm.calls[0].A != 42 {
		t.Fatalf("unexpected service calls: %+v", adm.calls)
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
	req := httptest.NewRequest(http.MethodGet, "/api/admin/payments?days=7&status=confirmed&provider=platega&kind=gift&q=alice&page=2", nil)
	req.Header.Set("Authorization", validAuth(t))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if len(adm.calls) != 1 || adm.calls[0].Name != "payments" || adm.calls[0].B != 2 || adm.calls[0].Text != "7:confirmed:platega:gift:alice" {
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

func TestHandleAdminAnalyticsDefaultsValidationAndAuth(t *testing.T) {
	adm := &fakeAdmin{analytics: &payments.WebOperatorAnalytics{
		RangeDays: 30,
		Revenue:   payments.WebRevenueAnalytics{TotalRevenue: 500, TotalRevenueLabel: "500₽"},
	}}
	srv := newTestServerWithAdmin(&fakeCabinet{}, adm)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/analytics", nil)
	req.Header.Set("Authorization", validAuth(t))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("default status = %d body=%s", w.Code, w.Body.String())
	}
	if len(adm.calls) != 1 || adm.calls[0].Name != "analytics" || adm.calls[0].A != 42 || adm.calls[0].B != 30 {
		t.Fatalf("calls = %+v", adm.calls)
	}
	var got payments.WebOperatorAnalytics
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.RangeDays != 30 || got.Revenue.TotalRevenueLabel != "500₽" {
		t.Fatalf("payload = %+v", got)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/admin/analytics?days=7", nil)
	req.Header.Set("Authorization", validAuth(t))
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK || adm.calls[1].B != 7 {
		t.Fatalf("days status=%d calls=%+v", w.Code, adm.calls)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/admin/analytics?days=14", nil)
	req.Header.Set("Authorization", validAuth(t))
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest || len(adm.calls) != 2 {
		t.Fatalf("invalid status=%d calls=%+v", w.Code, adm.calls)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/admin/analytics", nil)
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized || len(adm.calls) != 2 {
		t.Fatalf("unauthorized status=%d calls=%+v", w.Code, adm.calls)
	}
}

func TestHandleAdminPaymentsDefaultQueryAndValidation(t *testing.T) {
	adm := &fakeAdmin{report: &payments.WebPaymentReport{}}
	srv := newTestServerWithAdmin(&fakeCabinet{}, adm)
	req := httptest.NewRequest(http.MethodGet, "/api/admin/payments", nil)
	req.Header.Set("Authorization", validAuth(t))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK || len(adm.calls) != 1 || adm.calls[0].Text != "30:all:all:all:" || adm.calls[0].B != 1 {
		t.Fatalf("status=%d calls=%+v", w.Code, adm.calls)
	}

	// An unknown kind is rejected before reaching the service.
	req = httptest.NewRequest(http.MethodGet, "/api/admin/payments?kind=bogus", nil)
	req.Header.Set("Authorization", validAuth(t))
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest || len(adm.calls) != 1 {
		t.Fatalf("bad kind status=%d calls=%+v", w.Code, adm.calls)
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
		"/api/admin/analytics",
		"/api/admin/gift/delete",
		"/api/admin/invite/delete",
		"showAdminStats",
		"renderOperatorAnalytics",
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
	if got.User.Username != "alice" {
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
		{"/api/admin/payment/delete", `{"id":13}`, "delete payment", 42, 13, ""},
		{"/api/admin/gift/delete", `{"id":14}`, "delete gift", 42, 14, ""},
		{"/api/admin/invite/delete", `{"id":15}`, "delete invite", 42, 15, ""},
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
		{"/api/admin/user/update", `{"remnawave_id":1}`, "updateuser", 42, 1, ""},
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
	for _, want := range []string{"Резервная копия", "/api/admin/backup", "Создаю архив…", "Backup sent to your bot chat."} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatalf("index missing manual-backup UI marker %q", want)
		}
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

	// Username validation has a stable machine-readable code so the mini app
	// can show the exact profile-name rules instead of a generic input error.
	invalid := newTestServer(&fakeCabinet{trialErr: payments.ErrInvalidUsername})
	invalidReq := httptest.NewRequest("POST", "/api/trial", strings.NewReader(`{"username":"профиль"}`))
	invalidReq.Header.Set("Authorization", validAuth(t))
	invalidResp := httptest.NewRecorder()
	invalid.Handler().ServeHTTP(invalidResp, invalidReq)
	if invalidResp.Code != http.StatusBadRequest {
		t.Fatalf("invalid username: status=%d want=%d body=%s", invalidResp.Code, http.StatusBadRequest, invalidResp.Body.String())
	}
	var invalidBody map[string]any
	if err := json.Unmarshal(invalidResp.Body.Bytes(), &invalidBody); err != nil {
		t.Fatalf("decode invalid username response: %v", err)
	}
	if invalidBody["code"] != "invalid_username" {
		t.Fatalf("invalid username response code=%v, want invalid_username; body=%v", invalidBody["code"], invalidBody)
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
