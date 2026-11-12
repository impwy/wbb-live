package donations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Toss calls the Toss Payments core API with a test secret key.
type Toss struct {
	SecretKey string
	BaseURL   string // https://api.tosspayments.com
	Client    *http.Client
}

func NewToss(secretKey string) *Toss {
	return &Toss{SecretKey: secretKey, BaseURL: "https://api.tosspayments.com",
		Client: &http.Client{Timeout: 10 * time.Second}}
}

type tossPayment struct {
	PaymentKey  string `json:"paymentKey"`
	OrderID     string `json:"orderId"`
	Status      string `json:"status"`
	TotalAmount int    `json:"totalAmount"`
}

func (t *Toss) do(ctx context.Context, method, path string, body any) (Payment, error) {
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, t.BaseURL+path, reader)
	if err != nil {
		return Payment{}, err
	}
	req.SetBasicAuth(t.SecretKey, "")
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.Client.Do(req)
	if err != nil {
		return Payment{}, fmt.Errorf("%w: %v", ErrProvider, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if resp.StatusCode/100 != 2 {
		var e struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(raw, &e)
		return Payment{}, fmt.Errorf("%w: status %d %s", ErrNotApproved, resp.StatusCode, e.Code)
	}
	var p tossPayment
	if err := json.Unmarshal(raw, &p); err != nil {
		return Payment{}, fmt.Errorf("%w: %v", ErrProvider, err)
	}
	return Payment(p), nil
}

func (t *Toss) Confirm(ctx context.Context, paymentKey, orderID string, amount int) (Payment, error) {
	return t.do(ctx, http.MethodPost, "/v1/payments/confirm",
		map[string]any{"paymentKey": paymentKey, "orderId": orderID, "amount": amount})
}

func (t *Toss) Get(ctx context.Context, paymentKey string) (Payment, error) {
	return t.do(ctx, http.MethodGet, "/v1/payments/"+url.PathEscape(paymentKey), nil)
}
