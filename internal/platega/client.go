// Package platega is a minimal client for the Platega.io payment gateway:
// creating transactions and reading their status. Authentication uses the
// merchant id + secret sent as X-MerchantId / X-Secret headers.
package platega

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/Nakedjustice/remnaWake/internal/textutil"
)

// BaseURL is the Platega API root. It is a var (not const) so tests can point
// the client at an httptest server.
var BaseURL = "https://app.platega.io"

// Payment method codes accepted by /transaction/process (per docs.platega.io
// PaymentMethodInt: 2=СБП/QR, 3=ЕРИП, 11=карточный эквайринг, 12=межд., 13=крипто).
const (
	MethodSBP   = 2
	MethodCards = 11
)

// Client talks to the Platega API with a fixed merchant id / secret.
type Client struct {
	http     *http.Client
	merchant string
	secret   string
}

// New returns a Platega client. timeout bounds each HTTP request.
func New(merchant, secret string, timeout time.Duration) *Client {
	return &Client{
		http:     &http.Client{Timeout: timeout},
		merchant: merchant,
		secret:   secret,
	}
}

type paymentDetails struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

type createRequest struct {
	PaymentMethod  int            `json:"paymentMethod"`
	PaymentDetails paymentDetails `json:"paymentDetails"`
	Description    string         `json:"description"`
	Return         string         `json:"return"`
	FailedURL      string         `json:"failedUrl"`
	Payload        string         `json:"payload,omitempty"`
}

type createResponse struct {
	TransactionID string `json:"transactionId"`
	Redirect      string `json:"redirect"`
	Status        string `json:"status"`
}

type statusResponse struct {
	ID             string         `json:"id"`
	Status         string         `json:"status"`
	PaymentDetails paymentDetails `json:"paymentDetails"`
	Payload        string         `json:"payload"`
}

// Transaction is a created or fetched Platega transaction.
type Transaction struct {
	ID       string
	Redirect string
	Status   string
	Amount   float64
	Currency string
	Payload  string
}

func (c *Client) headers(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-MerchantId", c.merchant)
	req.Header.Set("X-Secret", c.secret)
}

// CreateTransaction opens a payment and returns its id and redirect URL. amount
// is in major currency units (e.g. rubles); currency is an ISO code ("RUB").
// payload is echoed back by GetTransaction and the webhook, so callers use it to
// correlate the transaction with their own request (e.g. "req:<id>").
func (c *Client) CreateTransaction(ctx context.Context, method int, amount float64, currency, desc, returnURL, payload string) (*Transaction, error) {
	body, err := json.Marshal(createRequest{
		PaymentMethod:  method,
		PaymentDetails: paymentDetails{Amount: amount, Currency: currency},
		Description:    desc,
		Return:         returnURL,
		FailedURL:      returnURL,
		Payload:        payload,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, BaseURL+"/transaction/process", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	c.headers(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("platega create: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("platega create: status=%d body=%s", resp.StatusCode, textutil.Truncate(string(b), 300))
	}
	var cr createResponse
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return nil, fmt.Errorf("platega create: decode: %w", err)
	}
	if cr.Redirect == "" || cr.TransactionID == "" {
		return nil, fmt.Errorf("platega create: empty redirect/transactionId")
	}
	return &Transaction{ID: cr.TransactionID, Redirect: cr.Redirect, Status: cr.Status}, nil
}

// GetTransaction reads a transaction's current status straight from Platega.
// This is the source of truth used to verify webhook notifications, which carry
// only a transaction id.
func (c *Client) GetTransaction(ctx context.Context, id string) (*Transaction, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, BaseURL+"/transaction/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	c.headers(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("platega status: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("platega status: status=%d body=%s", resp.StatusCode, textutil.Truncate(string(b), 300))
	}
	var sr statusResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, fmt.Errorf("platega status: decode: %w", err)
	}
	return &Transaction{
		ID:       id,
		Status:   sr.Status,
		Amount:   sr.PaymentDetails.Amount,
		Currency: sr.PaymentDetails.Currency,
		Payload:  sr.Payload,
	}, nil
}
