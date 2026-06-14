package autoupdate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseImageRef(t *testing.T) {
	cases := []struct {
		in              string
		host, repo, tag string
		wantErr         bool
	}{
		{in: "ghcr.io/nakedjustice/remnawave:main", host: "ghcr.io", repo: "nakedjustice/remnawave", tag: "main"},
		{in: "ghcr.io/owner/name", host: "ghcr.io", repo: "owner/name", tag: "latest"},
		{in: "127.0.0.1:5000/owner/name:v1", host: "127.0.0.1:5000", repo: "owner/name", tag: "v1"},
		{in: "nohost", wantErr: true},
		{in: "", wantErr: true},
	}
	for _, c := range cases {
		got, err := parseImageRef(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseImageRef(%q) = %+v, want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseImageRef(%q): %v", c.in, err)
			continue
		}
		if got.Host != c.host || got.Repo != c.repo || got.Tag != c.tag {
			t.Errorf("parseImageRef(%q) = %+v, want {%s %s %s}", c.in, got, c.host, c.repo, c.tag)
		}
	}
}

func TestDigestFetch(t *testing.T) {
	const wantDigest = "sha256:abc123"
	var sawTokenScope, sawManifestAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			sawTokenScope = r.URL.Query().Get("scope")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"token":"tok-123"}`))
		case strings.HasPrefix(r.URL.Path, "/v2/") && strings.Contains(r.URL.Path, "/manifests/"):
			sawManifestAuth = r.Header.Get("Authorization")
			w.Header().Set("Docker-Content-Digest", wantDigest)
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	f := NewDigestFetcher()
	f.scheme = "http"

	got, err := f.Digest(context.Background(), host+"/owner/name:main")
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	if got != wantDigest {
		t.Fatalf("Digest = %q, want %q", got, wantDigest)
	}
	if sawTokenScope != "repository:owner/name:pull" {
		t.Fatalf("token scope = %q, want repository:owner/name:pull", sawTokenScope)
	}
	if sawManifestAuth != "Bearer tok-123" {
		t.Fatalf("manifest auth = %q, want Bearer tok-123", sawManifestAuth)
	}
}

func TestDigestFetchMissingHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			_, _ = w.Write([]byte(`{"token":"t"}`))
			return
		}
		w.WriteHeader(http.StatusOK) // no Docker-Content-Digest
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	f := NewDigestFetcher()
	f.scheme = "http"

	if _, err := f.Digest(context.Background(), host+"/owner/name:main"); err == nil {
		t.Fatal("expected error when Docker-Content-Digest header is absent")
	}
}

func TestDigestFetchManifestError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			_, _ = w.Write([]byte(`{"token":"t"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	f := NewDigestFetcher()
	f.scheme = "http"

	if _, err := f.Digest(context.Background(), host+"/owner/name:main"); err == nil {
		t.Fatal("expected error on non-200 manifest response")
	}
}
