package webapp

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/payments"
)

type fakeCabinet struct {
	data      *payments.WebCabinet
	renewErr  error
	renewed   []int64
	giftErr   error
	gifted    []int
	inviteErr error
	invited   []string
}

func (f *fakeCabinet) CabinetData(_ context.Context, _ int64) (*payments.WebCabinet, error) {
	return f.data, nil
}

func (f *fakeCabinet) CreateRenewRequest(_ context.Context, _, remnawaveID int64, _ int) error {
	f.renewed = append(f.renewed, remnawaveID)
	return f.renewErr
}

func (f *fakeCabinet) CreateGiftRequest(_ context.Context, _ int64, months int) error {
	f.gifted = append(f.gifted, months)
	return f.giftErr
}

func (f *fakeCabinet) CreateInviteRequest(_ context.Context, _ int64, username string) error {
	f.invited = append(f.invited, username)
	return f.inviteErr
}

type adminCall struct {
	Name string
	A, B int64
	Text string
}

type fakeAdmin struct {
	panel *payments.WebAdminPanel
	err   error
	calls []adminCall
}

func (f *fakeAdmin) AdminPanelData(_ context.Context, tgID int64) (*payments.WebAdminPanel, error) {
	f.calls = append(f.calls, adminCall{Name: "panel", A: tgID})
	return f.panel, f.err
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
func (f *fakeAdmin) AdminBroadcast(_ context.Context, tgID int64, text string) (*payments.WebBroadcastResult, error) {
	f.calls = append(f.calls, adminCall{Name: "broadcast", A: tgID, Text: text})
	if f.err != nil {
		return nil, f.err
	}
	return &payments.WebBroadcastResult{Sent: 3, Failed: 1}, nil
}

func newTestServer(cab *fakeCabinet) *Server {
	return newTestServerWithAdmin(cab, &fakeAdmin{})
}

func newTestServerWithAdmin(cab *fakeCabinet, adm *fakeAdmin) *Server {
	s := NewServer(cab, adm, testToken, slog.New(slog.DiscardHandler))
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
		{"/api/admin/broadcast", `{"message":"hello"}`, "broadcast", 42, 0, "hello"},
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
