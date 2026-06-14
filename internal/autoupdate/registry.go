// Package autoupdate watches the bot's container image for new versions and,
// when a Watchtower sidecar is wired in, lets an admin apply the update.
package autoupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// imageRef is a parsed container image reference (registry host, repository
// path and tag).
type imageRef struct {
	Host string
	Repo string
	Tag  string
}

// parseImageRef splits "ghcr.io/owner/name:tag" into its parts. A missing tag
// defaults to "latest". The host is required (no implicit Docker Hub).
func parseImageRef(ref string) (imageRef, error) {
	ref = strings.TrimSpace(ref)
	slash := strings.IndexByte(ref, '/')
	if slash < 0 {
		return imageRef{}, fmt.Errorf("image ref %q must include a registry host", ref)
	}
	host := ref[:slash]
	rest := ref[slash+1:]
	if host == "" || rest == "" {
		return imageRef{}, fmt.Errorf("invalid image ref %q", ref)
	}
	tag := "latest"
	if colon := strings.LastIndexByte(rest, ':'); colon >= 0 {
		tag = rest[colon+1:]
		rest = rest[:colon]
	}
	if rest == "" || tag == "" {
		return imageRef{}, fmt.Errorf("invalid image ref %q", ref)
	}
	return imageRef{Host: host, Repo: rest, Tag: tag}, nil
}

// DigestFetcher resolves the current manifest digest of an image tag from a
// registry that uses the Docker/OCI bearer-token flow (GHCR in this project).
type DigestFetcher struct {
	client *http.Client
	scheme string // "https" in production; tests override with "http"
}

// NewDigestFetcher returns a fetcher with a sane HTTP timeout.
func NewDigestFetcher() *DigestFetcher {
	return &DigestFetcher{
		client: &http.Client{Timeout: 15 * time.Second},
		scheme: "https",
	}
}

const manifestAccept = "application/vnd.oci.image.index.v1+json," +
	"application/vnd.docker.distribution.manifest.list.v2+json," +
	"application/vnd.oci.image.manifest.v1+json," +
	"application/vnd.docker.distribution.manifest.v2+json"

// Digest returns the manifest digest (the value of the Docker-Content-Digest
// response header) for the given image ref's tag.
func (f *DigestFetcher) Digest(ctx context.Context, ref string) (string, error) {
	img, err := parseImageRef(ref)
	if err != nil {
		return "", err
	}

	token, err := f.fetchToken(ctx, img)
	if err != nil {
		return "", err
	}

	manifestURL := fmt.Sprintf("%s://%s/v2/%s/manifests/%s", f.scheme, img.Host, img.Repo, img.Tag)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", manifestAccept)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch manifest: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch manifest: unexpected status %d", resp.StatusCode)
	}
	digest := strings.TrimSpace(resp.Header.Get("Docker-Content-Digest"))
	if digest == "" {
		return "", fmt.Errorf("manifest response missing Docker-Content-Digest header")
	}
	return digest, nil
}

// fetchToken obtains an anonymous pull token for the repository. Public images
// still require a (free) bearer token on GHCR.
func (f *DigestFetcher) fetchToken(ctx context.Context, img imageRef) (string, error) {
	tokenURL := fmt.Sprintf("%s://%s/token?scope=repository:%s:pull&service=%s", f.scheme, img.Host, img.Repo, img.Host)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch token: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch token: unexpected status %d", resp.StatusCode)
	}
	var body struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decode token: %w", err)
	}
	if body.Token != "" {
		return body.Token, nil
	}
	return body.AccessToken, nil
}
