package webapp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	tg "github.com/Nakedjustice/remnaWake/internal/telegram"
)

var (
	errNoHash      = errors.New("initData: missing hash")
	errBadHash     = errors.New("initData: hash mismatch")
	errStaleAuth   = errors.New("initData: auth_date is too old")
	errNoUser      = errors.New("initData: missing user")
	errBadUserJSON = errors.New("initData: malformed user field")
)

// validateInitData verifies a Telegram Mini App initData string against the
// bot token per https://core.telegram.org/bots/webapps#validating-data-received
// and returns the authenticated Telegram user. maxAge guards against replay of
// old initData; pass 0 to skip the freshness check.
func validateInitData(initData, botToken string, maxAge time.Duration, now time.Time) (*tg.User, error) {
	values, err := url.ParseQuery(initData)
	if err != nil {
		return nil, fmt.Errorf("initData: parse: %w", err)
	}

	gotHash := values.Get("hash")
	if gotHash == "" {
		return nil, errNoHash
	}

	keys := make([]string, 0, len(values))
	for k := range values {
		if k == "hash" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, k+"="+values.Get(k))
	}
	dataCheckString := strings.Join(pairs, "\n")

	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(botToken))
	mac := hmac.New(sha256.New, secret.Sum(nil))
	mac.Write([]byte(dataCheckString))
	expected := hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(expected), []byte(gotHash)) {
		return nil, errBadHash
	}

	if maxAge > 0 {
		authUnix, err := strconv.ParseInt(values.Get("auth_date"), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("initData: bad auth_date: %w", err)
		}
		if now.Sub(time.Unix(authUnix, 0)) > maxAge {
			return nil, errStaleAuth
		}
	}

	rawUser := values.Get("user")
	if rawUser == "" {
		return nil, errNoUser
	}
	var user tg.User
	if err := json.Unmarshal([]byte(rawUser), &user); err != nil || user.ID == 0 {
		return nil, errBadUserJSON
	}
	return &user, nil
}
