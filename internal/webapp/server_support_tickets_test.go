package webapp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Nakedjustice/remnaWake/internal/payments"
)

// --- fakes for the multi-ticket surface ---

func (f *fakeCabinet) SupportTicketsUser(_ context.Context, telegramID int64) (*payments.WebSupportTickets, error) {
	f.ticketCalls = append(f.ticketCalls, fmt.Sprintf("tickets:%d", telegramID))
	if f.ticketErr != nil {
		return nil, f.ticketErr
	}
	return &payments.WebSupportTickets{
		Tickets:     []payments.WebSupportTicket{{ID: 7, Status: "open", State: "awaiting_reply"}},
		UnreadTotal: 1,
	}, nil
}

func (f *fakeCabinet) SupportTicketThreadUser(_ context.Context, telegramID, ticketID int64) (*payments.WebSupportThread, error) {
	f.ticketCalls = append(f.ticketCalls, fmt.Sprintf("thread:%d:%d", telegramID, ticketID))
	if f.ticketErr != nil {
		return nil, f.ticketErr
	}
	return &payments.WebSupportThread{Ticket: payments.WebSupportTicket{ID: ticketID, Status: "open"}}, nil
}

func (f *fakeCabinet) SupportCreateTicket(_ context.Context, telegramID int64, subject, text string) (int64, error) {
	f.ticketCalls = append(f.ticketCalls, fmt.Sprintf("create:%d:%s:%s", telegramID, subject, text))
	if f.ticketErr != nil {
		return 0, f.ticketErr
	}
	return 11, nil
}

func (f *fakeCabinet) SupportSendUserToTicket(_ context.Context, telegramID, ticketID int64, text string) error {
	f.ticketCalls = append(f.ticketCalls, fmt.Sprintf("send:%d:%d:%s", telegramID, ticketID, text))
	return f.ticketErr
}

func (f *fakeCabinet) SupportCloseTicketUser(_ context.Context, telegramID, ticketID int64) error {
	f.ticketCalls = append(f.ticketCalls, fmt.Sprintf("close:%d:%d", telegramID, ticketID))
	return f.ticketErr
}

func (f *fakeAdmin) SupportTicketsAdmin(_ context.Context, tgID int64) ([]payments.WebSupportTicket, error) {
	f.calls = append(f.calls, adminCall{Name: "support_tickets", A: tgID})
	if f.err != nil {
		return nil, f.err
	}
	return []payments.WebSupportTicket{{ID: 9, Status: "open", UserID: 200}}, nil
}

func (f *fakeAdmin) SupportTicketThreadAdmin(_ context.Context, tgID, ticketID int64) (*payments.WebSupportThread, error) {
	f.calls = append(f.calls, adminCall{Name: "support_ticket_thread", A: tgID, B: ticketID})
	if f.err != nil {
		return nil, f.err
	}
	return &payments.WebSupportThread{Ticket: payments.WebSupportTicket{ID: ticketID, Status: "open"}}, nil
}

func (f *fakeAdmin) SupportReplyTicketAdmin(_ context.Context, tgID, ticketID int64, text string) error {
	f.calls = append(f.calls, adminCall{Name: "support_ticket_reply", A: tgID, B: ticketID, Text: text})
	return f.err
}

func (f *fakeAdmin) SupportCloseTicketAdmin(_ context.Context, tgID, ticketID int64) error {
	f.calls = append(f.calls, adminCall{Name: "support_ticket_close", A: tgID, B: ticketID})
	return f.err
}

// --- routes ---

