package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/impwy/wbb-live/wbb-backend/internal/chat"
)

// POST /api/v1/broadcasts/{id}/chat (signed in): sends a live chat message.
func (s *Server) handleSendChat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text string `json:"text"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		return
	}
	id := r.PathValue("id")
	if _, err := s.broadcasts.Get(r.Context(), id); err != nil {
		writeDomainError(w, r, err)
		return
	}
	u := authUser(r)
	msg, err := s.chat.Send(r.Context(), chat.Sender{ID: u.ID, DisplayName: u.DisplayName}, id, req.Text)
	switch {
	case errors.Is(err, chat.ErrInvalidText):
		writeError(w, r, http.StatusBadRequest, "invalid_input", "text: must be 1 to 200 characters")
		return
	case errors.Is(err, chat.ErrTooFast):
		writeDomainError(w, r, errRateLimited)
		return
	case err != nil:
		writeError(w, r, http.StatusServiceUnavailable, "chat_unavailable", "Chat is temporarily unavailable.")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"message": msg})
}

// GET /api/v1/broadcasts/{id}/chat/stream (public): Server-Sent Events with
// recent history followed by live messages and periodic keep-alives.
func (s *Server) handleChatStream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.broadcasts.Get(r.Context(), id); err != nil {
		writeDomainError(w, r, err)
		return
	}
	// Subscribe before reading history so nothing falls in between; the
	// client drops duplicates by id.
	live, cancel := s.chat.Listen(id)
	defer cancel()
	history, err := s.chat.History(r.Context(), id)
	if err != nil {
		writeError(w, r, http.StatusServiceUnavailable, "chat_unavailable", "Chat is temporarily unavailable.")
		return
	}
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{}) // long-lived stream
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	send := func(m chat.Message) error {
		raw, _ := json.Marshal(m)
		if _, err := fmt.Fprintf(w, "id: %s\ndata: %s\n\n", m.ID, raw); err != nil {
			return err
		}
		return rc.Flush()
	}
	if _, err := fmt.Fprint(w, "retry: 3000\n\n"); err != nil {
		return
	}
	for _, m := range history {
		if send(m) != nil {
			return
		}
	}
	_ = rc.Flush()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case m := <-live:
			if send(m) != nil {
				return
			}
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil || rc.Flush() != nil {
				return
			}
		}
	}
}
