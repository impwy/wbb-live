// Package chat implements live broadcast chat. Messages are ephemeral: they
// are fanned out through Redis pub/sub so every API instance can serve them,
// and the newest messages are kept briefly in Redis for late joiners. Each
// process holds a single pattern subscription and dispatches to its local
// listeners, so viewer count does not multiply Redis connections.
package chat

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	ErrInvalidText = errors.New("chat text must be 1 to 200 characters")
	ErrTooFast     = errors.New("chat messages are too frequent")
)

const (
	maxText     = 200
	historySize = 50
	historyTTL  = 24 * time.Hour
	minInterval = 700 * time.Millisecond
	channelPfx  = "wbb:chat:msg:"
)

type Sender struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

type Message struct {
	ID          string    `json:"id"`
	BroadcastID string    `json:"broadcastId"`
	Sender      Sender    `json:"sender"`
	Text        string    `json:"text"`
	SentAt      time.Time `json:"sentAt"`
}

type Service struct {
	rdb   redis.UniversalClient
	once  sync.Once
	mu    sync.Mutex
	rooms map[string]map[chan Message]struct{}
}

func NewService(rdb redis.UniversalClient) *Service {
	return &Service{rdb: rdb, rooms: map[string]map[chan Message]struct{}{}}
}

func historyKey(broadcastID string) string { return "wbb:chat:hist:" + broadcastID }

// Send publishes a message from a signed-in user. The caller has already
// verified that the broadcast exists.
func (s *Service) Send(ctx context.Context, sender Sender, broadcastID, text string) (Message, error) {
	text = strings.TrimSpace(text)
	if text == "" || len([]rune(text)) > maxText {
		return Message{}, ErrInvalidText
	}
	ok, err := s.rdb.SetNX(ctx, "wbb:chat:rl:"+sender.ID, 1, minInterval).Result()
	if err != nil {
		return Message{}, err
	}
	if !ok {
		return Message{}, ErrTooFast
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	msg := Message{ID: hex.EncodeToString(b), BroadcastID: broadcastID, Sender: sender, Text: text, SentAt: time.Now().UTC()}
	raw, _ := json.Marshal(msg)
	pipe := s.rdb.TxPipeline()
	pipe.LPush(ctx, historyKey(broadcastID), raw)
	pipe.LTrim(ctx, historyKey(broadcastID), 0, historySize-1)
	pipe.Expire(ctx, historyKey(broadcastID), historyTTL)
	pipe.Publish(ctx, channelPfx+broadcastID, raw)
	if _, err := pipe.Exec(ctx); err != nil {
		return Message{}, err
	}
	return msg, nil
}

// History returns the newest messages, oldest first.
func (s *Service) History(ctx context.Context, broadcastID string) ([]Message, error) {
	raws, err := s.rdb.LRange(ctx, historyKey(broadcastID), 0, historySize-1).Result()
	if err != nil {
		return nil, err
	}
	out := make([]Message, 0, len(raws))
	for i := len(raws) - 1; i >= 0; i-- {
		var m Message
		if json.Unmarshal([]byte(raws[i]), &m) == nil {
			out = append(out, m)
		}
	}
	return out, nil
}

// Listen registers a local listener for a broadcast. The returned cancel
// function must be called when the listener goes away.
func (s *Service) Listen(broadcastID string) (<-chan Message, func()) {
	s.once.Do(func() { go s.dispatch() })
	ch := make(chan Message, 64)
	s.mu.Lock()
	room := s.rooms[broadcastID]
	if room == nil {
		room = map[chan Message]struct{}{}
		s.rooms[broadcastID] = room
	}
	room[ch] = struct{}{}
	s.mu.Unlock()
	return ch, func() {
		s.mu.Lock()
		delete(s.rooms[broadcastID], ch)
		if len(s.rooms[broadcastID]) == 0 {
			delete(s.rooms, broadcastID)
		}
		s.mu.Unlock()
	}
}

// dispatch holds the process-wide subscription; go-redis reconnects it.
func (s *Service) dispatch() {
	sub := s.rdb.PSubscribe(context.Background(), channelPfx+"*")
	for m := range sub.Channel() {
		var msg Message
		if json.Unmarshal([]byte(m.Payload), &msg) != nil {
			continue
		}
		s.mu.Lock()
		for ch := range s.rooms[msg.BroadcastID] {
			select {
			case ch <- msg:
			default: // a slow listener drops messages instead of blocking all
			}
		}
		s.mu.Unlock()
	}
}