func ticketCall(t *testing.T, srv *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	req.Header.Set("Authorization", validAuth(t))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

func TestSupportTicketUserRoutes(t *testing.T) {
	cab := &fakeCabinet{}
	srv := newTestServer(cab)

	for _, tc := range []struct{ method, path, body, want string }{
		{http.MethodGet, "/api/support/tickets", "", `"unread_total":1`},
		{http.MethodPost, "/api/support/ticket", `{"ticket_id":7}`, `"id":7`},
		{http.MethodPost, "/api/support/ticket/new", `{"subject":"Оплата","text":"не прошла"}`, `"ticket_id":11`},
		{http.MethodPost, "/api/support/ticket/send", `{"ticket_id":7,"text":"ещё"}`, `"ok":true`},
		{http.MethodPost, "/api/support/ticket/close", `{"ticket_id":7}`, `"ok":true`},
	} {
		w := ticketCall(t, srv, tc.method, tc.path, tc.body)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), tc.want) {
			t.Fatalf("%s %s: code=%d body=%s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}

	want := []string{
		"tickets:42",
		"thread:42:7",
		"create:42:Оплата:не прошла",
		"send:42:7:ещё",
		"close:42:7",
	}
	if strings.Join(cab.ticketCalls, "|") != strings.Join(want, "|") {
		t.Fatalf("service calls = %v, want %v", cab.ticketCalls, want)
	}
}

// The ticket routes are part of the support escape hatch: a registration-blocked
// identity must still be able to open and answer tickets.
func TestSupportTicketRoutesSurviveRegistrationBlock(t *testing.T) {
	cab := &fakeCabinet{registrationBlocked: true}
	srv := newTestServer(cab)

	if w := ticketCall(t, srv, http.MethodGet, "/api/me", ""); w.Code != http.StatusForbidden {
		t.Fatalf("/api/me for a blocked identity: code=%d", w.Code)
	}
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/support/tickets", ""},
		{http.MethodPost, "/api/support/ticket", `{"ticket_id":7}`},
		{http.MethodPost, "/api/support/ticket/new", `{"text":"помогите"}`},
		{http.MethodPost, "/api/support/ticket/send", `{"ticket_id":7,"text":"ещё"}`},
		{http.MethodPost, "/api/support/ticket/close", `{"ticket_id":7}`},
	} {
		if w := ticketCall(t, srv, tc.method, tc.path, tc.body); w.Code != http.StatusOK {
			t.Fatalf("%s %s for a blocked identity: code=%d body=%s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}

func TestSupportTicketUserRouteErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want int
	}{
		{payments.ErrTicketNotFound, http.StatusNotFound},
		{payments.ErrTicketClosed, http.StatusConflict},
		{payments.ErrBadInput, http.StatusBadRequest},
		{payments.ErrPaymentsDisabled, http.StatusServiceUnavailable},
	} {
		srv := newTestServer(&fakeCabinet{ticketErr: tc.err})
		for _, path := range []string{"/api/support/ticket", "/api/support/ticket/send", "/api/support/ticket/close"} {
			w := ticketCall(t, srv, http.MethodPost, path, `{"ticket_id":7,"text":"x"}`)
			if w.Code != tc.want {
				t.Fatalf("%s with %v: code=%d want=%d body=%s", path, tc.err, w.Code, tc.want, w.Body.String())
			}
		}
	}
}

func TestAdminSupportTicketRoutes(t *testing.T) {
	adm := &fakeAdmin{}
	srv := newTestServerWithAdmin(&fakeCabinet{}, adm)

	for _, tc := range []struct{ method, path, body, want string }{
		{http.MethodGet, "/api/admin/support/tickets", "", `"id":9`},
		{http.MethodPost, "/api/admin/support/ticket", `{"ticket_id":9}`, `"id":9`},
		{http.MethodPost, "/api/admin/support/ticket/reply", `{"ticket_id":9,"text":"смотрим"}`, `"ok":true`},
		{http.MethodPost, "/api/admin/support/ticket/close", `{"ticket_id":9}`, `"ok":true`},
	} {
		w := ticketCall(t, srv, tc.method, tc.path, tc.body)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), tc.want) {
			t.Fatalf("%s %s: code=%d body=%s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	want := []adminCall{
		{Name: "support_tickets", A: 42},
		{Name: "support_ticket_thread", A: 42, B: 9},
		{Name: "support_ticket_reply", A: 42, B: 9, Text: "смотрим"},
		{Name: "support_ticket_close", A: 42, B: 9},
	}
	if fmt.Sprintf("%+v", adm.calls) != fmt.Sprintf("%+v", want) {
		t.Fatalf("admin calls = %+v, want %+v", adm.calls, want)
	}
}

func TestAdminSupportTicketRouteErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want int
	}{
		{payments.ErrNotAdmin, http.StatusForbidden},
		{payments.ErrTicketNotFound, http.StatusNotFound},
		{payments.ErrTicketClosed, http.StatusConflict},
	} {
		srv := newTestServerWithAdmin(&fakeCabinet{}, &fakeAdmin{err: tc.err})
		for _, path := range []string{"/api/admin/support/ticket", "/api/admin/support/ticket/reply", "/api/admin/support/ticket/close"} {
			w := ticketCall(t, srv, http.MethodPost, path, `{"ticket_id":9,"text":"x"}`)
			if w.Code != tc.want {
				t.Fatalf("%s with %v: code=%d want=%d body=%s", path, tc.err, w.Code, tc.want, w.Body.String())
			}
		}
	}
}

func TestFrontendContainsSupportTicketScreens(t *testing.T) {
	b, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		t.Fatalf("read embedded frontend: %v", err)
	}
	src := string(b)
	for _, want := range []string{
		"/api/support/tickets",
		"/api/support/ticket/new",
		"/api/support/ticket/send",
		"/api/support/ticket/close",
		"/api/admin/support/tickets",
		"/api/admin/support/ticket/reply",
		"/api/admin/support/ticket/close",
		"showSupportTickets",
		"showSupportTicket",
		"showAdminTickets",
		"showAdminTicket",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("frontend is missing %q", want)
		}
	}
}
