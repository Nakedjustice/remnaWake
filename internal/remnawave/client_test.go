package remnawave

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGetUsersUsesBearerAPIToken(t *testing.T) {
	const token = "test-api-token"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/users" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got, want := r.Header.Get("Authorization"), "Bearer "+token; got != want {
			t.Fatalf("Authorization header = %q, want %q", got, want)
		}
		if got, want := r.Header.Get("Accept"), "application/json"; got != want {
			t.Fatalf("Accept header = %q, want %q", got, want)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":{"users":[],"total":0}}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, token, time.Second)
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	users, err := client.GetUsers(context.Background())
	if err != nil {
		t.Fatalf("GetUsers returned error: %v", err)
	}
	if len(users) != 0 {
		t.Fatalf("len(users) = %d, want 0", len(users))
	}
}

func TestExtendSubscriptionByUUIDSendsUUIDInBody(t *testing.T) {
	const (
		token = "test-api-token"
		uuid  = "b1a2c3d4-0000-1111-2222-333344445555"
	)
	expireAt := time.Date(2026, 6, 6, 9, 0, 0, 0, time.UTC)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Fatalf("method = %s, want PATCH", r.Method)
		}
		if r.URL.Path != "/api/users" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got, want := r.Header.Get("Authorization"), "Bearer "+token; got != want {
			t.Fatalf("Authorization header = %q, want %q", got, want)
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		var payload map[string]interface{}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("decode body: %v (body=%s)", err, body)
		}
		if got, want := payload["uuid"], uuid; got != want {
			t.Fatalf("body uuid = %v, want %v", got, want)
		}
		if got, want := payload["expireAt"], expireAt.Format(time.RFC3339); got != want {
			t.Fatalf("body expireAt = %v, want %v", got, want)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":{"uuid":"` + uuid + `"}}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, token, time.Second)
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	if err := client.ExtendSubscriptionByUUID(context.Background(), uuid, expireAt); err != nil {
		t.Fatalf("ExtendSubscriptionByUUID returned error: %v", err)
	}
}

func TestSetTelegramIDSendsUUIDAndTelegramID(t *testing.T) {
	const (
		token = "test-api-token"
		uuid  = "b1a2c3d4-0000-1111-2222-333344445555"
	)
	var telegramID int64 = 424242

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Fatalf("method = %s, want PATCH", r.Method)
		}
		if r.URL.Path != "/api/users" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got, want := r.Header.Get("Authorization"), "Bearer "+token; got != want {
			t.Fatalf("Authorization header = %q, want %q", got, want)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		var payload map[string]interface{}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("decode body: %v (body=%s)", err, body)
		}
		if got, want := payload["uuid"], uuid; got != want {
			t.Fatalf("body uuid = %v, want %v", got, want)
		}
		// JSON numbers decode to float64.
		if got, want := payload["telegramId"], float64(telegramID); got != want {
			t.Fatalf("body telegramId = %v, want %v", got, want)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":{"uuid":"` + uuid + `"}}`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, token, time.Second)
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}

	if err := client.SetTelegramID(context.Background(), uuid, telegramID); err != nil {
		t.Fatalf("SetTelegramID returned error: %v", err)
	}
}

func TestGetUserByUsername(t *testing.T) {
	const token = "tok"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.EscapedPath() != "/api/users/by-username/alice%20b" {
			t.Fatalf("path = %s", r.URL.EscapedPath())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Fatalf("auth = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":{"uuid":"u-1","id":7,"username":"alice b","status":"ACTIVE","expireAt":"2026-07-01T00:00:00Z","telegramId":555}}`))
	}))
	defer server.Close()

	c, _ := NewClient(server.URL, token, time.Second)
	u, err := c.GetUserByUsername(context.Background(), "alice b")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if u == nil || u.UUID != "u-1" || u.ID != 7 || u.TelegramID == nil || *u.TelegramID != 555 {
		t.Fatalf("user wrong: %+v", u)
	}
}

func TestGetUserByUsernameNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	c, _ := NewClient(server.URL, "tok", time.Second)
	u, err := c.GetUserByUsername(context.Background(), "ghost")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if u != nil {
		t.Fatalf("want nil, got %+v", u)
	}
}

func TestGetUserByShortUUID(t *testing.T) {
	const token = "tok"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.EscapedPath() != "/api/users/by-short-uuid/abc123XY" {
			t.Fatalf("path = %s", r.URL.EscapedPath())
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Fatalf("auth = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":{"uuid":"u-1","id":7,"shortUuid":"abc123XY","username":"alice","status":"ACTIVE","expireAt":"2026-07-01T00:00:00Z"}}`))
	}))
	defer server.Close()

	c, _ := NewClient(server.URL, token, time.Second)
	u, err := c.GetUserByShortUUID(context.Background(), "abc123XY")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if u == nil || u.UUID != "u-1" || u.ShortUUID != "abc123XY" || u.Username != "alice" {
		t.Fatalf("user wrong: %+v", u)
	}
}

func TestGetUserByShortUUIDNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	c, _ := NewClient(server.URL, "tok", time.Second)
	u, err := c.GetUserByShortUUID(context.Background(), "ghost123")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if u != nil {
		t.Fatalf("want nil, got %+v", u)
	}
}

func TestGetUserByTelegramID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/users/by-telegram-id/123" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":[{"uuid":"u-a","id":1,"username":"a","status":"ACTIVE","expireAt":"2026-07-01T00:00:00Z","telegramId":123},{"uuid":"u-b","id":2,"username":"b","status":"ACTIVE","expireAt":"2026-07-01T00:00:00Z","telegramId":123}]}`))
	}))
	defer server.Close()

	c, _ := NewClient(server.URL, "tok", time.Second)
	us, err := c.GetUserByTelegramID(context.Background(), 123)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(us) != 2 || us[0].Username != "a" || us[1].Username != "b" {
		t.Fatalf("users wrong: %+v", us)
	}
}

func TestGetUserByTelegramIDNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	c, _ := NewClient(server.URL, "tok", time.Second)
	us, err := c.GetUserByTelegramID(context.Background(), 999)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(us) != 0 {
		t.Fatalf("want empty, got %+v", us)
	}
}
