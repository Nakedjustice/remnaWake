package autoupdate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWatchtowerTrigger(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	wt := NewWatchtower(srv.URL, "secret-token")
	if err := wt.Trigger(context.Background()); err != nil {
		t.Fatalf("Trigger: %v", err)
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
