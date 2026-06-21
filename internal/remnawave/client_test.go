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
		_, _ = w.Write([]byte(`{"response":{"uuid":"u-1","id":7,"username":"alice b","status":"ACTIVE","expireAt":"2026-07-01T00:00:00Z","telegramId":555,"hwidDeviceLimit":4,"trafficLimitBytes":1073741824,"trafficLimitStrategy":"WEEK","activeInternalSquads":[{"uuid":"sq-1","name":"Default-Squad"}],"userTraffic":{"usedTrafficBytes":536870912,"lifetimeUsedTrafficBytes":2147483648}}}`))
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
	if u.HwidDeviceLimit == nil || *u.HwidDeviceLimit != 4 {
		t.Fatalf("hwidDeviceLimit = %v, want 4", u.HwidDeviceLimit)
	}
	if u.TrafficLimitBytes != 1073741824 || u.TrafficLimitStrategy != "WEEK" {
		t.Fatalf("traffic limit = %d / %q", u.TrafficLimitBytes, u.TrafficLimitStrategy)
	}
	// Used traffic lives under the nested "userTraffic" object, not at the top level.
	if u.UserTraffic.UsedTrafficBytes != 536870912 || u.UserTraffic.LifetimeUsedTrafficBytes != 2147483648 {
		t.Fatalf("user traffic = %d / %d", u.UserTraffic.UsedTrafficBytes, u.UserTraffic.LifetimeUsedTrafficBytes)
	}
	if len(u.ActiveInternalSquads) != 1 || u.ActiveInternalSquads[0].UUID != "sq-1" {
		t.Fatalf("activeInternalSquads = %+v", u.ActiveInternalSquads)
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

func TestGetInternalSquads(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/api/internal-squads" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Fatalf("auth = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":{"total":2,"internalSquads":[{"uuid":"sq-1","name":"Default-Squad"},{"uuid":"sq-2","name":"Premium"}]}}`))
	}))
	defer server.Close()

	c, _ := NewClient(server.URL, "tok", time.Second)
	squads, err := c.GetInternalSquads(context.Background())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(squads) != 2 || squads[0].UUID != "sq-1" || squads[0].Name != "Default-Squad" || squads[1].Name != "Premium" {
		t.Fatalf("squads wrong: %+v", squads)
	}
}

func TestCreateUserSendsActiveInternalSquads(t *testing.T) {
	expireAt := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	var gotBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/users" {
			t.Fatalf("got %s %s, want POST /api/users", r.Method, r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if err := json.Unmarshal(body, &gotBody); err != nil {
			t.Fatalf("decode body: %v (body=%s)", err, body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":{"uuid":"u-new","username":"alice"}}`))
	}))
	defer server.Close()

	c, _ := NewClient(server.URL, "tok", time.Second)
	if _, err := c.CreateUser(context.Background(), "alice", expireAt, []string{"sq-1"}, "WEEK"); err != nil {
		t.Fatalf("err: %v", err)
	}
	squads, ok := gotBody["activeInternalSquads"].([]interface{})
	if !ok || len(squads) != 1 || squads[0] != "sq-1" {
		t.Fatalf("body activeInternalSquads = %v, want [sq-1]", gotBody["activeInternalSquads"])
	}
	if got := gotBody["trafficLimitStrategy"]; got != "WEEK" {
		t.Fatalf("body trafficLimitStrategy = %v, want WEEK", got)
	}

	// Without squads the field must be absent entirely, not an empty array.
	// An empty strategy falls back to NO_RESET.
	gotBody = nil
	if _, err := c.CreateUser(context.Background(), "alice", expireAt, nil, ""); err != nil {
		t.Fatalf("err: %v", err)
	}
	if _, present := gotBody["activeInternalSquads"]; present {
		t.Fatalf("activeInternalSquads should be omitted when empty, body=%v", gotBody)
	}
	if got := gotBody["trafficLimitStrategy"]; got != "NO_RESET" {
		t.Fatalf("body trafficLimitStrategy = %v, want NO_RESET", got)
	}
}

func TestUpdateUserSendsOnlySetFields(t *testing.T) {
	const uuid = "u-42"
	var gotBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/api/users" {
			t.Fatalf("got %s %s, want PATCH /api/users", r.Method, r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if err := json.Unmarshal(body, &gotBody); err != nil {
			t.Fatalf("decode body: %v (body=%s)", err, body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":{"uuid":"u-42"}}`))
	}))
	defer server.Close()

	c, _ := NewClient(server.URL, "tok", time.Second)

	// Set traffic (zero = unlimited, must still be sent), strategy and status.
	var bytesLimit int64 = 0
	status := "DISABLED"
	strategy := "MONTH"
	if err := c.UpdateUser(context.Background(), uuid, UserPatch{
		TrafficLimitBytes:    &bytesLimit,
		TrafficLimitStrategy: &strategy,
		Status:               &status,
	}); err != nil {
		t.Fatalf("err: %v", err)
	}
	if gotBody["uuid"] != uuid {
		t.Fatalf("uuid = %v, want %v", gotBody["uuid"], uuid)
	}
	if v, ok := gotBody["trafficLimitBytes"]; !ok || v.(float64) != 0 {
		t.Fatalf("trafficLimitBytes = %v (present=%v), want 0", v, ok)
	}
	if gotBody["trafficLimitStrategy"] != "MONTH" {
		t.Fatalf("trafficLimitStrategy = %v, want MONTH", gotBody["trafficLimitStrategy"])
	}
	if gotBody["status"] != "DISABLED" {
		t.Fatalf("status = %v, want DISABLED", gotBody["status"])
	}
	// Omitted fields must be absent from the body.
	for _, k := range []string{"expireAt", "hwidDeviceLimit", "activeInternalSquads"} {
		if _, present := gotBody[k]; present {
			t.Fatalf("field %q should be omitted, body=%v", k, gotBody)
		}
	}

	// Squads + HWID + expiry path.
	gotBody = nil
	hwid := 3
	expire := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	squads := []string{"sq-1", "sq-2"}
	if err := c.UpdateUser(context.Background(), uuid, UserPatch{
		ExpireAt:             &expire,
		HwidDeviceLimit:      &hwid,
		ActiveInternalSquads: &squads,
	}); err != nil {
		t.Fatalf("err: %v", err)
	}
	if gotBody["expireAt"] != expire.UTC().Format(time.RFC3339) {
		t.Fatalf("expireAt = %v", gotBody["expireAt"])
	}
	if v := gotBody["hwidDeviceLimit"]; v == nil || v.(float64) != 3 {
		t.Fatalf("hwidDeviceLimit = %v, want 3", v)
	}
	sq, ok := gotBody["activeInternalSquads"].([]interface{})
	if !ok || len(sq) != 2 || sq[0] != "sq-1" {
		t.Fatalf("activeInternalSquads = %v", gotBody["activeInternalSquads"])
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
