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
	data     *payments.WebCabinet
	renewErr error
	renewed  []int64
}

func (f *fakeCabinet) CabinetData(_ context.Context, _ int64) (*payments.WebCabinet, error) {
	return f.data, nil
}

func (f *fakeCabinet) CreateRenewRequest(_ context.Context, _, remnawaveID int64, _ int) error {
	f.renewed = append(f.renewed, remnawaveID)
	return f.renewErr
}

func newTestServer(cab *fakeCabinet) *Server {
	s := NewServer(cab, testToken, slog.New(slog.DiscardHandler))
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

func TestServesIndex(t *testing.T) {
	srv := newTestServer(&fakeCabinet{})
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Личный кабинет") {
		t.Fatalf("index not served: status=%d", w.Code)
	}
}
