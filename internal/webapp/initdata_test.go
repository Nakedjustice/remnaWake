package webapp

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"
)

const testToken = "123456:TEST-TOKEN"

// signInitData builds a valid initData query string for the given fields,
// computing the hash the same way Telegram does.
func signInitData(t *testing.T, fields map[string]string) string {
	t.Helper()
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, k+"="+fields[k])
	}
	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(testToken))
	mac := hmac.New(sha256.New, secret.Sum(nil))
	mac.Write([]byte(strings.Join(pairs, "\n")))
	hash := hex.EncodeToString(mac.Sum(nil))

	v := url.Values{}
	for k, val := range fields {
		v.Set(k, val)
	}
	v.Set("hash", hash)
	return v.Encode()
}

func TestValidateInitDataOK(t *testing.T) {
	now := time.Unix(1700000000, 0)
	initData := signInitData(t, map[string]string{
		"auth_date": "1699999000",
		"query_id":  "AAH",
		"user":      `{"id":42,"first_name":"Test","username":"test"}`,
	})

	id, err := validateInitData(initData, testToken, 24*time.Hour, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != 42 {
		t.Fatalf("user id = %d, want 42", id)
	}
}

func TestValidateInitDataBadHash(t *testing.T) {
	now := time.Unix(1700000000, 0)
	initData := signInitData(t, map[string]string{
		"auth_date": "1699999000",
		"user":      `{"id":42}`,
	})
	// Tamper with the payload after signing.
	initData = strings.Replace(initData, "42", "43", 1)

	if _, err := validateInitData(initData, testToken, 24*time.Hour, now); err == nil {
		t.Fatal("expected hash mismatch error, got nil")
	}
}

func TestValidateInitDataWrongToken(t *testing.T) {
	now := time.Unix(1700000000, 0)
	initData := signInitData(t, map[string]string{
		"auth_date": "1699999000",
		"user":      `{"id":42}`,
	})
	if _, err := validateInitData(initData, "999:OTHER", 24*time.Hour, now); err == nil {
		t.Fatal("expected hash mismatch for wrong token, got nil")
	}
}

func TestValidateInitDataStale(t *testing.T) {
	now := time.Unix(1700000000, 0)
	initData := signInitData(t, map[string]string{
		"auth_date": "1600000000", // far in the past
		"user":      `{"id":42}`,
	})
	if _, err := validateInitData(initData, testToken, 24*time.Hour, now); err == nil {
		t.Fatal("expected stale auth_date error, got nil")
	}
}

func TestValidateInitDataMissingHash(t *testing.T) {
	if _, err := validateInitData("user=%7B%22id%22%3A42%7D", testToken, 0, time.Now()); err == nil {
		t.Fatal("expected missing hash error, got nil")
	}
}
