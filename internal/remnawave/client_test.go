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
