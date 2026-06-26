// Package forex fetches live FX reference rates for infrastructure-cost
// conversion. It targets the keyless open.er-api.com endpoint and is
// best-effort: callers fall back to admin-entered manual rates when a fetch
// fails, so a blocked or unreachable network never breaks cost reporting.
package forex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// defaultBaseURL is the keyless exchange-rate endpoint. The full request path is
// "<baseURL>/<BASE>" and the response carries symbol-per-base rates.
const defaultBaseURL = "https://open.er-api.com/v6/latest"

// Client fetches base-per-symbol rates over HTTP. The zero value is not usable;
// construct one with NewClient.
type Client struct {
	httpClient *http.Client
	baseURL    string
}

// NewClient returns a Client with a short timeout. It uses the default
// http.Transport, which honours HTTPS_PROXY for environments behind a proxy.
func NewClient() *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 10 * time.Second},
		baseURL:    defaultBaseURL,
	}
}

// FetchRates returns how many units of base equal one unit of each requested
// symbol (base-per-symbol). A symbol equal to base maps to 1; symbols missing
// from the upstream response are omitted. The upstream returns symbol-per-base
// rates, which are inverted here.
func (c *Client) FetchRates(ctx context.Context, base string, symbols []string) (map[string]float64, error) {
	base = strings.ToUpper(strings.TrimSpace(base))
	if base == "" {
		return nil, fmt.Errorf("forex: empty base currency")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/"+base, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("forex: unexpected status %d", resp.StatusCode)
	}

	var body struct {
		Result string             `json:"result"`
		Rates  map[string]float64 `json:"rates"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("forex: decode: %w", err)
	}
	if body.Result != "" && body.Result != "success" {
		return nil, fmt.Errorf("forex: result %q", body.Result)
	}

	out := make(map[string]float64, len(symbols))
	for _, sym := range symbols {
		sym = strings.ToUpper(strings.TrimSpace(sym))
		if sym == "" {
			continue
		}
		if sym == base {
			out[sym] = 1
			continue
		}
		// body.Rates[sym] is symbol-per-base; invert to base-per-symbol.
		if perBase, ok := body.Rates[sym]; ok && perBase > 0 {
			out[sym] = 1 / perBase
		}
	}
	return out, nil
}
