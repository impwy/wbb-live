package httpapi

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/impwy/wbb-live/wbb-backend/internal/config"
	"github.com/impwy/wbb-live/wbb-backend/internal/donations"
)

func newDonations(cfg config.Config, db *pgxpool.Pool) *donations.Service {
	var provider donations.Provider
	if cfg.TossSecretKey != "" {
		provider = donations.NewToss(cfg.TossSecretKey)
	}
	return donations.NewService(db, provider)
}

// SetPaymentProvider replaces the provider (tests use a fake).
func (s *Server) SetPaymentProvider(p donations.Provider) {
	s.donations = donations.NewService(s.db, p)
}

// GET /api/v1/payments/config (public): whether donations are enabled and
// the public test client key for the browser SDK.
func (s *Server) handlePaymentsConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"enabled": s.donations.Enabled(), "clientKey": s.cfg.TossClientKey})
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
	writeJSON(w, http.StatusCreated, map[string]any{"order": order, "clientKey": s.cfg.TossClientKey})
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
