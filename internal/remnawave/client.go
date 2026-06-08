package remnawave

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/textutil"
)

type Client struct {
	baseURL  string
	apiToken string
	http     *http.Client
}

func NewClient(baseURL, apiToken string, timeout time.Duration) (*Client, error) {
	return &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		apiToken: apiToken,
		http: &http.Client{
			Timeout: timeout,
		},
	}, nil
}

func (c *Client) GetUsers(ctx context.Context) ([]User, error) {
	users, total, err := c.fetchUsersPage(ctx, 0, 1000)
	if err != nil {
		return nil, err
	}
	for int64(len(users)) < total {
		next, _, err := c.fetchUsersPage(ctx, len(users), 1000)
		if err != nil {
			return nil, err
		}
		if len(next) == 0 {
			break
		}
		users = append(users, next...)
		if len(next) >= 1000 {
			continue
		}
		break
	}
	return users, nil
}

func (c *Client) fetchUsersPage(ctx context.Context, start, size int) ([]User, int64, error) {
	endpoint := fmt.Sprintf("%s/api/users?start=%d&size=%d", c.baseURL, start, size)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, err
	}
	c.setRequestHeaders(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("get users: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, 0, fmt.Errorf("get users: unauthorized (status=%d)", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, 0, fmt.Errorf("get users: status=%d body=%s", resp.StatusCode, textutil.Truncate(string(b), 300))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, err
	}

	var payload UsersResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, 0, fmt.Errorf("decode users: %w (body=%s)", err, textutil.Truncate(string(body), 300))
	}
	return payload.Response.Users, int64(payload.Response.Total), nil
}

func (c *Client) ExtendSubscriptionByUUID(ctx context.Context, uuid string, newExpireAt time.Time) error {
	endpoint := fmt.Sprintf("%s/api/users", c.baseURL)
	payload := map[string]interface{}{
		"uuid":     uuid,
		"expireAt": newExpireAt.Format(time.RFC3339),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	c.setRequestHeaders(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("extend subscription: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("extend subscription: unauthorized (status=%d)", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("extend subscription: status=%d body=%s", resp.StatusCode, textutil.Truncate(string(b), 300))
	}

	return nil
}

func (c *Client) SetTelegramID(ctx context.Context, uuid string, telegramID int64) error {
	endpoint := fmt.Sprintf("%s/api/users", c.baseURL)
	payload := map[string]interface{}{
		"uuid":       uuid,
		"telegramId": telegramID,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	c.setRequestHeaders(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("set telegram id: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("set telegram id: unauthorized (status=%d)", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("set telegram id: status=%d body=%s", resp.StatusCode, textutil.Truncate(string(b), 300))
	}

	return nil
}

func (c *Client) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	endpoint := fmt.Sprintf("%s/api/users/by-username/%s", c.baseURL, url.PathEscape(username))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	c.setRequestHeaders(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get user by username: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("get user by username: unauthorized (status=%d)", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get user by username: status=%d body=%s", resp.StatusCode, textutil.Truncate(string(b), 300))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var payload userResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode user: %w (body=%s)", err, textutil.Truncate(string(body), 300))
	}
	return &payload.Response, nil
}

func (c *Client) CreateUser(ctx context.Context, username string, expireAt time.Time) (*User, error) {
	endpoint := fmt.Sprintf("%s/api/users", c.baseURL)
	reqBody := map[string]interface{}{
		"username":             username,
		"expireAt":             expireAt.UTC().Format(time.RFC3339),
		"status":               "ACTIVE",
		"trafficLimitBytes":    0,
		"trafficLimitStrategy": "NO_RESET",
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	c.setRequestHeaders(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("create user: unauthorized (status=%d)", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("create user: status=%d body=%s", resp.StatusCode, textutil.Truncate(string(b), 300))
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var payload userResponse
	if err := json.Unmarshal(respBody, &payload); err != nil {
		return nil, fmt.Errorf("decode created user: %w (body=%s)", err, textutil.Truncate(string(respBody), 300))
	}
	return &payload.Response, nil
}

func (c *Client) GetUserByTelegramID(ctx context.Context, telegramID int64) ([]User, error) {
	endpoint := fmt.Sprintf("%s/api/users/by-telegram-id/%d", c.baseURL, telegramID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	c.setRequestHeaders(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get user by telegram id: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("get user by telegram id: unauthorized (status=%d)", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get user by telegram id: status=%d body=%s", resp.StatusCode, textutil.Truncate(string(b), 300))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var payload usersByTgResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode users: %w (body=%s)", err, textutil.Truncate(string(body), 300))
	}
	return payload.Response, nil
}

func (c *Client) setRequestHeaders(req *http.Request) {
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiToken)
	req.Header.Set("User-Agent", "remnawave-notify-bot/1.0")
}

func (c *Client) BaseURL() string {
	return c.baseURL
}

func (c *Client) RedactedURL() string {
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return ""
	}
	return u.Host
}
