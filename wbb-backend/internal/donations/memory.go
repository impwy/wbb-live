package donations

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
)

// MemoryProvider is a test-mode payment gateway kept in the API process.
// A checkout authorizes an order and issues a payment key (READY); the
// server then confirms it (DONE) exactly like an external gateway would.
// State is lost on restart; confirmed donations live in PostgreSQL.
type MemoryProvider struct {
	mu       sync.Mutex
	payments map[string]*Payment // by payment key
	byOrder  map[string]string   // order id -> payment key
}

func NewMemoryProvider() *MemoryProvider {
	return &MemoryProvider{payments: map[string]*Payment{}, byOrder: map[string]string{}}
}

// Authorize starts a test payment for the stored order amount. Repeating it
// for the same order returns the same payment.
func (m *MemoryProvider) Authorize(orderID string, amount int) Payment {
	m.mu.Lock()
	defer m.mu.Unlock()
	if key, ok := m.byOrder[orderID]; ok {
		return *m.payments[key]
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	p := &Payment{PaymentKey: "test_pk_" + hex.EncodeToString(b), OrderID: orderID, Status: "READY", TotalAmount: amount}
	m.payments[p.PaymentKey] = p
	m.byOrder[orderID] = p.PaymentKey
	return *p
}

// Confirm approves an authorized payment when order and amount match.
func (m *MemoryProvider) Confirm(_ context.Context, paymentKey, orderID string, amount int) (Payment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.payments[paymentKey]
	if !ok {
		return Payment{}, fmt.Errorf("%w: unknown payment", ErrNotApproved)
	}
	if p.OrderID != orderID || p.TotalAmount != amount {
		return Payment{}, ErrMismatch
	}
	p.Status = "DONE"
	return *p, nil
}

func (m *MemoryProvider) Get(_ context.Context, paymentKey string) (Payment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.payments[paymentKey]
	if !ok {
		return Payment{}, fmt.Errorf("%w: unknown payment", ErrNotApproved)
	}
	return *p, nil
}
