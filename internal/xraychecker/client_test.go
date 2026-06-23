package xraychecker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const sampleMetrics = `# HELP xray_proxy_status Proxy status (1 up, 0 down)
# TYPE xray_proxy_status gauge
xray_proxy_status{protocol="vless",address="a.example.com:443",name="Alpha",sub_name="Premium"} 1
xray_proxy_latency_ms{protocol="vless",address="a.example.com:443",name="Alpha",sub_name="Premium"} 156
xray_proxy_status{protocol="trojan",address="b.example.com:443",name="Bravo, EU",sub_name="Premium"} 0
xray_proxy_latency_ms{protocol="trojan",address="b.example.com:443",name="Bravo, EU",sub_name="Premium"} 0
go_goroutines 12
`

func TestStatusParsesAndMergesGauges(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "metrics" || pass != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/metrics" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(sampleMetrics))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "metrics", "secret", time.Second)
	got, err := c.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 proxies, got %d: %+v", len(got), got)
	}
	// Sorted by name: Alpha then "Bravo, EU".
	if got[0].Name != "Alpha" || !got[0].Up || got[0].LatencyMs != 156 || got[0].Protocol != "vless" {
		t.Fatalf("alpha row wrong: %+v", got[0])
	}
	// Label value with a comma must survive parsing.
	if got[1].Name != "Bravo, EU" || got[1].Up || got[1].Address != "b.example.com:443" {
		t.Fatalf("bravo row wrong: %+v", got[1])
	}
}

func TestStatusRejectsBadCredentials(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "metrics", "wrong", time.Second)
	if err := c.Ping(context.Background()); err == nil {
		t.Fatal("expected error on 401")
	}
}

func TestParseMetricsIgnoresUnrelatedLines(t *testing.T) {
	body := `# comment
xray_proxy_status{name="Solo",address="s:443",protocol="vmess",sub_name=""} 1
some_other_metric{foo="bar"} 99
`
	got := parseMetrics(body)
	if len(got) != 1 || got[0].Name != "Solo" || !got[0].Up {
		t.Fatalf("unexpected parse: %+v", got)
	}
}
