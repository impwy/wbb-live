// Package donations records donations paid through a test-mode payment
// provider (an in-process gateway, MemoryProvider). The server fixes the recipient and amount when the order is
// created and confirms a payment only after the provider reports it DONE
// with the same order id and amount. Confirmation is idempotent: repeated
// approval notices for an order credit it once.
package donations

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound          = errors.New("donation target or order not found")
	ErrForbidden         = errors.New("order belongs to another user")
	ErrInvalidInput      = errors.New("invalid donation input")
	ErrMismatch          = errors.New("payment does not match the order")
	ErrNotApproved       = errors.New("payment was not approved")
	ErrProvider          = errors.New("payment provider unavailable")
	ErrNotConfigured     = errors.New("payments are not configured")
	MinAmount, MaxAmount = 1000, 1000000
)

// Payment is the provider's view of one payment.
type Payment struct {
	PaymentKey  string
	OrderID     string
	Status      string // DONE when approved
	TotalAmount int
}

// Provider confirms and looks up payments.
type Provider interface {
	Confirm(ctx context.Context, paymentKey, orderID string, amount int) (Payment, error)
	Get(ctx context.Context, paymentKey string) (Payment, error)
}

type Order struct {
	OrderID   string `json:"orderId"`
	Amount    int    `json:"amount"`
	OrderName string `json:"orderName"`
	Recipient string `json:"recipient"`
}

type Donation struct {
	ID          string    `json:"id"`
	DonorName   string    `json:"donorName"`
	Amount      int       `json:"amount"`
	Message     string    `json:"message"`
	ConfirmedAt time.Time `json:"confirmedAt"`
}

type Summary struct {
	Items []Donation `json:"items"`
	Total int64      `json:"total"`
	Count int64      `json:"count"`
}

type Service struct {
	db       *pgxpool.Pool
	provider Provider
}

func NewService(db *pgxpool.Pool, provider Provider) *Service {
	return &Service{db: db, provider: provider}
}

func (s *Service) Enabled() bool { return s.provider != nil }

func newOrderID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "wbb-" + hex.EncodeToString(b)
}

// CreateOrder registers a pending donation to the broadcast's owner.
func (s *Service) CreateOrder(ctx context.Context, donorID, broadcastID string, amount int, message string) (Order, error) {
	if s.provider == nil {
		return Order{}, ErrNotConfigured
	}
	message = strings.TrimSpace(message)
	if amount < MinAmount || amount > MaxAmount || len([]rune(message)) > 200 {
		return Order{}, ErrInvalidInput
	}
	var title, recipient string
	orderID := newOrderID()
	err := s.db.QueryRow(ctx, `WITH b AS (
			SELECT b.id, b.owner_id, b.title, u.display_name FROM broadcasts b JOIN users u ON u.id = b.owner_id
			WHERE b.id::text = $2 AND b.deleted_at IS NULL),
		ins AS (INSERT INTO donations (order_id, broadcast_id, recipient_id, donor_id, amount, message)
			SELECT $1, b.id, b.owner_id, $3, $4, $5 FROM b RETURNING 1)
		SELECT b.title, b.display_name FROM b, ins`, orderID, broadcastID, donorID, amount, message).Scan(&title, &recipient)
	if errors.Is(err, pgx.ErrNoRows) {
		return Order{}, ErrNotFound
	}
	if err != nil {
		return Order{}, err
	}
	name := []rune(title + " 후원")
	if len(name) > 100 {
		name = name[:100]
	}
	return Order{OrderID: orderID, Amount: amount, OrderName: string(name), Recipient: recipient}, nil
}

// authorizer is a provider whose test checkout runs in this process.
type authorizer interface {
	Authorize(orderID string, amount int) Payment
}

// TestCheckout plays the gateway's payment window for the donor's pending
// order. The amount comes from the stored order, never from the client.
// Declining marks the order failed.
func (s *Service) TestCheckout(ctx context.Context, donorID, orderID string, approve bool) (Payment, error) {
	gateway, ok := s.provider.(authorizer)
	if !ok {
		return Payment{}, ErrNotConfigured
	}
	p, err := s.load(ctx, orderID)
	if err != nil {
		return Payment{}, err
	}
	if p.donorID != donorID {
		return Payment{}, ErrForbidden
	}
	if p.status != "pending" {
		return Payment{}, ErrNotApproved
	}
	if !approve {
		_, err := s.db.Exec(ctx, `UPDATE donations SET status='failed' WHERE order_id=$1 AND status='pending'`, orderID)
		if err != nil {
			return Payment{}, err
		}
		return Payment{}, ErrNotApproved
	}
	return gateway.Authorize(orderID, p.amount), nil
}

