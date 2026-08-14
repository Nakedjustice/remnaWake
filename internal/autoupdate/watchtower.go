package autoupdate

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// Watchtower triggers a one-shot container update through a Watchtower sidecar
// running in HTTP-API mode (WATCHTOWER_HTTP_API_UPDATE=true). Watchtower pulls
// the new image and recreates the container; it holds the docker socket, so the
// bot itself stays unprivileged and only makes an authenticated HTTP call.
type Watchtower struct {
	baseURL string
	token   string
	client  *http.Client
}

// NewWatchtower returns a trigger client for the given Watchtower base URL and
// API token.
func NewWatchtower(baseURL, token string) *Watchtower {
	return &Watchtower{
		baseURL: baseURL,
		token:   token,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// Trigger hits Watchtower's /v1/update endpoint, asking it to check and apply
// image updates now. It returns an error on any non-2xx response.
//
// The request is a POST because that is the only verb both Watchtower builds
// accept: the maintained fork routes /v1/update through app.Post, while the
// original containrrr build registered it with http.HandleFunc and so answers
// any method. A GET reaches the fork as 405.
func (w *Watchtower) Trigger(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.baseURL+"/v1/update", nil)
	if err != nil {
		return err
	}
	if w.token != "" {
		req.Header.Set("Authorization", "Bearer "+w.token)
	}
	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("watchtower trigger: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("watchtower trigger: unexpected status %d", resp.StatusCode)
	}
	return nil
}
