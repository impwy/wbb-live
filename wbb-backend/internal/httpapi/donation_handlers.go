package httpapi

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/impwy/wbb-live/wbb-backend/internal/config"
	"github.com/impwy/wbb-live/wbb-backend/internal/donations"
)

// Donations use the in-process test-mode gateway; no real payment happens.
func newDonations(_ config.Config, db *pgxpool.Pool) *donations.Service {
	return donations.NewService(db, donations.NewMemoryProvider())
}

// SetPaymentProvider replaces the provider (tests use a fake).
func (s *Server) SetPaymentProvider(p donations.Provider) {
	s.donations = donations.NewService(s.db, p)
}

// GET /api/v1/payments/config (public): whether donations are enabled.
func (s *Server) handlePaymentsConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"enabled": s.donations.Enabled(), "mode": "test"})
}

// POST /api/v1/payments/test-checkout (signed-in donor): the test gateway's
// payment window. Approving issues a payment key for the stored amount.
func (s *Server) handleTestCheckout(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OrderID string `json:"orderId"`
		Approve bool   `json:"approve"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	p, err := s.donations.TestCheckout(r.Context(), authUser(r).ID, req.OrderID, req.Approve)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"paymentKey": p.PaymentKey, "orderId": p.OrderID, "amount": p.TotalAmount, "status": p.Status})
}

// GET /api/v1/broadcasts/{id}/donations (public): confirmed donations and total.
func (s *Server) handleListDonations(w http.ResponseWriter, r *http.Request) {
	summary, err := s.donations.List(r.Context(), r.PathValue("id"))
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

// POST /api/v1/broadcasts/{id}/donations (signed in): creates a pending order.
func (s *Server) handleCreateDonation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Amount  int    `json:"amount"`
		Message string `json:"message"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	order, err := s.donations.CreateOrder(r.Context(), authUser(r).ID, r.PathValue("id"), req.Amount, req.Message)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"order": order})
}

// POST /api/v1/donations/confirm (signed-in donor): confirms after the
// provider redirect. Repeating it does not credit the donation twice.
func (s *Server) handleConfirmDonation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PaymentKey string `json:"paymentKey"`
		OrderID    string `json:"orderId"`
		Amount     int    `json:"amount"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	credited, err := s.donations.Confirm(r.Context(), authUser(r).ID, req.PaymentKey, req.OrderID, req.Amount)
	if err != nil {
		s.log.Warn("donation confirm failed", "error", err)
		writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "confirmed", "credited": credited})
}

// POST /api/v1/payments/webhook (provider): the body only names the payment;
// the server re-reads it from the provider before crediting.
func (s *Server) handlePaymentWebhook(w http.ResponseWriter, r *http.Request) {
	var event struct {
		Data struct {
			PaymentKey string `json:"paymentKey"`
		} `json:"data"`
	}
	raw, _ := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err := json.Unmarshal(raw, &event); err != nil || event.Data.PaymentKey == "" {
		writeError(w, r, http.StatusBadRequest, "invalid_input", "paymentKey is required")
		return
	}
	credited, err := s.donations.Reconcile(r.Context(), event.Data.PaymentKey)
	if err != nil {
		s.log.Warn("payment webhook ignored", "error", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"credited": credited})
}
