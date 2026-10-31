package autoupdate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWatchtowerTrigger(t *testing.T) {
	var gotMethod, gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	wt := NewWatchtower(srv.URL, "secret-token")
	if err := wt.Trigger(context.Background()); err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	// The maintained Watchtower fork registers /v1/update as POST only, and the
	// original containrrr build accepts any method, so POST is the one verb that
	// works against both.
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/v1/update" {
		t.Fatalf("path = %q, want /v1/update", gotPath)
	}
	if gotAuth != "Bearer secret-token" {
		t.Fatalf("auth = %q, want Bearer secret-token", gotAuth)
	}
}

func TestWatchtowerTriggerNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	wt := NewWatchtower(srv.URL, "")
	if err := wt.Trigger(context.Background()); err == nil {
		t.Fatal("expected error on non-2xx response")
	}
}

// TestWatchtowerTriggerRejectsNonPOST pins the failure mode this change fixes:
// a Watchtower that only routes POST answers 405 to anything else, and that
// must surface as an error rather than a silent no-op update.
func TestWatchtowerTriggerRejectsNonPOST(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)

			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	wt := NewWatchtower(srv.URL, "")
	if err := wt.Trigger(context.Background()); err != nil {
		t.Fatalf("Trigger against a POST-only Watchtower: %v", err)
	}
}