type pending struct {
	donorID, status, paymentKey string
	amount                      int
}

func (s *Service) load(ctx context.Context, orderID string) (pending, error) {
	var p pending
	var key *string
	err := s.db.QueryRow(ctx, `SELECT donor_id::text, status, payment_key, amount FROM donations WHERE order_id = $1`, orderID).
		Scan(&p.donorID, &p.status, &key, &p.amount)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, ErrNotFound
	}
	if key != nil {
		p.paymentKey = *key
	}
	return p, err
}

// Confirm is called by the donor after the provider redirects back. The
// amount must equal the stored order amount before the provider is asked
// to approve it.
func (s *Service) Confirm(ctx context.Context, donorID, paymentKey, orderID string, amount int) (bool, error) {
	if s.provider == nil {
		return false, ErrNotConfigured
	}
	p, err := s.load(ctx, orderID)
	if err != nil {
		return false, err
	}
	if p.donorID != donorID {
		return false, ErrForbidden
	}
	if p.amount != amount || paymentKey == "" {
		return false, ErrMismatch
	}
	if p.status == "confirmed" {
		if p.paymentKey != paymentKey {
			return false, ErrMismatch
		}
		return false, nil // repeated notice: already credited once
	}
	if p.status != "pending" {
		return false, ErrNotApproved
	}
	payment, err := s.provider.Confirm(ctx, paymentKey, orderID, amount)
	if err != nil {
		// The provider may have approved it on an earlier, lost call.
		if got, lookupErr := s.provider.Get(ctx, paymentKey); lookupErr == nil {
			payment = got
		} else {
			return false, err
		}
	}
	return s.record(ctx, payment, orderID, amount)
}

// Reconcile handles a provider webhook. The payload is only a hint: the
// payment is looked up from the provider before anything is recorded.
func (s *Service) Reconcile(ctx context.Context, paymentKey string) (bool, error) {
	if s.provider == nil {
		return false, ErrNotConfigured
	}
	payment, err := s.provider.Get(ctx, paymentKey)
	if err != nil {
		return false, err
	}
	p, err := s.load(ctx, payment.OrderID)
	if err != nil {
		return false, err
	}
	return s.record(ctx, payment, payment.OrderID, p.amount)
}

// record credits an approved payment once. It returns true only for the
// call that changed the order from pending to confirmed.
func (s *Service) record(ctx context.Context, payment Payment, orderID string, amount int) (bool, error) {
	if payment.OrderID != orderID || payment.TotalAmount != amount {
		return false, ErrMismatch
	}
	if payment.Status != "DONE" {
		return false, ErrNotApproved
	}
	tag, err := s.db.Exec(ctx, `UPDATE donations SET status='confirmed', payment_key=$2, confirmed_at=clock_timestamp()
		WHERE order_id=$1 AND status='pending' AND amount=$3`, orderID, payment.PaymentKey, amount)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 1 {
		return true, nil
	}
	p, err := s.load(ctx, orderID)
	if err != nil {
		return false, err
	}
	if p.status == "confirmed" && p.paymentKey == payment.PaymentKey {
		return false, nil
	}
	return false, ErrMismatch
}

// List returns confirmed donations of a broadcast with the total.
func (s *Service) List(ctx context.Context, broadcastID string) (Summary, error) {
	out := Summary{Items: []Donation{}}
	var exists bool
	if err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM broadcasts WHERE id::text=$1 AND deleted_at IS NULL)`, broadcastID).Scan(&exists); err != nil {
		return out, err
	}
	if !exists {
		return out, ErrNotFound
	}
	if err := s.db.QueryRow(ctx, `SELECT COALESCE(SUM(amount),0), COUNT(*) FROM donations
		WHERE broadcast_id::text=$1 AND status='confirmed'`, broadcastID).Scan(&out.Total, &out.Count); err != nil {
		return out, err
	}
	rows, err := s.db.Query(ctx, `SELECT d.id, u.display_name, d.amount, d.message, d.confirmed_at
		FROM donations d JOIN users u ON u.id = d.donor_id
		WHERE d.broadcast_id::text=$1 AND d.status='confirmed' ORDER BY d.confirmed_at DESC LIMIT 50`, broadcastID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var d Donation
		if err := rows.Scan(&d.ID, &d.DonorName, &d.Amount, &d.Message, &d.ConfirmedAt); err != nil {
			return out, err
		}
		out.Items = append(out.Items, d)
	}
	return out, rows.Err()
}
