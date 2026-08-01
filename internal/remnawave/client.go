package remnawave

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
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

// patchUser sends a PATCH /api/users request with the given JSON payload.
// The payload must include the target user's "id". op names the operation
// for error messages.
func (c *Client) patchUser(ctx context.Context, payload map[string]interface{}, op string) error {
	endpoint := fmt.Sprintf("%s/api/users", c.baseURL)
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
		return fmt.Errorf("%s: %w", op, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("%s: unauthorized (status=%d)", op, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s: status=%d body=%s", op, resp.StatusCode, textutil.Truncate(string(b), 300))
	}

	return nil
}

func (c *Client) ExtendSubscription(ctx context.Context, userID int64, newExpireAt time.Time) error {
	return c.patchUser(ctx, map[string]interface{}{
		"id":       userID,
		"expireAt": newExpireAt.Format(time.RFC3339),
	}, "extend subscription")
}

func (c *Client) SetTelegramID(ctx context.Context, userID, telegramID int64) error {
	return c.patchUser(ctx, map[string]interface{}{
		"id":         userID,
		"telegramId": telegramID,
	}, "set telegram id")
}

// UpdateUser patches the manageable fields of an existing user. Only the fields
// set in patch are sent; nil pointers are omitted entirely.
func (c *Client) UpdateUser(ctx context.Context, userID int64, patch UserPatch) error {
	payload := map[string]interface{}{"id": userID}
	if patch.ExpireAt != nil {
		payload["expireAt"] = patch.ExpireAt.UTC().Format(time.RFC3339)
	}
	if patch.HwidDeviceLimit != nil {
		payload["hwidDeviceLimit"] = *patch.HwidDeviceLimit
	}
	if patch.TrafficLimitBytes != nil {
		payload["trafficLimitBytes"] = *patch.TrafficLimitBytes
	}
	if patch.TrafficLimitStrategy != nil {
		payload["trafficLimitStrategy"] = *patch.TrafficLimitStrategy
	}
	if patch.Status != nil {
		payload["status"] = *patch.Status
	}
	if patch.ActiveInternalSquads != nil {
		payload["activeInternalSquads"] = *patch.ActiveInternalSquads
	}
	if patch.Tag != nil {
		if *patch.Tag == "" {
			payload["tag"] = nil
		} else {
			payload["tag"] = *patch.Tag
		}
	}
	return c.patchUser(ctx, payload, "update user")
}

func (c *Client) GetUserByUsername(ctx context.Context, username string) (*User, error) {
	endpoint := fmt.Sprintf("%s/api/users/by-username/%s", c.baseURL, url.PathEscape(username))
	return c.getUser(ctx, endpoint, "get user by username")
}

// GetUserByShortUUID resolves a user by the short UUID that terminates their
// subscription link. Returns nil without error when no user matches.
func (c *Client) GetUserByShortUUID(ctx context.Context, shortUUID string) (*User, error) {
	endpoint := fmt.Sprintf("%s/api/users/by-short-uuid/%s", c.baseURL, url.PathEscape(shortUUID))
	return c.getUser(ctx, endpoint, "get user by short uuid")
}

func (c *Client) getUser(ctx context.Context, endpoint, op string) (*User, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	c.setRequestHeaders(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("%s: unauthorized (status=%d)", op, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%s: status=%d body=%s", op, resp.StatusCode, textutil.Truncate(string(b), 300))
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

// CreateUserSpec contains fields that must be applied atomically when a user is
// created. Zero traffic/HWID limits use Remnawave's unlimited semantics.
type CreateUserSpec struct {
	Username             string
	ExpireAt             time.Time
	SquadUUIDs           []string
	TrafficLimitBytes    int64
	TrafficLimitStrategy string
	HwidDeviceLimit      int
	Tag                  string
}

func (c *Client) CreateUser(ctx context.Context, spec CreateUserSpec) (*User, error) {
	if spec.TrafficLimitStrategy == "" {
		spec.TrafficLimitStrategy = "NO_RESET"
	}
	endpoint := fmt.Sprintf("%s/api/users", c.baseURL)
	reqBody := map[string]interface{}{
		"username":             spec.Username,
		"expireAt":             spec.ExpireAt.UTC().Format(time.RFC3339),
		"status":               "ACTIVE",
		"trafficLimitBytes":    spec.TrafficLimitBytes,
		"trafficLimitStrategy": spec.TrafficLimitStrategy,
		"hwidDeviceLimit":      spec.HwidDeviceLimit,
	}
	if len(spec.SquadUUIDs) > 0 {
		reqBody["activeInternalSquads"] = spec.SquadUUIDs
	}
	if spec.Tag != "" {
		reqBody["tag"] = spec.Tag
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

func (c *Client) GetInternalSquads(ctx context.Context) ([]InternalSquad, error) {
	endpoint := fmt.Sprintf("%s/api/internal-squads", c.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	c.setRequestHeaders(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get internal squads: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("get internal squads: unauthorized (status=%d)", resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get internal squads: status=%d body=%s", resp.StatusCode, textutil.Truncate(string(b), 300))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var payload internalSquadsResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode internal squads: %w (body=%s)", err, textutil.Truncate(string(body), 300))
	}
	return payload.Response.InternalSquads, nil
}

// streamPageSize is the page size requested from GET /api/users/stream. The
// panel caps it at 1000; the filtered lookups here match far fewer than that.
const streamPageSize = 1000

// maxStreamPages bounds the keyset walk so a panel that keeps handing back a
// cursor can never spin the loop forever.
const maxStreamPages = 1000

// GetUserByTelegramID returns every profile linked to a Telegram ID — one ID may
// map to several subscriptions.
//
// v3 deleted GET /api/users/by-telegram-id/{telegramId}; the replacement is the
// keyset-paginated stream with a telegramId filter.
func (c *Client) GetUserByTelegramID(ctx context.Context, telegramID int64) ([]User, error) {
	filter := url.Values{"telegramId": {strconv.FormatInt(telegramID, 10)}}
	return c.streamUsers(ctx, filter, "get user by telegram id")
}

// streamUsers walks GET /api/users/stream to exhaustion with filter applied to
// every page. A 404 yields no matches rather than an error, preserving the
// contract the v2 by-* lookups had.
func (c *Client) streamUsers(ctx context.Context, filter url.Values, op string) ([]User, error) {
	var (
		out    []User
		cursor string
	)
	for i := 0; i < maxStreamPages; i++ {
		q := url.Values{}
		for k, vs := range filter {
			q[k] = vs
		}
		q.Set("size", strconv.Itoa(streamPageSize))
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		page, err := c.fetchUsersStreamPage(ctx, fmt.Sprintf("%s/api/users/stream?%s", c.baseURL, q.Encode()), op)
		if err != nil {
			return nil, err
		}
		if page == nil {
			return out, nil
		}
		out = append(out, page.Users...)
		// hasMore alone is not enough to continue: with no cursor to advance to,
		// another request would refetch the page we just read.
		if !page.HasMore || page.NextCursor == nil || *page.NextCursor == "" {
			return out, nil
		}
		cursor = *page.NextCursor
	}
	return nil, fmt.Errorf("%s: stream did not terminate after %d pages", op, maxStreamPages)
}

// usersStreamPage is one decoded page; a nil return means the panel answered 404.
type usersStreamPage struct {
	Users      []User
	NextCursor *string
	HasMore    bool
}

func (c *Client) fetchUsersStreamPage(ctx context.Context, endpoint, op string) (*usersStreamPage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	c.setRequestHeaders(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("%s: unauthorized (status=%d)", op, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%s: status=%d body=%s", op, resp.StatusCode, textutil.Truncate(string(b), 300))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var payload usersStreamResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode users stream: %w (body=%s)", err, textutil.Truncate(string(body), 300))
	}
	return &usersStreamPage{
		Users:      payload.Response.Users,
		NextCursor: payload.Response.NextCursor,
		HasMore:    payload.Response.HasMore,
	}, nil
}

func (c *Client) setRequestHeaders(req *http.Request) {
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiToken)
	req.Header.Set("User-Agent", "remnaWake/1.0")
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
