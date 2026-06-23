// Package xraychecker integrates an optional, separately deployed
// kutovoys/xray-checker sidecar. xray-checker probes connectivity through each
// configured proxy and exposes the per-proxy result as Prometheus gauges on its
// /metrics endpoint; this package fetches and parses those gauges so the bot can
// show proxy health to admins and alert them when a proxy goes down or recovers.
package xraychecker

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Prometheus metric names exported by xray-checker.
const (
	metricStatus  = "xray_proxy_status"
	metricLatency = "xray_proxy_latency_ms"
)

// ProxyStatus is one proxy's latest probe result, merged from the status and
// latency gauges that share the same label set.
type ProxyStatus struct {
	Name      string
	Protocol  string
	Address   string
	SubName   string
	Up        bool
	LatencyMs int
}

// Client polls a running xray-checker instance over HTTP. The /metrics endpoint
// may be protected by HTTP basic auth (METRICS_PROTECTED); credentials are sent
// only when configured.
type Client struct {
	baseURL  string
	username string
	password string
	http     *http.Client
}

// NewClient builds a client for the xray-checker at baseURL (e.g.
// http://xray-checker:2112). A non-positive timeout falls back to 15s.
func NewClient(baseURL, username, password string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		username: username,
		password: password,
		http:     &http.Client{Timeout: timeout},
	}
}

// Status fetches /metrics and returns the per-proxy status, sorted by name then
// address for stable display.
func (c *Client) Status(ctx context.Context) ([]ProxyStatus, error) {
	body, err := c.fetchMetrics(ctx)
	if err != nil {
		return nil, err
	}
	return parseMetrics(body), nil
}

// Ping verifies the checker is reachable and the credentials are accepted.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.fetchMetrics(ctx)
	return err
}

func (c *Client) fetchMetrics(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/metrics", nil)
	if err != nil {
		return "", err
	}
	if c.username != "" || c.password != "" {
		req.SetBasicAuth(c.username, c.password)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("xray-checker /metrics: unexpected status %d", resp.StatusCode)
	}
	var b strings.Builder
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		b.WriteString(sc.Text())
		b.WriteByte('\n')
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return b.String(), nil
}

// parseMetrics extracts the xray_proxy_status / xray_proxy_latency_ms gauges and
// merges the two (which carry the same labels) into one ProxyStatus per proxy.
func parseMetrics(body string) []ProxyStatus {
	order := make([]string, 0)
	byKey := make(map[string]*ProxyStatus)
	get := func(labels map[string]string) *ProxyStatus {
		key := labels["address"] + "|" + labels["name"] + "|" + labels["sub_name"]
		ps, ok := byKey[key]
		if !ok {
			ps = &ProxyStatus{
				Name:     labels["name"],
				Protocol: labels["protocol"],
				Address:  labels["address"],
				SubName:  labels["sub_name"],
			}
			byKey[key] = ps
			order = append(order, key)
		}
		return ps
	}

	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, labels, value, ok := parseSample(line)
		if !ok {
			continue
		}
		switch name {
		case metricStatus:
			get(labels).Up = value >= 0.5
		case metricLatency:
			get(labels).LatencyMs = int(value + 0.5)
		}
	}

	out := make([]ProxyStatus, 0, len(order))
	for _, key := range order {
		out = append(out, *byKey[key])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Address < out[j].Address
	})
	return out
}

// parseSample parses a single Prometheus exposition line of the form
// `name{label="v",...} value`, tolerating label values that contain commas or
// braces. ok is false for lines that are not a parseable sample.
func parseSample(line string) (name string, labels map[string]string, value float64, ok bool) {
	open := strings.IndexByte(line, '{')
	if open < 0 {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return "", nil, 0, false
		}
		v, err := strconv.ParseFloat(fields[1], 64)
		if err != nil {
			return "", nil, 0, false
		}
		return fields[0], map[string]string{}, v, true
	}

	name = strings.TrimSpace(line[:open])
	closeIdx := matchingBrace(line, open)
	if closeIdx < 0 {
		return "", nil, 0, false
	}
	rest := strings.Fields(strings.TrimSpace(line[closeIdx+1:]))
	if len(rest) == 0 {
		return "", nil, 0, false
	}
	v, err := strconv.ParseFloat(rest[0], 64)
	if err != nil {
		return "", nil, 0, false
	}
	return name, parseLabels(line[open+1 : closeIdx]), v, true
}

// matchingBrace returns the index of the '}' closing the '{' at open, ignoring
// braces inside quoted label values.
func matchingBrace(s string, open int) int {
	inQuote, esc := false, false
	for i := open + 1; i < len(s); i++ {
		c := s[i]
		switch {
		case esc:
			esc = false
		case c == '\\':
			esc = true
		case c == '"':
			inQuote = !inQuote
		case c == '}' && !inQuote:
			return i
		}
	}
	return -1
}

func parseLabels(block string) map[string]string {
	labels := make(map[string]string)
	for _, part := range splitTopLevel(block) {
		eq := strings.IndexByte(part, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(part[:eq])
		raw := strings.TrimSpace(part[eq+1:])
		if val, err := strconv.Unquote(raw); err == nil {
			labels[key] = val
		} else {
			labels[key] = strings.Trim(raw, `"`)
		}
	}
	return labels
}

// splitTopLevel splits a label block on commas that are not inside a quoted
// value, preserving quotes so the caller can Unquote each value.
func splitTopLevel(s string) []string {
	var out []string
	var cur strings.Builder
	inQuote, esc := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case esc:
			cur.WriteByte(c)
			esc = false
		case c == '\\':
			cur.WriteByte(c)
			esc = true
		case c == '"':
			inQuote = !inQuote
			cur.WriteByte(c)
		case c == ',' && !inQuote:
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	if strings.TrimSpace(cur.String()) != "" {
		out = append(out, cur.String())
	}
	return out
}
