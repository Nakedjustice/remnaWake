package webapp

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// getStatic issues a GET against the full mini app handler, optionally
// announcing gzip support the way a real browser does.
func getStatic(t *testing.T, path string, acceptGzip bool, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	srv := newTestServer(&fakeCabinet{})
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if acceptGzip {
		req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	return w
}

func TestFrontendIsGzippedWhenTheClientAcceptsIt(t *testing.T) {
	w := getStatic(t, "/", true, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if got := w.Header().Get("Vary"); !strings.Contains(got, "Accept-Encoding") {
		t.Fatalf("Vary = %q, want it to contain Accept-Encoding", got)
	}
	// A Content-Length left over from the uncompressed body would desync the
	// response and hang the client.
	if got := w.Header().Get("Content-Length"); got != "" {
		t.Fatalf("Content-Length = %q, want it dropped for a compressed body", got)
	}

	compressed := w.Body.Bytes()
	zr, err := gzip.NewReader(strings.NewReader(string(compressed)))
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	plain, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	if !strings.Contains(string(plain), "/api/me") {
		t.Fatal("decompressed frontend does not look like the mini app")
	}
	// The whole point: the 270 KB frontend must shrink substantially.
	if len(compressed) >= len(plain)/2 {
		t.Fatalf("gzip saved too little: %d compressed vs %d plain", len(compressed), len(plain))
	}
}

func TestFrontendStaysPlainWhenGzipIsNotAccepted(t *testing.T) {
	w := getStatic(t, "/", false, nil)
	if got := w.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want none", got)
	}
	if !strings.Contains(w.Body.String(), "/api/me") {
		t.Fatal("plain frontend body is not the mini app")
	}
}

func TestFrontendRevisitIs304ViaETag(t *testing.T) {
	first := getStatic(t, "/", false, nil)
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag on the frontend; repeat opens re-download the whole app")
	}
	second := getStatic(t, "/", false, map[string]string{"If-None-Match": etag})
	if second.Code != http.StatusNotModified {
		t.Fatalf("revisit status = %d, want 304", second.Code)
	}
	if second.Body.Len() != 0 {
		t.Fatalf("304 carried a %d-byte body", second.Body.Len())
	}
	if second.Header().Get("Content-Encoding") != "" {
		t.Fatal("304 must not claim an encoding: it has no body to encode")
	}
}

func TestFrontendIsRevalidatedNotCachedBlindly(t *testing.T) {
	w := getStatic(t, "/", false, nil)
	if got := w.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache so a release reaches clients", got)
	}
}

func TestFontsAreCachedLongAndLeftUncompressed(t *testing.T) {
	w := getStatic(t, "/fonts/onest-latin.woff2", true, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("Cache-Control"); !strings.Contains(got, "max-age=") {
		t.Fatalf("Cache-Control = %q, want a long max-age", got)
	}
	// woff2 is already compressed; re-gzipping burns CPU for nothing.
	if got := w.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want the font served as-is", got)
	}
}

func TestSmallJSONResponsesAreNotCompressed(t *testing.T) {
	srv := newTestServer(&fakeCabinet{})
	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	// Unauthorized: a tiny JSON error. Compressing it would make it bigger.
	if got := w.Header().Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want tiny bodies left alone", got)
	}
	if !strings.Contains(w.Body.String(), "{") {
		t.Fatalf("body = %q, want JSON", w.Body.String())
	}
}
