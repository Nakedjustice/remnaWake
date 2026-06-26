package forex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchRatesInverts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/RUB" {
			t.Errorf("path = %q, want /RUB", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		// symbol-per-base: 1 RUB = 0.011 USD, 0.0102 EUR.
		_, _ = w.Write([]byte(`{"result":"success","base_code":"RUB","rates":{"RUB":1,"USD":0.011,"EUR":0.0102}}`))
	}))
	defer srv.Close()

	c := NewClient()
	c.baseURL = srv.URL

	rates, err := c.FetchRates(context.Background(), "rub", []string{"USD", "EUR", "RUB", "ZZZ"})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	// base-per-symbol = 1 / symbol-per-base, so 1 USD ≈ 90.9 RUB.
	if got := rates["USD"]; got < 90 || got > 91.5 {
		t.Fatalf("USD = %v, want ~90.9", got)
	}
	if got := rates["EUR"]; got < 97 || got > 99 {
		t.Fatalf("EUR = %v, want ~98", got)
	}
	if rates["RUB"] != 1 {
		t.Fatalf("RUB = %v, want 1", rates["RUB"])
	}
	if _, ok := rates["ZZZ"]; ok {
		t.Fatalf("unknown symbol should be omitted, got %v", rates["ZZZ"])
	}
}

func TestFetchRatesErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewClient()
	c.baseURL = srv.URL
	if _, err := c.FetchRates(context.Background(), "USD", []string{"EUR"}); err == nil {
		t.Fatal("expected error on HTTP 500")
	}
}

func TestFetchRatesEmptyBase(t *testing.T) {
	if _, err := NewClient().FetchRates(context.Background(), "  ", []string{"EUR"}); err == nil {
		t.Fatal("expected error on empty base")
	}
}
